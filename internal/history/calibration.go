package history

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mbl/ocbench/internal/store"
)

// Verdict is how much a task can tell two configurations apart.
type Verdict string

const (
	// VerdictSaturated means every run passed: the task cannot show a quality
	// difference because there is none to show. It still guards against a
	// regression, which is a different job from measuring an improvement.
	VerdictSaturated Verdict = "saturated"
	// VerdictAlwaysFails means no run passed. The task is either broken or far
	// beyond every configuration tried, and separates none of them.
	VerdictAlwaysFails Verdict = "always-fails"
	// VerdictInformative means the pass rate sits between the two extremes,
	// which is where a task earns its keep.
	VerdictInformative Verdict = "informative"
	// VerdictUnknown means there are too few runs to classify. A single run
	// cannot establish a rate.
	VerdictUnknown Verdict = "unknown"
)

// The pass-rate band. A task at or above the saturated bound teaches nothing
// about quality; one at or below the failing bound teaches nothing either.
const (
	saturatedPassRate   = 0.9
	alwaysFailsPassRate = 0.2
)

// processMetrics are the metrics that describe how a run worked rather than
// what it produced. A task where every configuration produces the same tuple
// cannot separate them on process, however hard it is.
var processMetrics = []string{
	"tool_calls_total", "subagent_calls", "retries", "compactions",
}

// TaskCalibration is one task's ability to separate configurations.
type TaskCalibration struct {
	Suite, Task string
	Runs        int
	Profiles    int
	PassRate    float64
	ScoreMean   float64
	Verdict     Verdict
	// DistinctSignatures counts the distinct process signatures observed.
	DistinctSignatures int
	// ProcessVariance is true when at least two configurations worked
	// differently on this task, which is what lets it separate them on
	// strategy. It is false both when every configuration worked identically
	// and when only one configuration has been measured: use Profiles to tell
	// those apart before concluding the task cannot discriminate.
	ProcessVariance bool
	// ProcessKnown is false when no run recorded the process metrics, so the
	// absence of variance is unmeasured rather than observed.
	ProcessKnown bool
}

// CalibrationReport ranks the corpus by how much it can separate.
type CalibrationReport struct {
	Tasks       []TaskCalibration
	Informative int
	Saturated   int
	AlwaysFails int
	Unknown     int
	// NoProcessVariance counts tasks measured under two or more configurations
	// that all worked identically.
	NoProcessVariance int
}

// Calibration classifies each task by pass rate and by whether configurations
// actually behaved differently on it.
//
// It exists because a task everything passes measures nothing about quality,
// and a task every configuration solves the same way measures nothing about
// strategy — however hard the task looks. suite filters by suite name ("" for
// all).
func Calibration(ctx context.Context, st *store.Store, suite string) (CalibrationReport, error) {
	if st == nil {
		return CalibrationReport{}, fmt.Errorf("history: nil store")
	}
	runs, err := st.ListRuns(ctx, 0, "")
	if err != nil {
		return CalibrationReport{}, err
	}

	type taskKey struct{ suite, task string }
	type acc struct {
		runs     int
		passes   float64
		scoreSum float64
		profiles map[string]bool
		// signatures are the distinct process tuples seen on this task, and
		// perProfile records whether each configuration ever varied from its
		// own signature.
		signatures map[string]bool
		process    bool
	}
	tasks := map[taskKey]*acc{}
	var order []taskKey

	for _, r := range runs {
		if suite != "" && r.SuiteName != suite {
			continue
		}
		metrics, err := st.GetRunMetrics(ctx, r.ID)
		if err != nil {
			return CalibrationReport{}, err
		}
		values := metricValues(metrics)

		key := taskKey{r.SuiteName, r.TaskID}
		a := tasks[key]
		if a == nil {
			a = &acc{profiles: map[string]bool{}, signatures: map[string]bool{}}
			tasks[key] = a
			order = append(order, key)
		}
		a.runs++
		a.passes += values["success"]
		a.scoreSum += scoreOf(values)
		a.profiles[r.ProfileHash] = true

		if sig, ok := processSignature(values); ok {
			a.process = true
			a.signatures[sig] = true
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].suite != order[j].suite {
			return order[i].suite < order[j].suite
		}
		return order[i].task < order[j].task
	})

	var rep CalibrationReport
	for _, key := range order {
		a := tasks[key]
		tc := TaskCalibration{
			Suite: key.suite, Task: key.task,
			Runs: a.runs, Profiles: len(a.profiles),
			PassRate:           a.passes / float64(a.runs),
			ScoreMean:          a.scoreSum / float64(a.runs),
			DistinctSignatures: len(a.signatures),
			ProcessKnown:       a.process,
		}
		tc.Verdict = classify(tc.PassRate, tc.Runs)
		tc.ProcessVariance = len(a.signatures) > 1
		if a.process && len(a.profiles) >= 2 && len(a.signatures) == 1 {
			rep.NoProcessVariance++
		}

		switch tc.Verdict {
		case VerdictInformative:
			rep.Informative++
		case VerdictSaturated:
			rep.Saturated++
		case VerdictAlwaysFails:
			rep.AlwaysFails++
		default:
			rep.Unknown++
		}
		rep.Tasks = append(rep.Tasks, tc)
	}
	return rep, nil
}

// classify places a task in the pass-rate band. One run cannot establish a
// rate, so it is reported as unknown rather than as a verdict the data does not
// support.
func classify(passRate float64, runs int) Verdict {
	if runs < 2 {
		return VerdictUnknown
	}
	switch {
	case passRate >= saturatedPassRate:
		return VerdictSaturated
	case passRate <= alwaysFailsPassRate:
		return VerdictAlwaysFails
	default:
		return VerdictInformative
	}
}

// processSignature renders how a run worked, so two runs can be compared. ok is
// false when the run recorded none of the process metrics, which is different
// from recording zeros.
func processSignature(values map[string]float64) (string, bool) {
	parts := make([]string, 0, len(processMetrics))
	known := false
	for _, name := range processMetrics {
		if v, ok := values[name]; ok {
			known = true
			parts = append(parts, fmt.Sprintf("%s=%d", name, int(v)))
			continue
		}
		parts = append(parts, name+"=?")
	}
	if !known {
		return "", false
	}
	return strings.Join(parts, ","), true
}
