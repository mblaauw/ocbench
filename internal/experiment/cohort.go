package experiment

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
)

// CohortArm is one configuration's cost-and-context standing within one
// controlled experiment. It never includes wall-clock duration: duration is a
// runner diagnostic, not a cross-runner efficiency rank.
type CohortArm struct {
	Label              string
	ProfileHash        string
	Runs               int
	TaskCount          int
	MinRepeats         int
	PassRate           float64
	CostPerSolved      float64
	CostPerSolvedOK    bool
	MedianTokens       float64
	MedianTokensOK     bool
	RunnerEnvironments []string
	Eligible           bool
}

// CohortSummary is the evidence-gated, cost-and-context interpretation of one
// persisted experiment. Experiments are the controlled cohorts: they already
// pin the suite, selected tasks, arms and repeats before any run is written.
type CohortSummary struct {
	Experiment         store.ExperimentRow
	Arms               []CohortArm
	RunnerEnvironments []string
	Ranked             bool
	Gate               string
	DriftWarnings      []string
}

// SummarizeCohort derives the portable standing for an experiment. Every arm
// must have at least three successful executions per selected task before the
// cohort can rank configurations by cost. This prevents a cheap failure or a
// single lucky run from appearing efficient.
func SummarizeCohort(ctx context.Context, st *store.Store, experimentID string) (CohortSummary, error) {
	summary, err := Summarize(ctx, st, experimentID, "")
	if err != nil {
		return CohortSummary{}, err
	}
	runs := summary.runs

	labelByArm := make(map[string]string, len(summary.Arms))
	for _, arm := range summary.Arms {
		labelByArm[arm.ID] = arm.Label
	}
	envsByArm := make(map[string]map[string]bool, len(summary.Arms))
	allEnvs := map[string]bool{}
	for _, run := range runs {
		if run.ArmID == nil || run.DryRun {
			continue
		}
		label, ok := labelByArm[*run.ArmID]
		if !ok {
			continue
		}
		env := run.RunnerEnv
		if env == "" {
			env = "unknown"
		}
		if envsByArm[label] == nil {
			envsByArm[label] = map[string]bool{}
		}
		envsByArm[label][env] = true
		allEnvs[env] = true
	}

	out := CohortSummary{
		Experiment:         summary.Experiment,
		RunnerEnvironments: sortedStrings(allEnvs),
		DriftWarnings:      append([]string(nil), summary.DriftWarnings...),
	}
	taskIDs := cohortTaskIDs(summary)
	taskByID := make(map[string]TaskSummary, len(summary.Tasks))
	for _, task := range summary.Tasks {
		taskByID[task.TaskID] = task
	}
	missingSelectedTask := false
	for _, taskID := range taskIDs {
		if _, ok := taskByID[taskID]; !ok {
			missingSelectedTask = true
		}
	}
	allEligible := len(summary.Arms) >= 2
	for _, arm := range summary.Arms {
		item := CohortArm{
			Label:              arm.Label,
			ProfileHash:        arm.ProfileHash,
			RunnerEnvironments: sortedStrings(envsByArm[arm.Label]),
		}
		allPassed := true
		missingCost := false
		var tokenMedians []float64
		for _, taskID := range taskIDs {
			task, taskExists := taskByID[taskID]
			if !taskExists {
				allPassed = false
				continue
			}
			stats, ok := task.PerArm[arm.Label]
			if !ok {
				allPassed = false
				continue
			}
			item.TaskCount++
			item.Runs += stats.Executions
			if item.TaskCount == 1 || stats.Executions < item.MinRepeats {
				item.MinRepeats = stats.Executions
			}
			if stats.Executions == 0 || stats.Successes != stats.Executions {
				allPassed = false
			}
			if stats.HasTokenMetrics {
				tokenMedians = append(tokenMedians, stats.MedianTokens)
			}
		}
		if item.Runs > 0 {
			var successes int
			for _, taskID := range taskIDs {
				if task, ok := taskByID[taskID]; ok {
					if taskStats, ok := task.PerArm[arm.Label]; ok {
						successes += taskStats.Successes
					}
				}
			}
			item.PassRate = float64(successes) / float64(item.Runs)
		}
		if len(tokenMedians) == len(taskIDs) && len(tokenMedians) > 0 {
			item.MedianTokens = stats.Median(tokenMedians)
			item.MedianTokensOK = true
		}
		item.CostPerSolved, item.CostPerSolvedOK = summary.CostPerSolved[arm.Label]
		missingCost = !item.CostPerSolvedOK
		item.Eligible = item.TaskCount == len(taskIDs) && item.MinRepeats >= 3 && allPassed && item.CostPerSolvedOK
		if !item.Eligible {
			allEligible = false
		}
		if missingCost {
			out.Gate = "cost metrics are required before efficiency can be ranked"
		}
		out.Arms = append(out.Arms, item)
	}

	switch {
	case len(summary.Arms) < 2:
		out.Gate = "needs at least two configuration arms"
	case len(summary.Tasks) == 0:
		out.Gate = "no attributed task runs"
	case missingSelectedTask:
		out.Gate = "a selected task has no recorded runs"
	case summary.SignificanceSuppressed:
		out.Gate = "incompatible benchmark inputs: " + joinWarnings(summary.DriftWarnings)
	case summary.InsufficientData:
		out.Gate = "needs at least three runs per task for every configuration"
	case out.Gate != "":
		// A missing cost metric is a distinct evidence failure from validation.
	case !allEligible:
		out.Gate = "every configuration must pass every task before cost can be ranked"
	default:
		out.Ranked = true
		out.Gate = "eligible: three or more validated runs per task for every configuration"
	}

	sort.SliceStable(out.Arms, func(i, j int) bool {
		a, b := out.Arms[i], out.Arms[j]
		if a.Eligible != b.Eligible {
			return a.Eligible
		}
		if a.Eligible && a.CostPerSolved != b.CostPerSolved {
			return a.CostPerSolved < b.CostPerSolved
		}
		return a.Label < b.Label
	})
	return out, nil
}

// cohortTaskIDs takes the experiment's declared task selection as the source
// of truth. Older/manual experiment rows without that field fall back to the
// observed task list, which is all the store can establish for them.
func cohortTaskIDs(summary ExperimentSummary) []string {
	var spec struct {
		Tasks []string `json:"tasks"`
	}
	if json.Unmarshal([]byte(summary.Experiment.SpecJSON), &spec) == nil && len(spec.Tasks) > 0 {
		seen := make(map[string]bool, len(spec.Tasks))
		out := make([]string, 0, len(spec.Tasks))
		for _, id := range spec.Tasks {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	out := make([]string, 0, len(summary.Tasks))
	for _, task := range summary.Tasks {
		out = append(out, task.TaskID)
	}
	return out
}

func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func joinWarnings(warnings []string) string {
	if len(warnings) == 0 {
		return "unknown drift"
	}
	return strings.Join(warnings, "; ")
}
