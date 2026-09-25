package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
)

// runVarianceCmd executes the variance command with injected deps.
func runVarianceCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newVarianceCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// seedVarianceRuns writes n repeats of one configuration on one task, with the
// given token totals.
func seedVarianceRuns(t *testing.T, st *store.Store, task string, tokens ...float64) {
	t.Helper()
	for i, tok := range tokens {
		id := task + "-" + string(rune('a'+i))
		r := historyBaseRun(id, "2026-01-01T00:00:0"+string(rune('1'+i))+"Z", task, "p1", "hash-1")
		seedHistoryRun(t, st, r)
		if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
			"score": 1, "success": 1, "tokens_total": tok,
		}); err != nil {
			t.Fatalf("metrics: %v", err)
		}
	}
}

func TestVarianceReportsNoiseAndRepeatsNeeded(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	// mean 100, sample SD 10 → 10% relative spread.
	seedVarianceRuns(t, st, cliTaskID, 90, 100, 110)

	out, err := runVarianceCmd(t, d)
	if err != nil {
		t.Fatalf("variance: %v", err)
	}
	for _, want := range []string{
		"tokens_total", "10.0%", cliTaskID,
		"3 run(s) across 1 task(s): 1 repeated, 0 measured once",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// A 10% spread with five repeats per arm detects about a 17.7% effect:
	// 2.8016 * 0.10 * sqrt(2/5).
	if !strings.Contains(out, "17.7%") {
		t.Errorf("output missing the detectable effect at 5 repeats:\n%s", out)
	}
}

// A task whose configurations were each run once has no noise estimate, and the
// report must say so rather than implying a comparison is meaningful.
func TestVarianceRefusesToEstimateFromUnrepeatedRuns(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedHistoryProfile(t, st, "p2", "hash-2", nil)
	seedHistoryRun(t, st, historyBaseRun("x1", "2026-01-01T00:00:01Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("x2", "2026-01-01T00:00:02Z", cliTaskID, "p2", "hash-2"))

	out, err := runVarianceCmd(t, d)
	if err != nil {
		t.Fatalf("variance: %v", err)
	}
	if !strings.Contains(out, "no repeats (2 configurations, 2 runs)") {
		t.Errorf("output does not explain the missing estimate:\n%s", out)
	}
	if !strings.Contains(out, "No task has been repeated") {
		t.Errorf("output does not state that nothing is distinguishable:\n%s", out)
	}
}

func TestVarianceJSONIsStable(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedVarianceRuns(t, st, cliTaskID, 90, 100, 110)

	out, err := runVarianceCmd(t, d, "--json", "--repeats", "10")
	if err != nil {
		t.Fatalf("variance --json: %v", err)
	}
	var got struct {
		TotalRuns      int `json:"total_runs"`
		SingleRunTasks int `json:"single_run_tasks"`
		RepeatedTasks  int `json:"repeated_tasks"`
		Repeats        int `json:"projected_repeats_per_arm"`
		Tasks          []struct {
			Task           string `json:"task"`
			RepeatedGroups int    `json:"repeated_groups"`
			Axes           []struct {
				Metric         string  `json:"metric"`
				RelativeSD     float64 `json:"relative_sd"`
				RepeatsFor10Pc int     `json:"repeats_for_10pc"`
			} `json:"axes"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if got.TotalRuns != 3 || got.RepeatedTasks != 1 || got.Repeats != 10 {
		t.Errorf("envelope = %+v", got)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].RepeatedGroups != 1 {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
	var found bool
	for _, av := range got.Tasks[0].Axes {
		if av.Metric != "tokens_total" {
			continue
		}
		found = true
		if av.RelativeSD < 0.099 || av.RelativeSD > 0.101 {
			t.Errorf("relative_sd = %v, want ~0.10", av.RelativeSD)
		}
		if av.RepeatsFor10Pc != 16 {
			t.Errorf("repeats_for_10pc = %d, want 16", av.RepeatsFor10Pc)
		}
	}
	if !found {
		t.Errorf("tokens_total axis missing: %s", out)
	}
}

func TestVarianceRejectsZeroRepeats(t *testing.T) {
	d, _ := historyTestDeps(t)
	if _, err := runVarianceCmd(t, d, "--repeats", "0"); err == nil {
		t.Fatal("variance --repeats 0 = nil error, want a usage error")
	}
}
