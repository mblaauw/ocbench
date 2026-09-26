package history_test

import (
	"context"
	"testing"

	"mbl/ocbench/internal/history"
)

func TestCalibrationClassifiesPassRate(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	seedProfile(t, st, "p2", "hash-2", components("h2"))

	// saturated: every configuration passes
	for i, id := range []string{"s1", "s2"} {
		seedScoredRun(t, st, id, "2026-01-01T00:00:0"+string(rune('1'+i))+"Z",
			"p1", "hash-1", suiteName, "task-easy", 1, 1, 100, 0.001)
	}
	// always fails: no configuration passes
	for i, id := range []string{"f1", "f2"} {
		seedScoredRun(t, st, id, "2026-01-01T00:01:0"+string(rune('1'+i))+"Z",
			"p1", "hash-1", suiteName, "task-broken", 0, 0, 100, 0.001)
	}
	// informative: one pass, one fail
	seedScoredRun(t, st, "i1", "2026-01-01T00:02:01Z", "p1", "hash-1", suiteName, "task-mixed", 1, 1, 100, 0.001)
	seedScoredRun(t, st, "i2", "2026-01-01T00:02:02Z", "p2", "hash-2", suiteName, "task-mixed", 0, 0, 100, 0.001)
	// unknown: a single run cannot be classified
	seedScoredRun(t, st, "u1", "2026-01-01T00:03:01Z", "p1", "hash-1", suiteName, "task-once", 1, 1, 100, 0.001)

	rep, err := history.Calibration(context.Background(), st, "")
	if err != nil {
		t.Fatalf("Calibration: %v", err)
	}
	byTask := map[string]history.TaskCalibration{}
	for _, tc := range rep.Tasks {
		byTask[tc.Task] = tc
	}
	for task, want := range map[string]history.Verdict{
		"task-easy":   history.VerdictSaturated,
		"task-broken": history.VerdictAlwaysFails,
		"task-mixed":  history.VerdictInformative,
		"task-once":   history.VerdictUnknown,
	} {
		if got := byTask[task].Verdict; got != want {
			t.Errorf("%s verdict = %q, want %q", task, got, want)
		}
	}
	if rep.Saturated != 1 || rep.AlwaysFails != 1 || rep.Informative != 1 || rep.Unknown != 1 {
		t.Errorf("counts = %+v", rep)
	}
}

// A task where every configuration took the same number of tool calls cannot
// separate them on process, however it scores.
func TestCalibrationFlagsTasksWithoutProcessVariance(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	seedProfile(t, st, "p2", "hash-2", components("h2"))

	// Both configurations: same tool calls, same delegation. Identical process.
	for i, id := range []string{"a1", "a2"} {
		r := baseRun(id, "2026-01-01T00:00:0"+string(rune('1'+i))+"Z", "p1", "hash-1")
		r.SuiteName, r.TaskID = suiteName, "task-uniform"
		seedRun(t, st, r)
		if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
			"score": 1, "success": 1, "tool_calls_total": 5, "subagent_calls": 0, "files_changed": 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"b1", "b2"} {
		r := baseRun(id, "2026-01-01T00:01:0"+string(rune('1'+i))+"Z", "p2", "hash-2")
		r.SuiteName, r.TaskID = suiteName, "task-uniform"
		seedRun(t, st, r)
		if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
			"score": 1, "success": 1, "tool_calls_total": 5, "subagent_calls": 0, "files_changed": 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A task where the two configurations took different routes.
	for i, id := range []string{"c1", "c2"} {
		r := baseRun(id, "2026-01-01T00:02:0"+string(rune('1'+i))+"Z", "p1", "hash-1")
		r.SuiteName, r.TaskID = suiteName, "task-varied"
		if i == 1 {
			r.ProfileID, r.ProfileHash = "p2", "hash-2"
		}
		seedRun(t, st, r)
		tools := 4.0
		subs := 0.0
		if i == 1 {
			tools, subs = 11, 3
		}
		if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
			"score": 1, "success": 1, "tool_calls_total": tools, "subagent_calls": subs, "files_changed": 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := history.Calibration(context.Background(), st, "")
	if err != nil {
		t.Fatalf("Calibration: %v", err)
	}
	byTask := map[string]history.TaskCalibration{}
	for _, tc := range rep.Tasks {
		byTask[tc.Task] = tc
	}
	uniform := byTask["task-uniform"]
	if uniform.ProcessVariance {
		t.Errorf("task-uniform reported process variance from identical signatures: %+v", uniform)
	}
	varied := byTask["task-varied"]
	if !varied.ProcessVariance {
		t.Errorf("task-varied reported no process variance despite 4 vs 11 tool calls")
	}
	if varied.DistinctSignatures < 2 {
		t.Errorf("task-varied distinct signatures = %d, want at least 2", varied.DistinctSignatures)
	}
}

func TestCalibrationFiltersBySuite(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h1"))
	seedScoredRun(t, st, "r1", "2026-01-01T00:00:01Z", "p1", "hash-1", suiteName, "task-a", 1, 1, 100, 0.001)
	seedScoredRun(t, st, "r2", "2026-01-01T00:00:02Z", "p1", "hash-1", "other", "task-b", 1, 1, 100, 0.001)

	rep, err := history.Calibration(context.Background(), st, "other")
	if err != nil {
		t.Fatalf("Calibration: %v", err)
	}
	if len(rep.Tasks) != 1 || rep.Tasks[0].Suite != "other" {
		t.Fatalf("filtered report = %+v, want only suite other", rep.Tasks)
	}
}
