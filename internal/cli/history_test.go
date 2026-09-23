package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/store"
)

// Shared history/compare CLI fixtures. The store is a temp SQLite database
// rooted in the injected Deps paths; nothing here calls OpenCode or the
// network.

const (
	cliSuiteName    = "core"
	cliSuiteVersion = "1"
	cliSuiteHash    = "cli-suite-hash"
	cliTaskID       = "py-bugfix"
	cliTaskVersion  = "1"
	cliFixtureSHA   = "abc123"
)

// historyTestDeps returns injected deps plus an open, migrated temp store at
// the deps DB path.
func historyTestDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	d := cliTestDeps(t)
	if err := config.EnsureDirs(d.Paths); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(d.Paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d, st
}

func seedHistorySuiteTask(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertSuite(ctx, store.SuiteRow{
		ID: cliSuiteHash, Name: cliSuiteName, Version: cliSuiteVersion, Hash: cliSuiteHash,
		Source: "embedded", ManifestJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	if err := st.InsertTask(ctx, store.TaskRow{
		SuiteID: cliSuiteHash, TaskID: cliTaskID, Version: cliTaskVersion, Name: "Fix",
		TagsJSON: `[]`, TimeoutSeconds: 300, FixtureSHA: cliFixtureSHA, SpecJSON: `{}`,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func seedHistoryProfile(t *testing.T, st *store.Store, id, hash string, comps []store.ComponentRow) {
	t.Helper()
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: id, ProfileHash: hash, OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, comps); err != nil {
		t.Fatalf("insert profile %s: %v", id, err)
	}
}

// historyBaseRun builds a compatible non-dry run for the shared fixture.
func historyBaseRun(id, started, task, profileID, profileHash string) store.RunRow {
	exit := 0
	dur := int64(1000)
	return store.RunRow{
		ID: id, ProfileID: profileID, ProfileHash: profileHash,
		SuiteID: cliSuiteHash, SuiteName: cliSuiteName, SuiteVersion: cliSuiteVersion, SuiteHash: cliSuiteHash,
		TaskID: task, TaskVersion: cliTaskVersion, FixtureSHA: cliFixtureSHA,
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "p/m", Agent: "build",
		Status: "passed", DryRun: false, ExitCode: &exit,
		StartedAt: started, FinishedAt: started, DurationMS: &dur,
		ArtifactsDir: "/runs/" + id,
	}
}

func seedHistoryRun(t *testing.T, st *store.Store, r store.RunRow) {
	t.Helper()
	if err := st.InsertRun(context.Background(), r); err != nil {
		t.Fatalf("insert run %s: %v", r.ID, err)
	}
}

// runHistoryCmd executes the history command with injected deps.
func runHistoryCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newHistoryCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestHistoryListsNewestFirstAndFiltersByTask(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedHistoryRun(t, st, historyBaseRun("run-old", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("run-new", "2026-01-02T00:00:00Z", "other-task", "p1", "hash-1"))

	out, err := runHistoryCmd(t, d)
	if err != nil {
		t.Fatalf("history: %v\n%s", err, out)
	}
	if !strings.Contains(out, "RUN") || !strings.Contains(out, "TOOLS") {
		t.Fatalf("history header missing:\n%s", out)
	}
	newIdx := strings.Index(out, "run-new")
	oldIdx := strings.Index(out, "run-old")
	if newIdx < 0 || oldIdx < 0 || newIdx > oldIdx {
		t.Fatalf("history order wrong (newest first):\n%s", out)
	}

	filtered, err := runHistoryCmd(t, d, "--task", cliTaskID)
	if err != nil {
		t.Fatalf("history --task: %v\n%s", err, filtered)
	}
	if !strings.Contains(filtered, "run-old") || strings.Contains(filtered, "run-new") {
		t.Fatalf("history --task filter wrong:\n%s", filtered)
	}
}

func TestHistoryLimit(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedHistoryRun(t, st, historyBaseRun("run-1", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("run-2", "2026-01-02T00:00:00Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("run-3", "2026-01-03T00:00:00Z", cliTaskID, "p1", "hash-1"))

	out, err := runHistoryCmd(t, d, "--limit", "1")
	if err != nil {
		t.Fatalf("history --limit 1: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run-3") || strings.Contains(out, "run-2") || strings.Contains(out, "run-1") {
		t.Fatalf("history --limit 1 wrong:\n%s", out)
	}
}

func TestHistoryLimitBelowOneIsUsageError(t *testing.T) {
	d, _ := historyTestDeps(t)
	for _, limit := range []string{"0", "-1"} {
		out, err := runHistoryCmd(t, d, "--limit", limit)
		if err == nil {
			t.Fatalf("history --limit %s: expected an error\n%s", limit, out)
		}
		if !isUsageError(err) {
			t.Fatalf("history --limit %s error = %v, want UsageError", limit, err)
		}
	}
}

func TestHistoryEmptyStore(t *testing.T) {
	d, _ := historyTestDeps(t)
	out, err := runHistoryCmd(t, d)
	if err != nil {
		t.Fatalf("history empty: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no runs") {
		t.Fatalf("history empty output = %q, want no runs", out)
	}
}

func TestHistoryJSONEmptyArray(t *testing.T) {
	d, _ := historyTestDeps(t)
	out, err := runHistoryCmd(t, d, "--json")
	if err != nil {
		t.Fatalf("history --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"runs": []`) {
		t.Fatalf("history --json empty runs must be []:\n%s", out)
	}
}

func TestHistoryJSONShape(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	seedHistoryRun(t, st, historyBaseRun("run-1", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1"))
	if err := st.InsertRunMetrics(context.Background(), "run-1", map[string]float64{
		"tokens_total": 15, "tool_calls_total": 2,
	}); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}

	out, err := runHistoryCmd(t, d, "--json")
	if err != nil {
		t.Fatalf("history --json: %v\n%s", err, out)
	}
	type runJSON struct {
		RunID          string `json:"run_id"`
		StartedAt      string `json:"started_at"`
		TaskID         string `json:"task_id"`
		Status         string `json:"status"`
		ProfileHash    string `json:"profile_hash"`
		DurationMS     int64  `json:"duration_ms"`
		TokensTotal    int64  `json:"tokens_total"`
		ToolCallsTotal int64  `json:"tool_calls_total"`
	}
	var doc struct {
		Runs []runJSON `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode history JSON: %v\n%s", err, out)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("runs = %d, want 1\n%s", len(doc.Runs), out)
	}
	got := doc.Runs[0]
	if got.RunID != "run-1" || got.StartedAt != "2026-01-01T00:00:00Z" || got.TaskID != cliTaskID {
		t.Fatalf("run identity = %+v", got)
	}
	if got.Status != "passed" || got.ProfileHash != "hash-1" {
		t.Fatalf("run status/profile = %+v", got)
	}
	if got.DurationMS != 1000 || got.TokensTotal != 15 || got.ToolCallsTotal != 2 {
		t.Fatalf("run numbers = %+v", got)
	}
}
