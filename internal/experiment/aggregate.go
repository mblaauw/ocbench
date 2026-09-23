package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
)

// wilsonZ is the standard-normal quantile for a two-sided 95% interval, the
// level spec §12.3 fixes for the reported pass-rate interval.
const wilsonZ = 1.96

// The permutation test is seeded and bounded so a fixed input yields the same
// p-value on every run and machine, as spec §12.3 requires.
const (
	permutationIters = 10000
	permutationSeed  = 20260923
)

// StatTest is one derived permutation test. Observed is signed so a positive
// value always means "the comparison arm is worse"; P is the two-sided
// permutation p-value. BaselineValue and ArmValue are the statistics that were
// permuted, so the regression gate is applied to the same values the test
// measured. Applicable is false when there are no comparable samples, in which
// case the other fields are meaningless.
type StatTest struct {
	Observed      float64
	P             float64
	N             int
	Applicable    bool
	BaselineValue float64
	ArmValue      float64
}

// ArmTaskStats is the derived statistics for one (arm, task) pair.
type ArmTaskStats struct {
	Executions, Successes            int
	PassRate, WilsonLo, WilsonHi     float64
	PassAtK, PassAllK                bool
	MedianTokens, Q1Tokens, Q3Tokens float64
	MedianCost, MedianDurationMS     float64
}

// TaskSummary is the per-arm statistics for one task.
type TaskSummary struct {
	TaskID string
	PerArm map[string]ArmTaskStats
}

// ExperimentSummary is the read-time aggregate for one experiment. Regression
// is nil until a caller runs DecideRegression with a chosen baseline.
type ExperimentSummary struct {
	Experiment             store.ExperimentRow
	Arms                   []store.ExperimentArmRow
	Tasks                  []TaskSummary
	CostPerSolved          map[string]float64
	DriftWarnings          []string
	SignificanceSuppressed bool
	InsufficientData       bool
	Regression             *RegressionDecision
	// PassRateTests and CostTests are keyed by arm label and cover every
	// non-baseline arm, so the decision never re-derives a comparison.
	PassRateTests map[string]StatTest
	CostTests     map[string]StatTest
}

// RegressionDecision is the outcome of applying spec §12.4 to a summary. Arm
// names the worst regressed arm and is empty when nothing regressed.
type RegressionDecision struct {
	Arm       string
	Regressed bool
	Reason    string
}

// armTaskAgg accumulates the raw observations of one (arm, task) pair while the
// summary is built.
type armTaskAgg struct {
	executions int
	successes  int
	costSum    float64
	tokens     []float64
	costs      []float64
	durations  []float64
}

// taskWeight returns the pooled-pass-rate weight of a task. Every task weighs 1
// in this slice; the indirection exists so a later slice can return per-task
// weights without changing the decision call sites.
func taskWeight(string) float64 { return 1 }

// Summarize reads an experiment's arms, runs, metrics and validations and
// derives the per-task and per-arm statistics, the drift guard, the
// insufficient-data flag and one pair of permutation tests per non-baseline
// arm. It performs no regression decision; that is DecideRegression's pure
// interpretation.
//
// baseline names the reference arm. An empty baseline falls back to the one
// recorded in the experiment spec, then to the first arm in label order,
// mirroring spec §12.1.
func Summarize(ctx context.Context, st *store.Store, experimentID, baseline string, alpha float64) (ExperimentSummary, error) {
	exp, err := st.GetExperiment(ctx, experimentID)
	if err != nil {
		return ExperimentSummary{}, err
	}
	arms, err := st.ListExperimentArms(ctx, experimentID)
	if err != nil {
		return ExperimentSummary{}, err
	}
	runs, err := st.RunsForExperiment(ctx, experimentID)
	if err != nil {
		return ExperimentSummary{}, err
	}

	labelByArm := make(map[string]string, len(arms))
	for _, a := range arms {
		labelByArm[a.ID] = a.Label
	}

	grouped := map[string]map[string]*armTaskAgg{}
	taskRuns := map[string][]store.RunRow{}
	for _, run := range runs {
		// Runs with no arm belong to single-profile runs, not to a comparison.
		if run.ArmID == nil {
			continue
		}
		label, ok := labelByArm[*run.ArmID]
		if !ok {
			continue
		}
		metrics, err := st.GetRunMetrics(ctx, run.ID)
		if err != nil {
			return ExperimentSummary{}, err
		}
		validations, err := st.ListRunValidations(ctx, run.ID)
		if err != nil {
			return ExperimentSummary{}, err
		}
		if grouped[label] == nil {
			grouped[label] = map[string]*armTaskAgg{}
		}
		agg := grouped[label][run.TaskID]
		if agg == nil {
			agg = &armTaskAgg{}
			grouped[label][run.TaskID] = agg
		}
		agg.executions++
		if runSucceeded(run, metrics, validations) {
			agg.successes++
		}
		tokens, _ := metricValue(metrics, "tokens_total")
		cost, _ := metricValue(metrics, "cost")
		duration, _ := metricValue(metrics, "duration_ms")
		agg.tokens = append(agg.tokens, tokens)
		agg.costs = append(agg.costs, cost)
		agg.durations = append(agg.durations, duration)
		agg.costSum += cost
		taskRuns[run.TaskID] = append(taskRuns[run.TaskID], run)
	}

	taskIDs := make([]string, 0, len(taskRuns))
	for id := range taskRuns {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)

	tasks := make([]TaskSummary, 0, len(taskIDs))
	for _, id := range taskIDs {
		ts := TaskSummary{TaskID: id, PerArm: map[string]ArmTaskStats{}}
		for _, a := range arms {
			if agg := grouped[a.Label][id]; agg != nil {
				ts.PerArm[a.Label] = summarizeArmTask(agg)
			}
		}
		tasks = append(tasks, ts)
	}

	// Cost per solved task, per arm: a task's value is its total cost divided
	// by its successes; the arm's value is the median across such tasks.
	costByTask := map[string]map[string]float64{}
	costPerSolved := map[string]float64{}
	for _, a := range arms {
		perTask := map[string]float64{}
		var vals []float64
		for _, id := range taskIDs {
			agg := grouped[a.Label][id]
			if agg == nil || agg.successes == 0 {
				continue
			}
			v := agg.costSum / float64(agg.successes)
			perTask[id] = v
			vals = append(vals, v)
		}
		if len(vals) > 0 {
			costByTask[a.Label] = perTask
			costPerSolved[a.Label] = stats.Median(vals)
		}
	}

	summary := ExperimentSummary{
		Experiment:    *exp,
		Arms:          arms,
		Tasks:         tasks,
		CostPerSolved: costPerSolved,
		PassRateTests: map[string]StatTest{},
		CostTests:     map[string]StatTest{},
	}
	summary.DriftWarnings, summary.SignificanceSuppressed = driftWarnings(taskIDs, taskRuns)
	summary.InsufficientData = insufficientData(arms, tasks)

	baseline = baselineLabel(baseline, exp, arms)
	basePass := passRateSample(tasks, baseline)
	for _, a := range arms {
		if a.Label == baseline {
			continue
		}
		armPass := passRateSample(tasks, a.Label)
		baseMean, armMean := sampleMean(basePass), sampleMean(armPass)
		summary.PassRateTests[a.Label] = StatTest{
			Observed:      baseMean - armMean,
			P:             permutationP(basePass, armPass),
			N:             len(basePass) + len(armPass),
			Applicable:    len(basePass) > 0 && len(armPass) > 0,
			BaselineValue: baseMean,
			ArmValue:      armMean,
		}
		baseCost := sharedCosts(taskIDs, costByTask, baseline, a.Label, true)
		armCost := sharedCosts(taskIDs, costByTask, baseline, a.Label, false)
		baseMedian, armMedian := stats.Median(baseCost), stats.Median(armCost)
		summary.CostTests[a.Label] = StatTest{
			Observed:      armMedian - baseMedian,
			P:             permutationPMedian(baseCost, armCost),
			N:             len(baseCost) + len(armCost),
			Applicable:    len(baseCost) > 0 && len(armCost) > 0,
			BaselineValue: baseMedian,
			ArmValue:      armMedian,
		}
	}
	return summary, nil
}

// DecideRegression is a pure interpreter of a summary: it never reads the
// store. It short-circuits on the drift and insufficient-data guards, then
// applies spec §12.4 to every non-baseline arm and returns the worst outcome
// (the regressed arm with the strongest evidence). Arm is empty when nothing
// regressed, and Reason names the rules evaluated.
func DecideRegression(s ExperimentSummary, alpha float64) RegressionDecision {
	if s.InsufficientData {
		return RegressionDecision{Reason: "insufficient data"}
	}
	if s.SignificanceSuppressed {
		return RegressionDecision{Reason: "significance suppressed: " + strings.Join(s.DriftWarnings, "; ")}
	}

	arms := sortedTestArms(s.PassRateTests)
	if len(arms) == 0 {
		return RegressionDecision{Reason: "no regression: no comparison arm"}
	}

	var (
		worst  *RegressionDecision
		worstP = 1.0
		reason []string
	)
	for _, arm := range arms {
		d, p := decideArm(s, arm, alpha)
		if d.Regressed {
			if worst == nil || p < worstP {
				candidate := d
				worst, worstP = &candidate, p
			}
			continue
		}
		reason = append(reason, d.Reason)
	}
	if worst != nil {
		return *worst
	}
	return RegressionDecision{Reason: "no regression: " + strings.Join(reason, "; ")}
}

// decideArm applies spec §12.4 to one non-baseline arm and returns the
// decision together with the p-value that triggered it (1 when no regression),
// so the caller can pick the worst outcome.
func decideArm(s ExperimentSummary, arm string, alpha float64) (RegressionDecision, float64) {
	pass := s.PassRateTests[arm]
	passSignificant := pass.Applicable && pass.P < alpha
	if passSignificant && pass.Observed > 0 {
		return RegressionDecision{Arm: arm, Regressed: true, Reason: fmt.Sprintf(
			"regression: arm %s pass rate %.4f below baseline %.4f (p=%.4f < %.4f)",
			arm, pass.ArmValue, pass.BaselineValue, pass.P, alpha)}, pass.P
	}
	cost := s.CostTests[arm]
	if !passSignificant && cost.Applicable && cost.P < alpha && exceedsBy25(cost.BaselineValue, cost.ArmValue) {
		return RegressionDecision{Arm: arm, Regressed: true, Reason: fmt.Sprintf(
			"regression: arm %s median cost per solved task %.4f exceeds baseline %.4f by more than 25%% (p=%.4f < %.4f)",
			arm, cost.ArmValue, cost.BaselineValue, cost.P, alpha)}, cost.P
	}
	if passSignificant {
		return RegressionDecision{Arm: arm, Reason: fmt.Sprintf(
			"arm %s: pass rate differs but arm is not worse (p=%.4f)", arm, pass.P)}, 1
	}
	if cost.Applicable {
		return RegressionDecision{Arm: arm, Reason: fmt.Sprintf(
			"arm %s: pass rates indistinguishable (p=%.4f); cost per solved task rule evaluated (p=%.4f)",
			arm, pass.P, cost.P)}, 1
	}
	return RegressionDecision{Arm: arm, Reason: fmt.Sprintf(
		"arm %s: pass rate rule evaluated (p=%.4f); cost per solved task rule not applicable", arm, pass.P)}, 1
}

// sortedTestArms returns the arm labels present in a test map in label order.
func sortedTestArms(tests map[string]StatTest) []string {
	arms := make([]string, 0, len(tests))
	for arm := range tests {
		arms = append(arms, arm)
	}
	sort.Strings(arms)
	return arms
}

// runSucceeded reports whether a run counts as a solved task. A failing
// validation vetoes the run; otherwise the persisted success metric is
// authoritative, falling back to the run status when the metric is absent.
func runSucceeded(run store.RunRow, metrics []store.MetricRow, validations []store.ValidationRow) bool {
	for _, v := range validations {
		switch v.Status {
		case "failed", "error", "timeout":
			return false
		}
	}
	if v, ok := metricValue(metrics, "success"); ok {
		return v >= 0.5
	}
	return run.Status == "passed"
}

// metricValue returns the numeric value of a named metric.
func metricValue(metrics []store.MetricRow, name string) (float64, bool) {
	for _, m := range metrics {
		if m.Name == name && m.ValueNum != nil {
			return *m.ValueNum, true
		}
	}
	return 0, false
}

// summarizeArmTask renders one accumulated (arm, task) pair.
func summarizeArmTask(agg *armTaskAgg) ArmTaskStats {
	lo, hi := stats.Wilson(agg.successes, agg.executions, wilsonZ)
	q1, q3 := stats.IQR(agg.tokens)
	outcomes := make([]bool, agg.executions)
	for i := range outcomes {
		outcomes[i] = i < agg.successes
	}
	return ArmTaskStats{
		Executions:       agg.executions,
		Successes:        agg.successes,
		PassRate:         float64(agg.successes) / float64(agg.executions),
		WilsonLo:         lo,
		WilsonHi:         hi,
		PassAtK:          stats.PassAtK(outcomes) == 1,
		PassAllK:         stats.PassAllK(outcomes) == 1,
		MedianTokens:     stats.Median(agg.tokens),
		Q1Tokens:         q1,
		Q3Tokens:         q3,
		MedianCost:       stats.Median(agg.costs),
		MedianDurationMS: stats.Median(agg.durations),
	}
}

// passRateSample builds the pooled per-execution success sample (1 for a
// success, 0 otherwise) across every task, scaled by the task weight. With the
// unit weights of this slice the sample mean is the pooled pass rate.
func passRateSample(tasks []TaskSummary, label string) []float64 {
	var out []float64
	for _, ts := range tasks {
		st, ok := ts.PerArm[label]
		if !ok {
			continue
		}
		w := taskWeight(ts.TaskID)
		for i := 0; i < st.Successes; i++ {
			out = append(out, w)
		}
		for i := 0; i < st.Executions-st.Successes; i++ {
			out = append(out, 0)
		}
	}
	return out
}

// sharedCosts returns the per-task cost-per-solved values of one arm for the
// tasks that have at least one success in both arms, in task order.
func sharedCosts(taskIDs []string, costByTask map[string]map[string]float64, baseline, candidate string, fromBaseline bool) []float64 {
	var out []float64
	for _, id := range taskIDs {
		base, okBase := costByTask[baseline][id]
		arm, okArm := costByTask[candidate][id]
		if !okBase || !okArm {
			continue
		}
		if fromBaseline {
			out = append(out, base)
		} else {
			out = append(out, arm)
		}
	}
	return out
}

// permutationP runs the pooled mean-difference permutation test, returning 1
// when either sample is empty.
func permutationP(base, arm []float64) float64 {
	if len(base) == 0 || len(arm) == 0 {
		return 1
	}
	return stats.PermutationP(base, arm, permutationSeed, permutationIters)
}

// permutationPMedian runs the pooled median-difference permutation test,
// returning 1 when either sample is empty. The median statistic matches the
// cost test's Observed value.
func permutationPMedian(base, arm []float64) float64 {
	if len(base) == 0 || len(arm) == 0 {
		return 1
	}
	return stats.PermutationPMedian(base, arm, permutationSeed, permutationIters)
}

// sampleMean returns the arithmetic mean of xs, or 0 for an empty slice.
func sampleMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// exceedsBy25 reports whether the candidate cost is more than 25% above the
// baseline. A zero baseline is only exceeded by a positive candidate.
func exceedsBy25(baseline, candidate float64) bool {
	if baseline > 0 {
		return candidate/baseline > 1.25
	}
	return candidate > 0
}

// baselineLabel resolves the baseline arm label: an explicit request wins,
// otherwise the experiment spec's baseline, otherwise the first arm in label
// order (spec §12.1).
func baselineLabel(requested string, exp *store.ExperimentRow, arms []store.ExperimentArmRow) string {
	if requested != "" {
		return requested
	}
	var spec struct {
		Baseline string `json:"baseline"`
	}
	if err := json.Unmarshal([]byte(exp.SpecJSON), &spec); err == nil && spec.Baseline != "" {
		return spec.Baseline
	}
	if len(arms) > 0 {
		return arms[0].Label
	}
	return ""
}

// driftField is one benchmark-relevant variable checked across arms.
type driftField struct {
	name string
	get  func(store.RunRow) string
}

// driftFields are the seven variables spec §12.3 requires to be named when they
// differ between arms.
var driftFields = []driftField{
	{"model", func(r store.RunRow) string { return r.Model }},
	{"agent", func(r store.RunRow) string { return r.Agent }},
	{"variant", func(r store.RunRow) string { return r.Variant }},
	{"opencode_version", func(r store.RunRow) string { return r.OpenCodeVersion }},
	{"suite_hash", func(r store.RunRow) string { return r.SuiteHash }},
	{"task_version", func(r store.RunRow) string { return r.TaskVersion }},
	{"fixture_sha", func(r store.RunRow) string { return r.FixtureSHA }},
}

// driftWarnings compares each field across the arms within every task and
// returns one warning per differing field, naming the values.
func driftWarnings(taskIDs []string, taskRuns map[string][]store.RunRow) ([]string, bool) {
	var warnings []string
	for _, id := range taskIDs {
		for _, f := range driftFields {
			seen := map[string]bool{}
			for _, run := range taskRuns[id] {
				seen[f.get(run)] = true
			}
			if len(seen) < 2 {
				continue
			}
			values := make([]string, 0, len(seen))
			for v := range seen {
				values = append(values, v)
			}
			sort.Strings(values)
			warnings = append(warnings, fmt.Sprintf("%s differs across arms: %s", f.name, strings.Join(values, ", ")))
		}
	}
	return warnings, len(warnings) > 0
}

// insufficientData reports whether any (arm, task) pair has fewer than three
// executions. An experiment with arms but no attributed runs is insufficient.
func insufficientData(arms []store.ExperimentArmRow, tasks []TaskSummary) bool {
	if len(arms) > 0 && len(tasks) == 0 {
		return true
	}
	for _, ts := range tasks {
		for _, a := range arms {
			if ts.PerArm[a.Label].Executions < 3 {
				return true
			}
		}
	}
	return false
}
