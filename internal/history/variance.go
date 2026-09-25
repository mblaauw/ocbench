package history

import (
	"context"
	"fmt"
	"math"
	"sort"

	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
)

// varianceAxes are the metrics the noise report covers, in render order.
// tokens_total and tokens_cache_read are the axes a configuration actually
// moves; tool_calls_total and duration_ms explain why it moved; score is the
// outcome. An axis with no observations is omitted rather than reported as zero.
var varianceAxes = []string{
	"tokens_total", "tokens_cache_read", "tool_calls_total", "duration_ms", "score",
}

// AxisVariance is one metric's measured noise floor, pooled across the repeated
// (task, profile) groups that observed it.
type AxisVariance struct {
	Metric string
	// Groups is how many repeated groups contributed, Samples how many runs.
	Groups  int
	Samples int
	// RelSD is the pooled within-group relative standard deviation, as a
	// fraction of the mean. Normalising inside each group removes the level
	// difference between configurations, so a merely cheaper profile is not
	// reported as noise.
	RelSD float64
	// Repeats5 and Repeats10 are the repeats per arm needed to detect a 5% and
	// a 10% difference at the conventional power and alpha. Zero means the
	// observed spread does not support an estimate — including the case where
	// every repeat was identical.
	Repeats5  int
	Repeats10 int
	// Indicative marks a spread estimated from too few observations to trust on
	// its own. The relative standard error of a sample standard deviation is
	// about 1/sqrt(2*dof), so two or three runs put the spread itself out by
	// 50-70%; the figure is a reason to run more, not a result.
	Indicative bool
}

// indicativeDOF is the pooled degrees of freedom below which a spread is
// reported as indicative rather than as an estimate.
const indicativeDOF = 4

// MDEAt returns the relative effect detectable with n repeats per arm.
func (a AxisVariance) MDEAt(n int) float64 {
	return stats.MDEForRelSD(a.RelSD, n, stats.DefaultPower, stats.DefaultAlpha)
}

// TaskVariance is one task's coverage and measured noise floor.
type TaskVariance struct {
	Suite, Task string
	Runs        int
	Profiles    int
	// RepeatedGroups counts the (task, profile) groups with two or more runs.
	// Zero means no configuration was ever repeated on this task, so there is
	// no noise estimate: running three different configurations once each is
	// not repetition.
	RepeatedGroups int
	Axes           []AxisVariance
}

// VarianceReport is the whole picture: what has been repeated, and how large an
// effect those repeats could detect.
type VarianceReport struct {
	Tasks     []TaskVariance
	TotalRuns int
	// SingleRunTasks counts tasks measured exactly once.
	SingleRunTasks int
	// RepeatedTasks counts tasks with at least one repeated group.
	RepeatedTasks int
}

// Variance measures the run-to-run noise floor of each task from the runs that
// repeated the same configuration on it.
//
// A task whose configurations were each run once yields coverage but no noise
// estimate, and the report says so rather than inventing a spread from a single
// sample. suite filters by suite name ("" for all) and tasks by task id (nil for
// all).
func Variance(ctx context.Context, st *store.Store, suite string, tasks []string) (VarianceReport, error) {
	if st == nil {
		return VarianceReport{}, fmt.Errorf("history: nil store")
	}
	runs, err := st.ListRuns(ctx, 0, "")
	if err != nil {
		return VarianceReport{}, err
	}
	wantTask := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		wantTask[t] = true
	}

	type taskKey struct{ suite, task string }
	type groupKey struct{ suite, task, profile string }

	taskRuns := map[taskKey]int{}
	taskProfiles := map[taskKey]map[string]bool{}
	groupRuns := map[groupKey]int{}
	groupValues := map[groupKey]map[string][]float64{}

	var rep VarianceReport
	for _, r := range runs {
		if suite != "" && r.SuiteName != suite {
			continue
		}
		if len(wantTask) > 0 && !wantTask[r.TaskID] {
			continue
		}
		rep.TotalRuns++

		metrics, err := st.GetRunMetrics(ctx, r.ID)
		if err != nil {
			return VarianceReport{}, err
		}
		values := metricValues(metrics)

		tk := taskKey{r.SuiteName, r.TaskID}
		taskRuns[tk]++
		if taskProfiles[tk] == nil {
			taskProfiles[tk] = map[string]bool{}
		}
		taskProfiles[tk][r.ProfileHash] = true

		gk := groupKey{r.SuiteName, r.TaskID, r.ProfileHash}
		groupRuns[gk]++
		if groupValues[gk] == nil {
			groupValues[gk] = map[string][]float64{}
		}
		for _, axis := range varianceAxes {
			if v, ok := values[axis]; ok {
				groupValues[gk][axis] = append(groupValues[gk][axis], v)
			}
		}
	}

	repeatedGroups := map[taskKey]int{}
	for gk, n := range groupRuns {
		if n >= 2 {
			repeatedGroups[taskKey{gk.suite, gk.task}]++
		}
	}

	keys := make([]taskKey, 0, len(taskRuns))
	for tk := range taskRuns {
		keys = append(keys, tk)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].suite != keys[j].suite {
			return keys[i].suite < keys[j].suite
		}
		return keys[i].task < keys[j].task
	})

	for _, tk := range keys {
		tv := TaskVariance{
			Suite: tk.suite, Task: tk.task,
			Runs:           taskRuns[tk],
			Profiles:       len(taskProfiles[tk]),
			RepeatedGroups: repeatedGroups[tk],
		}
		if tv.Runs == 1 {
			rep.SingleRunTasks++
		}
		if tv.RepeatedGroups > 0 {
			rep.RepeatedTasks++
		}
		for _, axis := range varianceAxes {
			// Pool the within-group variances, each expressed relative to its
			// own mean. Concatenating the groups instead would understate the
			// spread: duplicated deviations inflate the denominator without
			// adding information.
			var weighted, dof float64
			groups, samples := 0, 0
			for gk, axes := range groupValues {
				if gk.suite != tk.suite || gk.task != tk.task {
					continue
				}
				vals := axes[axis]
				if len(vals) < 2 {
					continue
				}
				mean := stats.Mean(vals)
				if mean == 0 {
					// A zero mean cannot be normalised; the relative spread
					// of this group is undefined rather than infinite.
					continue
				}
				sd, ok := stats.StdDev(vals)
				if !ok {
					continue
				}
				relVar := (sd * sd) / (mean * mean)
				weight := float64(len(vals) - 1)
				weighted += weight * relVar
				dof += weight
				groups++
				samples += len(vals)
			}
			if groups == 0 || dof == 0 {
				continue
			}
			relSD := math.Sqrt(weighted / dof)
			av := AxisVariance{
				Metric: axis, Groups: groups, Samples: samples, RelSD: relSD,
				Indicative: dof < indicativeDOF,
			}
			if n, ok := stats.RepeatsForSD(relSD, 0.05, stats.DefaultPower, stats.DefaultAlpha); ok {
				av.Repeats5 = n
			}
			if n, ok := stats.RepeatsForSD(relSD, 0.10, stats.DefaultPower, stats.DefaultAlpha); ok {
				av.Repeats10 = n
			}
			tv.Axes = append(tv.Axes, av)
		}
		rep.Tasks = append(rep.Tasks, tv)
	}
	return rep, nil
}
