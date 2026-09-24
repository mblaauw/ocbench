package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
)

// runTraceCmd executes the trace command with injected deps.
func runTraceCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newTraceCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// traceTestDeps seeds the suite/task/profile a run row references.
func traceTestDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", nil)
	return d, st
}

// traceFixtureRun seeds one run whose artifacts directory is a fresh temp dir
// populated with the shared event fixture. withChild also copies the captured
// child session under sessions/.
func traceFixtureRun(t *testing.T, st *store.Store, id string, withChild bool) string {
	t.Helper()
	dir := t.TempDir()
	events, err := os.ReadFile(filepath.Join("..", "session", "testdata", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), events, 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}
	if withChild {
		child, err := os.ReadFile(filepath.Join("..", "session", "testdata", "child-session.json"))
		if err != nil {
			t.Fatalf("read child fixture: %v", err)
		}
		sessionsDir := filepath.Join(dir, "sessions")
		if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
			t.Fatalf("mkdir sessions: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sessionsDir, "ses_f2bc3c5a4ffe0WMXC38zE4mJNF.json"), child, 0o644); err != nil {
			t.Fatalf("write child: %v", err)
		}
	}
	r := historyBaseRun(id, "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1")
	r.ArtifactsDir = dir
	seedHistoryRun(t, st, r)
	return dir
}

func TestTraceJSONShape(t *testing.T) {
	d, st := traceTestDeps(t)
	traceFixtureRun(t, st, "run-trace", false)

	out, err := runTraceCmd(t, d, "run-trace", "--json")
	if err != nil {
		t.Fatalf("trace --json: %v\n%s", err, out)
	}

	type tokensJSON struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		Reasoning  int64 `json:"reasoning"`
		CacheRead  int64 `json:"cache_read"`
		CacheWrite int64 `json:"cache_write"`
		Total      int64 `json:"total"`
	}
	type toolCallJSON struct {
		Name       string `json:"name"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		DurationMS int64  `json:"duration_ms"`
	}
	type subagentJSON struct {
		SessionID string         `json:"session_id"`
		Agent     string         `json:"agent"`
		Tokens    tokensJSON     `json:"tokens"`
		Cost      float64        `json:"cost"`
		Tools     []toolCallJSON `json:"tools"`
	}
	type toolJSON struct {
		Tool       string        `json:"tool"`
		Title      string        `json:"title"`
		Status     string        `json:"status"`
		DurationMS int64         `json:"duration_ms"`
		Child      *subagentJSON `json:"child"`
	}
	type stepJSON struct {
		Index       int        `json:"index"`
		Tokens      tokensJSON `json:"tokens"`
		Cost        float64    `json:"cost"`
		DurationMS  int64      `json:"duration_ms"`
		Retries     int        `json:"retries"`
		Compactions int        `json:"compactions"`
		Tools       []toolJSON `json:"tools"`
	}
	var doc struct {
		RunID     string         `json:"run_id"`
		TaskID    string         `json:"task_id"`
		Steps     []stepJSON     `json:"steps"`
		Subagents []subagentJSON `json:"subagents"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode trace JSON: %v\n%s", err, out)
	}
	if doc.RunID != "run-trace" || doc.TaskID != cliTaskID {
		t.Fatalf("identity = %q/%q", doc.RunID, doc.TaskID)
	}
	if len(doc.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(doc.Steps))
	}
	if doc.Steps[0].Tokens.Total != 12491 {
		t.Fatalf("step 0 tokens = %+v", doc.Steps[0].Tokens)
	}
	if len(doc.Steps[0].Tools) != 1 {
		t.Fatalf("step 0 tools = %+v", doc.Steps[0].Tools)
	}
	if doc.Steps[0].Tools[0].Child == nil {
		t.Fatal("task tool child missing")
	}
	if doc.Steps[0].Tools[0].Child.SessionID != "ses_f2bc3c5a4ffe0WMXC38zE4mJNF" {
		t.Fatalf("child session = %q", doc.Steps[0].Tools[0].Child.SessionID)
	}
	if doc.Steps[0].Tools[0].Child.Agent != "" {
		t.Fatalf("uncaptured child agent = %q, want empty", doc.Steps[0].Tools[0].Child.Agent)
	}
	if doc.Subagents == nil || len(doc.Subagents) != 0 {
		t.Fatalf("subagents = %+v, want empty slice", doc.Subagents)
	}
	if !strings.Contains(out, `"subagents": []`) {
		t.Fatalf("JSON missing empty subagents array:\n%s", out)
	}
}

func TestTraceHumanNestsSubagentUnderTask(t *testing.T) {
	d, st := traceTestDeps(t)
	traceFixtureRun(t, st, "run-trace", true)

	out, err := runTraceCmd(t, d, "run-trace")
	if err != nil {
		t.Fatalf("trace: %v\n%s", err, out)
	}
	for _, want := range []string{"step 0", "12491", "List files in directory", "explore", "5653"} {
		if !strings.Contains(out, want) {
			t.Fatalf("trace output missing %q:\n%s", want, out)
		}
	}
	taskIdx := strings.Index(out, "List files in directory")
	childIdx := strings.Index(out, "explore")
	if taskIdx < 0 || childIdx < 0 || childIdx < taskIdx {
		t.Fatalf("child not nested under task line:\n%s", out)
	}
	// The child line must be indented further than the task line.
	taskLine := out[strings.LastIndex(out[:taskIdx], "\n")+1:]
	childLine := out[strings.LastIndex(out[:childIdx], "\n")+1:]
	if !strings.HasPrefix(childLine, "    ") || strings.HasPrefix(taskLine, "    ") {
		t.Fatalf("unexpected indentation:\ntask=%q\nchild=%q", taskLine, childLine)
	}
}

func TestTraceUnknownRunIsUsageError(t *testing.T) {
	d, _ := traceTestDeps(t)
	out, err := runTraceCmd(t, d, "no-such-run")
	if err == nil {
		t.Fatalf("trace unknown run: expected error\n%s", out)
	}
	if !isUsageError(err) {
		t.Fatalf("trace unknown run error = %v, want UsageError", err)
	}
}

func TestTraceMissingEventsIsInfrastructureError(t *testing.T) {
	d, st := traceTestDeps(t)
	dir := t.TempDir()
	r := historyBaseRun("run-no-events", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1")
	r.ArtifactsDir = dir
	seedHistoryRun(t, st, r)

	out, err := runTraceCmd(t, d, "run-no-events")
	if err == nil {
		t.Fatalf("trace missing events: expected error\n%s", out)
	}
	if isUsageError(err) {
		t.Fatalf("trace missing events error = %v, want infrastructure error", err)
	}
	if !strings.Contains(err.Error(), "events.jsonl") {
		t.Fatalf("trace missing events error = %v, want path", err)
	}
}
