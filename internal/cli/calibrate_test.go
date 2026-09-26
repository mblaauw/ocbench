package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
)

// runCalibrateCmd executes the calibrate command with injected deps.
func runCalibrateCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newCalibrateCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// seedCalibrateRun records one run with a process signature.
func seedCalibrateRun(t *testing.T, st *store.Store, id, started, task, profileID, profileHash string, success, toolCalls, subagents float64) {
	t.Helper()
	r := historyBaseRun(id, started, task, profileID, profileHash)
	seedHistoryRun(t, st, r)
	if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
		"score": success, "success": success,
		"tool_calls_total": toolCalls, "subagent_calls": subagents,
	}); err != nil {
		t.Fatalf("metrics: %v", err)
	}
}

func TestCalibrateClassifiesAndReports(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedHistoryProfile(t, st, "p2", "hash-2", nil)

	// Saturated, and both configurations worked identically.
	seedCalibrateRun(t, st, "s1", "2026-01-01T00:00:01Z", cliTaskID, "p1", "hash-1", 1, 5, 0)
	seedCalibrateRun(t, st, "s2", "2026-01-01T00:00:02Z", cliTaskID, "p2", "hash-2", 1, 5, 0)
	// Informative, and the configurations took different routes.
	seedCalibrateRun(t, st, "i1", "2026-01-01T00:01:01Z", "task-mixed", "p1", "hash-1", 1, 4, 0)
	seedCalibrateRun(t, st, "i2", "2026-01-01T00:01:02Z", "task-mixed", "p2", "hash-2", 0, 11, 3)
	// Measured once: unclassifiable.
	seedCalibrateRun(t, st, "u1", "2026-01-01T00:02:01Z", "task-once", "p1", "hash-1", 1, 5, 0)

	out, err := runCalibrateCmd(t, d)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	for _, want := range []string{
		cliTaskID, "saturated", "task-mixed", "informative", "task-once", "unknown",
		"identical across configs", "2 distinct",
		"1 informative, 1 saturated, 0 always-fail, 1 unclassified",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "cannot separate them on strategy") {
		t.Errorf("output does not explain the identical-process finding:\n%s", out)
	}
}

// A corpus where nothing is informative must say so plainly: this is the
// finding the command exists to surface.
func TestCalibrateStatesWhenNothingIsInformative(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedCalibrateRun(t, st, "s1", "2026-01-01T00:00:01Z", cliTaskID, "p1", "hash-1", 1, 5, 0)
	seedCalibrateRun(t, st, "s2", "2026-01-01T00:00:02Z", cliTaskID, "p1", "hash-1", 1, 5, 0)

	out, err := runCalibrateCmd(t, d)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if !strings.Contains(out, "No task is in the informative band") {
		t.Errorf("output does not state that nothing can show a quality difference:\n%s", out)
	}
}

func TestCalibrateJSONAndSuiteFilter(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedCalibrateRun(t, st, "s1", "2026-01-01T00:00:01Z", cliTaskID, "p1", "hash-1", 1, 5, 0)
	seedCalibrateRun(t, st, "s2", "2026-01-01T00:00:02Z", cliTaskID, "p1", "hash-1", 1, 5, 0)

	out, err := runCalibrateCmd(t, d, "--json")
	if err != nil {
		t.Fatalf("calibrate --json: %v", err)
	}
	var got struct {
		Saturated int `json:"saturated"`
		Tasks     []struct {
			Task         string  `json:"task"`
			Verdict      string  `json:"verdict"`
			PassRate     float64 `json:"pass_rate"`
			ProcessKnown bool    `json:"process_known"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if got.Saturated != 1 || len(got.Tasks) != 1 {
		t.Fatalf("envelope = %+v", got)
	}
	if got.Tasks[0].Verdict != "saturated" || got.Tasks[0].PassRate != 1 || !got.Tasks[0].ProcessKnown {
		t.Errorf("task = %+v", got.Tasks[0])
	}

	// A suite name that matches nothing yields an empty report, not an error.
	empty, err := runCalibrateCmd(t, d, "absent-suite")
	if err != nil {
		t.Fatalf("calibrate absent-suite: %v", err)
	}
	if !strings.Contains(empty, "No runs recorded") {
		t.Errorf("empty report = %q", empty)
	}
}
