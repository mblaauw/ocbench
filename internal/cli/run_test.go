package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/store"
)

// The fake OpenCode binary for `run` tests is this test binary re-invoked with
// -test.run=TestRunHelperProcess. It only acts when the guard variable is
// forwarded into the sandboxed child environment by Config.Sandbox.PassEnv.
const (
	runHelperGuard = "GO_WANT_HELPER_PROCESS"
	runHelperMode  = "RUN_HELPER_MODE"

	runHelperSession = "ses_cli"

	runHelperConfig = `{"default_agent":"build","agent":{"build":{}},"mcp":{"gitlab":{"enabled":true}}}`
	runHelperExport = `{"info":{"id":"ses_cli","tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01},"messages":[]}`

	// A minimal 4-line stream: step_start, one completed tool_use, a
	// step_finish carrying tokens, and a text part.
	runHelperEvents = `{"type":"step_start","timestamp":1,"sessionID":"ses_cli","part":{"type":"step-start"}}
{"type":"tool_use","timestamp":2,"sessionID":"ses_cli","part":{"type":"tool","tool":"read","callID":"c1","state":{"status":"completed"}}}
{"type":"step_finish","timestamp":3,"sessionID":"ses_cli","part":{"type":"step-finish","reason":"stop","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01}}
{"type":"text","timestamp":4,"sessionID":"ses_cli","part":{"type":"text","text":"done"}}
`
)

// TestRunHelperProcess impersonates opencode for the CLI run tests. It is only
// active in the re-invoked child, never in the parent suite.
func TestRunHelperProcess(t *testing.T) {
	if os.Getenv(runHelperGuard) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	write := func(s string) {
		fmt.Fprint(os.Stdout, s)
		os.Exit(0)
	}
	switch {
	case len(args) > 0 && args[0] == "--version":
		write("1.18.32\n")
	case len(args) > 1 && args[0] == "debug" && args[1] == "config":
		write(runHelperConfig)
	case len(args) > 1 && args[0] == "debug" && args[1] == "skill":
		write("[]")
	case len(args) > 2 && args[0] == "debug" && args[1] == "agent":
		write(fmt.Sprintf(`{"name":%q,"mode":"primary","native":false,"model":"p/m"}`, args[2]))
	case len(args) > 0 && args[0] == "export":
		write(runHelperExport)
	case len(args) > 0 && args[0] == "run":
		if os.Getenv(runHelperMode) == "fail" {
			fmt.Fprintln(os.Stdout, `{"type":"step_start","timestamp":1,"sessionID":"ses_cli","part":{"type":"step-start"}}`)
			os.Exit(3)
		}
		fmt.Fprint(os.Stdout, runHelperEvents)
		os.Exit(0)
	}
	os.Exit(42)
}

// runTestAdapter serves discovery through the helper-backed Real adapter and
// counts Start calls so tests can prove --dry-run never opens a session.
type runTestAdapter struct {
	*opencode.Real

	mu     sync.Mutex
	starts int
}

func (a *runTestAdapter) Start(ctx context.Context, req opencode.RunRequest) (*opencode.Session, error) {
	a.mu.Lock()
	a.starts++
	a.mu.Unlock()
	return a.Real.Start(ctx, req)
}

func (a *runTestAdapter) startCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts
}

// newRunTestDeps builds deps whose adapter is the guarded test binary. The
// guard and mode variables are forwarded through the sandbox allowlist.
func newRunTestDeps(t *testing.T) (Deps, *runTestAdapter) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(runHelperGuard, "1")
	base := t.TempDir()
	paths := config.Paths{
		Home:       filepath.Join(base, "data"),
		ConfigFile: filepath.Join(base, "config", "ocbench", "config.yaml"),
		DB:         filepath.Join(base, "data", "ocbench.db"),
		Profiles:   filepath.Join(base, "data", "profiles"),
		Runs:       filepath.Join(base, "data", "runs"),
		Suites:     filepath.Join(base, "data", "suites"),
		Cache:      filepath.Join(base, "cache", "ocbench"),
	}
	cfg := config.DefaultsConfig()
	cfg.OpenCodeBin = filepath.Join(base, "opencode-unused")
	cfg.Sandbox.PassEnv = []string{runHelperGuard, runHelperMode}
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	adapter := &runTestAdapter{Real: opencode.NewReal(opencode.Options{
		Bin:        bin,
		Timeout:    20 * time.Second,
		Env:        os.Environ(),
		TestPrefix: []string{"-test.run=TestRunHelperProcess", "--"},
	})}
	return Deps{Adapter: adapter, Paths: paths, Config: cfg}, adapter
}

type runTaskSpec struct {
	id      string
	failing bool
}

// writeRunSuite writes a minimal on-disk suite: one task per spec, each with a
// fixture file and a single command validator.
func writeRunSuite(t *testing.T, tasks ...runTaskSpec) string {
	t.Helper()
	dir := t.TempDir()
	writeRunFile(t, filepath.Join(dir, "suite.yaml"), "name: mini\nversion: \"1\"\n")
	for _, ts := range tasks {
		base := filepath.Join(dir, "tasks", ts.id)
		exit := "exit 0"
		if ts.failing {
			exit = "exit 1"
		}
		task := fmt.Sprintf("id: %s\nversion: \"1\"\nname: %s\nvalidators:\n  - kind: command\n    name: check\n    command: [\"sh\", \"-c\", \"%s\"]\n",
			ts.id, ts.id, exit)
		writeRunFile(t, filepath.Join(base, "task.yaml"), task)
		writeRunFile(t, filepath.Join(base, "prompt.md"), "do the thing\n")
		writeRunFile(t, filepath.Join(base, "fixture", "hello.txt"), "hi\n")
	}
	return dir
}

func writeRunFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runRunCmd executes the run command with injected deps and returns its output
// and error.
func runRunCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newRunCmd(d)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// openRunStore opens the deps' store for assertions after the command closed
// its own handle.
func openRunStore(t *testing.T, d Deps) *store.Store {
	t.Helper()
	st, err := store.Open(d.Paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func runMetric(t *testing.T, st *store.Store, runID, name string) float64 {
	t.Helper()
	var v float64
	if err := st.DB().QueryRow(
		`SELECT value_num FROM run_metrics WHERE run_id = ? AND name = ?`, runID, name).Scan(&v); err != nil {
		t.Fatalf("metric %s for %s: %v", name, runID, err)
	}
	return v
}

func TestRunCommandSinglePassPersistsMetrics(t *testing.T) {
	d, adapter := newRunTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	out, err := runRunCmd(t, d, "mini", "t1", "--suite-dir", suiteDir)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("output missing PASS:\n%s", out)
	}
	if !strings.Contains(out, "1/1 successful") {
		t.Fatalf("output missing summary:\n%s", out)
	}
	if got := adapter.startCount(); got != 1 {
		t.Fatalf("Start calls = %d, want 1", got)
	}

	st := openRunStore(t, d)
	runs, err := st.ListRuns(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Status != "passed" || runs[0].RepeatIndex != 0 {
		t.Fatalf("run row = %+v", runs[0])
	}
	if runs[0].ExperimentID == "" {
		t.Fatal("run row has no experiment id")
	}
	if got := runMetric(t, st, runs[0].ID, "tokens_total"); got != 15 {
		t.Fatalf("tokens_total = %v, want 15", got)
	}
	if got := runMetric(t, st, runs[0].ID, "tool_calls_total"); got != 1 {
		t.Fatalf("tool_calls_total = %v, want 1", got)
	}
	var experiments int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM experiments`).Scan(&experiments); err != nil {
		t.Fatal(err)
	}
	if experiments != 1 {
		t.Fatalf("experiments = %d, want 1", experiments)
	}
}

func TestRunCommandFailingValidatorExitCodes(t *testing.T) {
	t.Run("exit 0 without flag", func(t *testing.T) {
		d, adapter := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t-ok"}, runTaskSpec{id: "t-bad", failing: true})

		out, err := runRunCmd(t, d, "mini", "--suite-dir", suiteDir)
		if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if !strings.Contains(out, "FAIL") {
			t.Fatalf("output missing FAIL:\n%s", out)
		}
		if !strings.Contains(out, "1/2 successful") {
			t.Fatalf("output missing 1/2 summary:\n%s", out)
		}
		if got := exitCode(err); got != 0 {
			t.Fatalf("exit code = %d, want 0", got)
		}
		if got := adapter.startCount(); got != 2 {
			t.Fatalf("Start calls = %d, want 2", got)
		}
	})

	t.Run("exit 3 with exit-on-task-failure", func(t *testing.T) {
		d, _ := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t-ok"}, runTaskSpec{id: "t-bad", failing: true})

		out, err := runRunCmd(t, d, "mini", "--suite-dir", suiteDir, "--exit-on-task-failure")
		if !errors.Is(err, ErrTaskFailure) {
			t.Fatalf("error = %v, want ErrTaskFailure\n%s", err, out)
		}
		if got := exitCode(err); got != 3 {
			t.Fatalf("exit code = %d, want 3", got)
		}
	})
}

func TestRunCommandRepeatAggregates(t *testing.T) {
	d, adapter := newRunTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	out, err := runRunCmd(t, d, "--repeat", "2", "mini", "--suite-dir", suiteDir)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got := adapter.startCount(); got != 2 {
		t.Fatalf("Start calls = %d, want 2", got)
	}
	for _, want := range []string{"2/2 successful", "task t1", "median"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	st := openRunStore(t, d)
	runs, err := st.ListRuns(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	indexes := map[int]bool{}
	for _, r := range runs {
		indexes[r.RepeatIndex] = true
		if r.TaskID != "t1" {
			t.Fatalf("task id = %q, want t1", r.TaskID)
		}
	}
	if !indexes[0] || !indexes[1] {
		t.Fatalf("repeat indexes = %v, want 0 and 1", indexes)
	}
}

func TestRunCommandDryRun(t *testing.T) {
	d, adapter := newRunTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	out, err := runRunCmd(t, d, "--dry-run", "mini", "--suite-dir", suiteDir)
	if err != nil {
		t.Fatalf("run --dry-run: %v\n%s", err, out)
	}
	if got := adapter.startCount(); got != 0 {
		t.Fatalf("Start calls = %d, want 0 for dry-run", got)
	}

	st := openRunStore(t, d)
	runs, err := st.ListRuns(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "dry_run" {
		t.Fatalf("runs = %+v, want one dry_run row", runs)
	}
	if _, err := os.Stat(filepath.Join(runs[0].ArtifactsDir, "plan.json")); err != nil {
		t.Fatalf("plan.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runs[0].ArtifactsDir, "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("events.jsonl present for dry-run (err = %v)", err)
	}
}

func TestRunCommandInheritEnvironmentProfile(t *testing.T) {
	t.Run("default sandbox", func(t *testing.T) {
		d, _ := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})
		if _, err := runRunCmd(t, d, "mini", "--suite-dir", suiteDir); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := persistedSandbox(t, d); got != "default" {
			t.Fatalf("sandbox = %q, want default", got)
		}
	})

	t.Run("inherit environment", func(t *testing.T) {
		d, _ := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})
		if _, err := runRunCmd(t, d, "--inherit-environment", "mini", "--suite-dir", suiteDir); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := persistedSandbox(t, d); got != "inherit" {
			t.Fatalf("sandbox = %q, want inherit", got)
		}
	})
}

// persistedSandbox reads environment.sandbox from the single persisted profile.
func persistedSandbox(t *testing.T, d Deps) string {
	t.Helper()
	st := openRunStore(t, d)
	var canonical string
	if err := st.DB().QueryRow(`SELECT canonical_json FROM profiles LIMIT 1`).Scan(&canonical); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(canonical), &snapshot); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	components, ok := snapshot["components"].(map[string]any)
	if !ok {
		t.Fatalf("profile has no components: %s", canonical)
	}
	environment, ok := components["environment"].(map[string]any)
	if !ok {
		t.Fatalf("profile has no environment component: %s", canonical)
	}
	sandbox, _ := environment["sandbox"].(string)
	return sandbox
}

func TestRunCommandUnknownTaskIsUsageError(t *testing.T) {
	d, _ := newRunTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	out, err := runRunCmd(t, d, "mini", "nope", "--suite-dir", suiteDir)
	if !isUsageError(err) {
		t.Fatalf("error = %v, want UsageError\n%s", err, out)
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRunCommandRepeatZeroIsUsageError(t *testing.T) {
	d, _ := newRunTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	_, err := runRunCmd(t, d, "--repeat", "0", "mini", "--suite-dir", suiteDir)
	if !isUsageError(err) {
		t.Fatalf("error = %v, want UsageError", err)
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

type runJSONRun struct {
	RunID            string `json:"run_id"`
	TaskID           string `json:"task_id"`
	RepeatIndex      int    `json:"repeat_index"`
	Status           string `json:"status"`
	DurationMS       int64  `json:"duration_ms"`
	TokensTotal      int64  `json:"tokens_total"`
	ToolCallsTotal   int64  `json:"tool_calls_total"`
	ValidatorsFailed int    `json:"validators_failed"`
}

type runJSONAggregate struct {
	TaskID           string  `json:"task_id"`
	Executions       int     `json:"executions"`
	Successes        int     `json:"successes"`
	SuccessRate      float64 `json:"success_rate"`
	MedianTokens     float64 `json:"median_tokens"`
	MinTokens        int64   `json:"min_tokens"`
	MaxTokens        int64   `json:"max_tokens"`
	MedianTools      float64 `json:"median_tools"`
	MinTools         int64   `json:"min_tools"`
	MaxTools         int64   `json:"max_tools"`
	MedianDurationMS float64 `json:"median_duration_ms"`
}

type runJSONDoc struct {
	ProfileHash  string             `json:"profile_hash"`
	ExperimentID string             `json:"experiment_id"`
	Runs         []runJSONRun       `json:"runs"`
	Aggregates   []runJSONAggregate `json:"aggregates"`
}

func TestRunCommandJSONOutput(t *testing.T) {
	t.Run("single run has empty aggregates", func(t *testing.T) {
		d, _ := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})
		out, err := runRunCmd(t, d, "--json", "mini", "--suite-dir", suiteDir)
		if err != nil {
			t.Fatalf("run --json: %v\n%s", err, out)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("decode json: %v\n%s", err, out)
		}
		for _, key := range []string{"profile_hash", "experiment_id", "runs", "aggregates"} {
			if _, ok := raw[key]; !ok {
				t.Fatalf("json missing key %q: %s", key, out)
			}
		}
		if got := strings.TrimSpace(string(raw["aggregates"])); got != "[]" {
			t.Fatalf("aggregates = %s, want []", got)
		}

		var doc runJSONDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("decode doc: %v", err)
		}
		if doc.ProfileHash == "" || doc.ExperimentID == "" {
			t.Fatalf("doc = %+v, want profile hash and experiment id", doc)
		}
		if len(doc.Runs) != 1 {
			t.Fatalf("runs = %d, want 1", len(doc.Runs))
		}
		r := doc.Runs[0]
		if r.RunID == "" || r.TaskID != "t1" || r.Status != "passed" || r.RepeatIndex != 0 {
			t.Fatalf("run = %+v", r)
		}
		if r.TokensTotal != 15 || r.ToolCallsTotal != 1 || r.ValidatorsFailed != 0 {
			t.Fatalf("run metrics = %+v", r)
		}
	})

	t.Run("repeat produces aggregates", func(t *testing.T) {
		d, _ := newRunTestDeps(t)
		suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})
		out, err := runRunCmd(t, d, "--json", "--repeat", "2", "mini", "--suite-dir", suiteDir)
		if err != nil {
			t.Fatalf("run --json --repeat 2: %v\n%s", err, out)
		}
		var doc runJSONDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("decode doc: %v\n%s", err, out)
		}
		if len(doc.Runs) != 2 {
			t.Fatalf("runs = %d, want 2", len(doc.Runs))
		}
		if len(doc.Aggregates) != 1 {
			t.Fatalf("aggregates = %d, want 1", len(doc.Aggregates))
		}
		a := doc.Aggregates[0]
		if a.TaskID != "t1" || a.Executions != 2 || a.Successes != 2 || a.SuccessRate != 1 {
			t.Fatalf("aggregate = %+v", a)
		}
		if a.MedianTokens != 15 || a.MinTokens != 15 || a.MaxTokens != 15 {
			t.Fatalf("token aggregate = %+v", a)
		}
		if a.MedianTools != 1 || a.MinTools != 1 || a.MaxTools != 1 {
			t.Fatalf("tool aggregate = %+v", a)
		}
		if a.MedianDurationMS < 0 {
			t.Fatalf("duration aggregate = %+v", a)
		}
	})
}
