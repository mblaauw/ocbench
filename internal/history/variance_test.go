package history_test

import (
	"context"
	"testing"

	"mbl/ocbench/internal/history"
)

// findTask returns the variance entry for a task, failing when it is absent.
func findTask(t *testing.T, rep history.VarianceReport, task string) history.TaskVariance {
	t.Helper()
	for _, tv := range rep.Tasks {
		if tv.Task == task {
			return tv
		}
	}
	t.Fatalf("task %q missing from the report", task)
	return history.TaskVariance{}
}

// findAxis returns one axis of a task, failing when it is absent.
func findAxis(t *testing.T, tv history.TaskVariance, metric string) history.AxisVariance {
	t.Helper()
	for _, av := range tv.Axes {
		if av.Metric == metric {
			return av
		}
	}
	t.Fatalf("axis %q missing for task %q", metric, tv.Task)
	return history.AxisVariance{}
}

func TestVarianceMeasuresNoiseFromRepeats(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	// Three repeats of one config on one task: mean 100, sample SD 10, so the
	// relative spread is 10%.
	seedScoredRun(t, st, "r1", "2026-01-01T00:00:01Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 90, 0.001)
	seedScoredRun(t, st, "r2", "2026-01-01T00:00:02Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 100, 0.001)
	seedScoredRun(t, st, "r3", "2026-01-01T00:00:03Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 110, 0.001)
	// A task measured once yields no noise estimate at all.
	seedScoredRun(t, st, "r4", "2026-01-01T00:00:04Z", "p1", "hash-1", suiteName, "task-b", 1, 1, 50, 0.001)

	rep, err := history.Variance(context.Background(), st, "", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if rep.TotalRuns != 4 {
		t.Errorf("TotalRuns = %d, want 4", rep.TotalRuns)
	}
	if rep.SingleRunTasks != 1 {
		t.Errorf("SingleRunTasks = %d, want 1", rep.SingleRunTasks)
	}
	if rep.RepeatedTasks != 1 {
		t.Errorf("RepeatedTasks = %d, want 1", rep.RepeatedTasks)
	}

	a := findTask(t, rep, "task-a")
	if a.Runs != 3 || a.Profiles != 1 || a.RepeatedGroups != 1 {
		t.Errorf("task-a = runs %d, profiles %d, repeated groups %d; want 3, 1, 1",
			a.Runs, a.Profiles, a.RepeatedGroups)
	}
	tokens := findAxis(t, a, "tokens_total")
	if tokens.Samples != 3 {
		t.Errorf("tokens samples = %d, want 3", tokens.Samples)
	}
	if tokens.RelSD < 0.099 || tokens.RelSD > 0.101 {
		t.Errorf("tokens RelSD = %v, want ~0.10", tokens.RelSD)
	}
	// Detecting a 10% effect at a 10% relative spread needs ~16 repeats.
	if tokens.Repeats10 < 15 || tokens.Repeats10 > 17 {
		t.Errorf("tokens Repeats10 = %d, want ~16", tokens.Repeats10)
	}
	// A smaller effect always needs more repeats.
	if tokens.Repeats5 <= tokens.Repeats10 {
		t.Errorf("Repeats5 = %d, want more than Repeats10 = %d", tokens.Repeats5, tokens.Repeats10)
	}

	// The singly-measured task is reported, but carries no noise estimate.
	b := findTask(t, rep, "task-b")
	if b.Runs != 1 || b.RepeatedGroups != 0 {
		t.Errorf("task-b = runs %d, repeated groups %d; want 1, 0", b.Runs, b.RepeatedGroups)
	}
	if len(b.Axes) != 0 {
		t.Errorf("task-b reported %d axes from a single run, want none", len(b.Axes))
	}
}

// Two profiles with the same relative spread but different absolute levels must
// pool to that relative spread. Normalising inside each group is what keeps a
// merely faster profile from being reported as noise.
func TestVarianceNormalisesWithinEachGroup(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	seedProfile(t, st, "p2", "hash-2", components("h2"))
	// Profile one: mean 100, 10% spread.
	seedScoredRun(t, st, "a1", "2026-01-01T00:00:01Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 90, 0.001)
	seedScoredRun(t, st, "a2", "2026-01-01T00:00:02Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 100, 0.001)
	seedScoredRun(t, st, "a3", "2026-01-01T00:00:03Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 110, 0.001)
	// Profile two: mean 200 — twice the level, same 10% spread.
	seedScoredRun(t, st, "b1", "2026-01-01T00:00:04Z", "p2", "hash-2", suiteName, "task-a", 1, 1, 180, 0.001)
	seedScoredRun(t, st, "b2", "2026-01-01T00:00:05Z", "p2", "hash-2", suiteName, "task-a", 1, 1, 200, 0.001)
	seedScoredRun(t, st, "b3", "2026-01-01T00:00:06Z", "p2", "hash-2", suiteName, "task-a", 1, 1, 220, 0.001)

	rep, err := history.Variance(context.Background(), st, "", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	a := findTask(t, rep, "task-a")
	if a.RepeatedGroups != 2 {
		t.Fatalf("repeated groups = %d, want 2", a.RepeatedGroups)
	}
	tokens := findAxis(t, a, "tokens_total")
	if tokens.RelSD < 0.09 || tokens.RelSD > 0.11 {
		t.Errorf("pooled RelSD = %v, want ~0.10 (a 2x level difference is not noise)", tokens.RelSD)
	}
}

// A metric that is identical across repeats reports a zero spread, and the
// report must not promise that a single repeat can detect any effect.
func TestVarianceZeroSpreadIsNotAPromise(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	for i, id := range []string{"z1", "z2", "z3"} {
		seedScoredRun(t, st, id, "2026-01-01T00:00:0"+string(rune('1'+i))+"Z",
			"p1", "hash-1", suiteName, "task-a", 1, 1, 100, 0.001)
	}

	rep, err := history.Variance(context.Background(), st, "", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	tokens := findAxis(t, findTask(t, rep, "task-a"), "tokens_total")
	if tokens.RelSD != 0 {
		t.Errorf("RelSD = %v, want 0 for identical repeats", tokens.RelSD)
	}
	if tokens.Repeats10 != 0 {
		t.Errorf("Repeats10 = %d, want 0 (unknown) rather than a promise of 1", tokens.Repeats10)
	}
}

func TestVarianceFiltersBySuiteAndTask(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	seedScoredRun(t, st, "r1", "2026-01-01T00:00:01Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 90, 0.001)
	seedScoredRun(t, st, "r2", "2026-01-01T00:00:02Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 110, 0.001)
	seedScoredRun(t, st, "r3", "2026-01-01T00:00:03Z", "p1", "hash-1", "other", "task-c", 1, 1, 10, 0.001)

	rep, err := history.Variance(context.Background(), st, suiteName, []string{"task-a"})
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if len(rep.Tasks) != 1 || rep.Tasks[0].Task != "task-a" {
		t.Fatalf("filtered report = %+v, want only task-a", rep.Tasks)
	}

	bySuite, err := history.Variance(context.Background(), st, "other", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if len(bySuite.Tasks) != 1 || bySuite.Tasks[0].Suite != "other" {
		t.Fatalf("suite filter = %+v, want only suite other", bySuite.Tasks)
	}
}

// A spread from two or three runs is itself uncertain, and the report must say
// so rather than presenting it as a measured constant.
func TestVarianceMarksThinSpreadsAsIndicative(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	// Two repeats: one degree of freedom, so the spread is indicative.
	seedScoredRun(t, st, "t1", "2026-01-01T00:00:01Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 90, 0.001)
	seedScoredRun(t, st, "t2", "2026-01-01T00:00:02Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 110, 0.001)

	rep, err := history.Variance(context.Background(), st, "", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if tokens := findAxis(t, findTask(t, rep, "task-a"), "tokens_total"); !tokens.Indicative {
		t.Error("a two-run spread was not marked indicative")
	}

	// Five repeats give four degrees of freedom: enough to stop flagging.
	for i := 0; i < 3; i++ {
		id := "u" + string(rune('1'+i))
		seedScoredRun(t, st, id, "2026-01-01T00:00:1"+string(rune('0'+i))+"Z",
			"p1", "hash-1", suiteName, "task-a", 1, 1, float64(95+i*5), 0.001)
	}
	rep, err = history.Variance(context.Background(), st, "", nil)
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	tokens := findAxis(t, findTask(t, rep, "task-a"), "tokens_total")
	if tokens.Indicative {
		t.Errorf("a five-run spread (dof=%d) was still marked indicative", tokens.Samples-1)
	}
}
