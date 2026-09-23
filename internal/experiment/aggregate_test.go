package experiment

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
)

const (
	aggSuiteHash = "suite-h"
	aggTaskVer   = "1"
	aggFixture   = "fix-1"
	aggModel     = "p/m"
)

// aggStore opens a migrated temp SQLite store.
func aggStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// aggSeed writes the profile, experiment and one arm row per label and returns
// the label-to-arm-id map.
func aggSeed(t *testing.T, st *store.Store, expID, spec string, labels ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: "p1", ProfileHash: "ph1", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, nil); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{
		ID: expID, Name: "experiment core@1", SpecJSON: spec, CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert experiment: %v", err)
	}
	ids := map[string]string{}
	for i, label := range labels {
		id := fmt.Sprintf("arm-%d", i)
		if err := st.InsertExperimentArm(ctx, store.ExperimentArmRow{
			ID: id, ExperimentID: expID, Label: label,
			ProfileHash: "ph1", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z",
		}); err != nil {
			t.Fatalf("insert arm %s: %v", label, err)
		}
		ids[label] = id
	}
	return ids
}

// aggRun is one synthetic execution. The zero values are replaced by the
// package defaults so a test only states what it varies.
type aggRun struct {
	task     string
	repeat   int
	success  bool
	cost     float64
	tokens   float64
	duration float64
	mutate   func(*store.RunRow)
}

// aggInsert writes one run and its metrics.
func aggInsert(t *testing.T, st *store.Store, expID, armID string, r aggRun) {
	t.Helper()
	ctx := context.Background()
	arm := armID
	dur := int64(r.duration)
	row := store.RunRow{
		ID:              fmt.Sprintf("%s-%s-%d", armID, r.task, r.repeat),
		ExperimentID:    expID,
		ArmID:           &arm,
		RepeatIndex:     r.repeat,
		ProfileID:       "p1",
		ProfileHash:     "ph1",
		SuiteName:       "core",
		SuiteVersion:    "1",
		SuiteHash:       aggSuiteHash,
		TaskID:          r.task,
		TaskVersion:     aggTaskVer,
		FixtureSHA:      aggFixture,
		OpenCodeVersion: "1.18.32",
		OCBenchVersion:  "dev",
		Model:           aggModel,
		Agent:           "build",
		Status:          "passed",
		StartedAt:       "2026-01-01T00:00:00Z",
		DurationMS:      &dur,
	}
	if !r.success {
		row.Status = "failed"
	}
	if r.mutate != nil {
		r.mutate(&row)
	}
	if err := st.InsertRun(ctx, row); err != nil {
		t.Fatalf("insert run %s: %v", row.ID, err)
	}
	success := 0.0
	if r.success {
		success = 1
	}
	if err := st.InsertRunMetrics(ctx, row.ID, map[string]float64{
		"success":      success,
		"cost":         r.cost,
		"tokens_total": r.tokens,
		"duration_ms":  r.duration,
	}); err != nil {
		t.Fatalf("insert metrics %s: %v", row.ID, err)
	}
}

// aggInsertOrphan writes a run with no arm id, as a single-profile run would.
func aggInsertOrphan(t *testing.T, st *store.Store, expID, task string) {
	t.Helper()
	dur := int64(1000)
	if err := st.InsertRun(context.Background(), store.RunRow{
		ID: "orphan-run", ExperimentID: expID, ArmID: nil, RepeatIndex: 0,
		ProfileID: "p1", ProfileHash: "ph1",
		SuiteName: "core", SuiteVersion: "1", SuiteHash: aggSuiteHash,
		TaskID: task, TaskVersion: aggTaskVer, FixtureSHA: aggFixture,
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		Model: aggModel, Agent: "build", Status: "passed",
		StartedAt: "2026-01-01T00:00:00Z", DurationMS: &dur,
	}); err != nil {
		t.Fatalf("insert orphan run: %v", err)
	}
	if err := st.InsertRunMetrics(context.Background(), "orphan-run",
		map[string]float64{"success": 1}); err != nil {
		t.Fatalf("insert orphan metrics: %v", err)
	}
}

// aggSeedRepeats writes count identical repeats for one arm and task.
func aggSeedRepeats(t *testing.T, st *store.Store, expID, armID, task string, count int, success bool, cost, tokens, duration float64) {
	t.Helper()
	for r := 0; r < count; r++ {
		aggInsert(t, st, expID, armID, aggRun{
			task: task, repeat: r, success: success,
			cost: cost, tokens: tokens, duration: duration,
		})
	}
}

func TestSummarizeIdenticalArmsNoRegression(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 3, true, 0.5, 100, 1000)
	}
	// A run with no arm id belongs to a single-profile run and is ignored.
	aggInsertOrphan(t, st, "exp-1", "orphan")

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.Experiment.ID != "exp-1" {
		t.Fatalf("experiment id = %q, want exp-1", s.Experiment.ID)
	}
	if len(s.Arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(s.Arms))
	}
	if len(s.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2 (orphan run must be ignored): %+v", len(s.Tasks), s.Tasks)
	}
	if s.InsufficientData || s.SignificanceSuppressed || len(s.DriftWarnings) != 0 {
		t.Fatalf("guards = insufficient=%v suppressed=%v warnings=%v, want all clear",
			s.InsufficientData, s.SignificanceSuppressed, s.DriftWarnings)
	}

	ts := s.Tasks[0]
	if ts.TaskID != "t1" {
		t.Fatalf("first task = %q, want t1", ts.TaskID)
	}
	base := ts.PerArm["baseline"]
	if base.Executions != 3 || base.Successes != 3 {
		t.Fatalf("baseline executions/successes = %d/%d, want 3/3", base.Executions, base.Successes)
	}
	if base.PassRate != 1 || !base.PassAtK || !base.PassAllK {
		t.Fatalf("baseline pass stats = %+v, want rate 1, pass@k and pass^k", base)
	}
	if base.WilsonHi != 1 || base.WilsonLo <= 0 {
		t.Fatalf("baseline Wilson = %v..%v, want (0,1] at 3/3", base.WilsonLo, base.WilsonHi)
	}
	if base.MedianCost != 0.5 || base.MedianTokens != 100 || base.MedianDurationMS != 1000 {
		t.Fatalf("baseline medians = %v/%v/%v", base.MedianCost, base.MedianTokens, base.MedianDurationMS)
	}
	if base.Q1Tokens != 100 || base.Q3Tokens != 100 {
		t.Fatalf("baseline token IQR = %v..%v, want 100..100", base.Q1Tokens, base.Q3Tokens)
	}
	if s.CostPerSolved["baseline"] != 0.5 {
		t.Fatalf("baseline cost per solved = %v, want 0.5", s.CostPerSolved["baseline"])
	}

	if !s.PassRateTests["candidate"].Applicable || s.PassRateTests["candidate"].Observed != 0 || s.PassRateTests["candidate"].P < stats.DefaultAlpha {
		t.Fatalf("pass-rate test = %+v, want applicable, observed 0, not significant", s.PassRateTests["candidate"])
	}

	d := DecideRegression(s, stats.DefaultAlpha)
	if d.Regressed {
		t.Fatalf("DecideRegression = %+v, want no regression", d)
	}
	if !strings.Contains(d.Reason, "no regression") {
		t.Fatalf("reason = %q, want a no-regression reason", d.Reason)
	}
}

func TestSummarizeArmWorsePassRateRegression(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 3, false, 0.5, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.PassRateTests["candidate"].Observed <= 0 || s.PassRateTests["candidate"].P >= stats.DefaultAlpha {
		t.Fatalf("pass-rate test = %+v, want worse arm with p < %v", s.PassRateTests["candidate"], stats.DefaultAlpha)
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if !d.Regressed {
		t.Fatalf("DecideRegression = %+v, want regression", d)
	}
	if !strings.Contains(strings.ToLower(d.Reason), "pass rate") {
		t.Fatalf("reason = %q, want the pass-rate rule named", d.Reason)
	}
}

func TestSummarizeEqualPassRateCostRegression(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	// A majority of candidate tasks cost 3x; a minority cost the same, so the
	// median-difference test has a non-degenerate null and separates.
	tasks := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}
	for i, task := range tasks {
		cost := 0.5
		if i < 6 {
			cost = 1.5
		}
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 3, true, cost, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.PassRateTests["candidate"].P < stats.DefaultAlpha {
		t.Fatalf("pass-rate test = %+v, want indistinguishable pass rates", s.PassRateTests["candidate"])
	}
	cost := s.CostTests["candidate"]
	if !cost.Applicable || cost.P >= stats.DefaultAlpha || cost.Observed <= 0 {
		t.Fatalf("cost test = %+v, want applicable, worse and significant", cost)
	}
	if cost.BaselineValue != 0.5 || cost.ArmValue != 1.5 {
		t.Fatalf("cost gate values = %v/%v, want 0.5/1.5", cost.BaselineValue, cost.ArmValue)
	}
	if s.CostPerSolved["baseline"] != 0.5 || s.CostPerSolved["candidate"] != 1.5 {
		t.Fatalf("cost per solved = %v, want baseline 0.5 candidate 1.5", s.CostPerSolved)
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if !d.Regressed {
		t.Fatalf("DecideRegression = %+v, want regression", d)
	}
	if !strings.Contains(strings.ToLower(d.Reason), "cost") {
		t.Fatalf("reason = %q, want the cost rule named", d.Reason)
	}
}

func TestSummarizeInsufficientData(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 2, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 2, true, 0.5, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !s.InsufficientData {
		t.Fatalf("InsufficientData = false, want true with two repeats")
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if d.Regressed || d.Reason != "insufficient data" {
		t.Fatalf("DecideRegression = %+v, want insufficient data", d)
	}
}

func TestSummarizeDriftSuppressesSignificance(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		for r := 0; r < 3; r++ {
			aggInsert(t, st, "exp-1", ids["candidate"], aggRun{
				task: task, repeat: r, success: true, cost: 0.5, tokens: 100, duration: 1000,
				mutate: func(row *store.RunRow) { row.Model = "p/other" },
			})
		}
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !s.SignificanceSuppressed {
		t.Fatalf("SignificanceSuppressed = false, want true")
	}
	if len(s.DriftWarnings) == 0 || !strings.Contains(s.DriftWarnings[0], "model") {
		t.Fatalf("DriftWarnings = %v, want a model warning", s.DriftWarnings)
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if d.Regressed {
		t.Fatalf("DecideRegression = %+v, want suppressed, not regressed", d)
	}
	if !strings.HasPrefix(d.Reason, "significance suppressed:") {
		t.Fatalf("reason = %q, want the suppression reason", d.Reason)
	}
}

func TestSummarizeDriftOnNonModelField(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		for r := 0; r < 3; r++ {
			aggInsert(t, st, "exp-1", ids["candidate"], aggRun{
				task: task, repeat: r, success: true, cost: 0.5, tokens: 100, duration: 1000,
				mutate: func(row *store.RunRow) { row.OpenCodeVersion = "9.9.9" },
			})
		}
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !s.SignificanceSuppressed {
		t.Fatalf("SignificanceSuppressed = false, want true")
	}
	found := false
	for _, w := range s.DriftWarnings {
		if strings.Contains(w, "opencode_version") && strings.Contains(w, "9.9.9") {
			found = true
		}
	}
	if !found {
		t.Fatalf("DriftWarnings = %v, want an opencode_version warning naming the value", s.DriftWarnings)
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if d.Regressed || !strings.HasPrefix(d.Reason, "significance suppressed:") {
		t.Fatalf("DecideRegression = %+v, want suppressed", d)
	}
}

func TestSummarizeThreeArmsNamesRegressedArm(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "b", "c")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["b"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["c"], task, 3, false, 0.5, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(s.PassRateTests) != 2 || len(s.CostTests) != 2 {
		t.Fatalf("tests = %d/%d, want one pair per non-baseline arm", len(s.PassRateTests), len(s.CostTests))
	}
	if s.PassRateTests["b"].Observed != 0 || s.PassRateTests["b"].P < stats.DefaultAlpha {
		t.Fatalf("arm b test = %+v, want no signal", s.PassRateTests["b"])
	}
	if s.PassRateTests["c"].Observed <= 0 || s.PassRateTests["c"].P >= stats.DefaultAlpha {
		t.Fatalf("arm c test = %+v, want a worse signal", s.PassRateTests["c"])
	}

	d := DecideRegression(s, stats.DefaultAlpha)
	if !d.Regressed || d.Arm != "c" {
		t.Fatalf("DecideRegression = %+v, want the regressed arm c named", d)
	}
	if !strings.Contains(strings.ToLower(d.Reason), "pass rate") {
		t.Fatalf("reason = %q, want the pass-rate rule named", d.Reason)
	}
}

func TestSummarizeCandidateBetterNoRegression(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 3, true, 0.25, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	cost := s.CostTests["candidate"]
	if !cost.Applicable || cost.Observed >= 0 {
		t.Fatalf("cost test = %+v, want applicable with the arm cheaper", cost)
	}
	if cost.BaselineValue != 0.5 || cost.ArmValue != 0.25 {
		t.Fatalf("cost gate values = %v/%v, want 0.5/0.25", cost.BaselineValue, cost.ArmValue)
	}
	d := DecideRegression(s, stats.DefaultAlpha)
	if d.Regressed || d.Arm != "" {
		t.Fatalf("DecideRegression = %+v, want no regression and no arm", d)
	}
	if !strings.Contains(d.Reason, "no regression") {
		t.Fatalf("reason = %q, want a no-regression reason", d.Reason)
	}
}

func TestSummarizeBorderlineAlpha(t *testing.T) {
	st := aggStore(t)
	ids := aggSeed(t, st, "exp-1", `{"baseline":"baseline"}`, "baseline", "candidate")

	for _, task := range []string{"t1", "t2", "t3"} {
		aggSeedRepeats(t, st, "exp-1", ids["baseline"], task, 3, true, 0.5, 100, 1000)
		aggSeedRepeats(t, st, "exp-1", ids["candidate"], task, 3, false, 0.5, 100, 1000)
	}

	s, err := Summarize(context.Background(), st, "exp-1", "baseline", stats.DefaultAlpha)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	p := s.PassRateTests["candidate"].P
	if p <= 0 || p >= 1 {
		t.Fatalf("candidate p = %v, want a usable borderline value", p)
	}
	if d := DecideRegression(s, p*0.5); d.Regressed {
		t.Fatalf("alpha below p = %+v, want no regression", d)
	}
	d := DecideRegression(s, p+(1-p)*0.5)
	if !d.Regressed || d.Arm != "candidate" {
		t.Fatalf("alpha above p = %+v, want the candidate regressed", d)
	}
}
