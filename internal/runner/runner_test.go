package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
)

// The fake OpenCode binary is this test binary re-invoked with
// -test.run=TestRunnerHelperProcess. It is only active when
// OCBENCH_FAKE_OPENCODE=1, which the parent forwards to the child through the
// sandbox allowlist (EnvPolicy.PassEnv). No real opencode or network is used.
const (
	fakeHelperGuard = "OCBENCH_FAKE_OPENCODE"
	fakeModeVar     = "OCBENCH_FAKE_MODE"
	// fakePIDFileVar, when set, makes the slow helper record its PID so a test
	// can prove the process was reaped after Run returned.
	fakePIDFileVar = "OCBENCH_FAKE_PIDFILE"

	fakeExportJSON = `{"info":{"id":"ses_test","tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01},"messages":[]}`

	fakeStepStart = `{"type":"step_start","timestamp":1,"sessionID":"ses_test","part":{"type":"step-start"}}`

	fakeEvents = `{"type":"step_start","timestamp":1,"sessionID":"ses_test","part":{"type":"step-start"}}
{"type":"tool_use","timestamp":2,"sessionID":"ses_test","part":{"type":"tool","tool":"read","callID":"c1","state":{"status":"completed"}}}
{"type":"tool_use","timestamp":3,"sessionID":"ses_test","part":{"type":"tool","tool":"gitlab_search","callID":"c2","state":{"status":"completed"}}}
{"type":"text","timestamp":4,"sessionID":"ses_test","part":{"type":"text","text":"the retry logic is off-by-one"}}
{"type":"step_finish","timestamp":5,"sessionID":"ses_test","part":{"type":"step-finish","reason":"stop","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01}}
`
)

// TestRunnerHelperProcess impersonates opencode for the runner tests. It only
// runs its body in the re-invoked child, never in the parent test suite.
func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv(fakeHelperGuard) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode := os.Getenv(fakeModeVar)
	if len(args) > 0 && args[0] == "export" {
		if mode == "export-fail" {
			fmt.Fprintln(os.Stderr, "export failed: simulated")
			os.Exit(4)
		}
		fmt.Fprint(os.Stdout, fakeExportJSON)
		os.Exit(0)
	}
	if len(args) > 0 && args[0] == "run" {
		switch mode {
		case "slow":
			if pf := os.Getenv(fakePIDFileVar); pf != "" {
				_ = os.WriteFile(pf, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
			}
			fmt.Fprintln(os.Stdout, fakeStepStart)
			time.Sleep(30 * time.Second)
			os.Exit(0)
		case "near":
			// Emit events and finish before the task deadline so the watchdog
			// must not misclassify the run as timed out. The margin is wide
			// because a race-instrumented child binary can take over a second to
			// start.
			fmt.Fprint(os.Stdout, fakeEvents)
			_ = os.WriteFile("src/main.go", []byte("package main // changed by agent\n"), 0o644)
			_ = os.WriteFile("untracked.txt", []byte("hello untracked\n"), 0o644)
			time.Sleep(500 * time.Millisecond)
			os.Exit(0)
		}
		// Emit the canned stream and mutate the worktree (cwd is the worktree).
		fmt.Fprint(os.Stdout, fakeEvents)
		_ = os.WriteFile("src/main.go", []byte("package main // changed by agent\n"), 0o644)
		_ = os.WriteFile("untracked.txt", []byte("hello untracked\n"), 0o644)
		if mode == "symlink" {
			// A link pointing outside the worktree: it must be recorded in
			// changed.json but its target must not be copied into untracked/.
			_ = os.WriteFile(filepath.Join("..", "..", "outside-secret.txt"), []byte("secret\n"), 0o644)
			_ = os.Symlink(filepath.Join("..", "..", "outside-secret.txt"), "escape-link")
		}
		if mode == "exit3" {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(42)
}

// scriptedAdapter is the full Adapter surface with a scriptable Start/Export.
// It embeds the interface so the promoted observation methods delegate to the
// configured Real adapter (which points at the helper process).
type scriptedAdapter struct {
	opencode.Adapter
	startErr  error
	exportErr error

	mu     sync.Mutex
	starts []opencode.RunRequest
}

func (a *scriptedAdapter) Start(ctx context.Context, req opencode.RunRequest) (*opencode.Session, error) {
	a.mu.Lock()
	a.starts = append(a.starts, req)
	a.mu.Unlock()
	if a.startErr != nil {
		return nil, a.startErr
	}
	return a.Adapter.Start(ctx, req)
}

func (a *scriptedAdapter) Export(ctx context.Context, id string) ([]byte, error) {
	if a.exportErr != nil {
		return nil, a.exportErr
	}
	return a.Adapter.Export(ctx, id)
}

func (a *scriptedAdapter) startCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.starts)
}

func (a *scriptedAdapter) firstStart(t *testing.T) opencode.RunRequest {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.starts) == 0 {
		t.Fatal("adapter Start was never called")
	}
	return a.starts[0]
}

// newScriptedAdapter builds the helper-backed adapter. It must be called after
// t.Setenv so os.Environ carries the fake mode for the Real adapter's Export.
func newScriptedAdapter(t *testing.T) *scriptedAdapter {
	t.Helper()
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return &scriptedAdapter{Adapter: opencode.NewReal(opencode.Options{
		Bin:        bin,
		Timeout:    10 * time.Second,
		Env:        os.Environ(),
		TestPrefix: []string{"-test.run=TestRunnerHelperProcess", "--"},
	})}
}

type runnerFixture struct {
	st      *store.Store
	paths   config.Paths
	suite   *suite.Suite
	task    *suite.Task
	profile *profile.Profile
}

func runnerFixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"README.md":   {Data: []byte("# fixture\n")},
		"src/main.go": {Data: []byte("package main\n")},
	}
}

func setupRunner(t *testing.T) *runnerFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := &profile.Profile{ID: "p1", Hash: "hash-1", OpenCodeVersion: "1.18.32"}
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: "p1", ProfileHash: "hash-1", OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, nil); err != nil {
		t.Fatal(err)
	}

	task := &suite.Task{
		ID: "py-bugfix", Version: "1", Name: "Fix it",
		TimeoutSeconds: 30,
		Prompt:         "fix the bug",
		Fixture:        runnerFixtureFS(),
		FixtureHash:    testFixtureHash,
		AllowChanges:   []string{"src/**", "untracked.txt"},
	}
	s := &suite.Suite{
		Name: "core", Version: "1", Hash: "suite-hash-1",
		FS: fstest.MapFS{
			"suite.yaml":                {Data: []byte("name: core\nversion: \"1\"\n")},
			"tasks/py-bugfix/task.yaml": {Data: []byte("id: py-bugfix\nversion: \"1\"\nname: Fix it\n")},
			"tasks/py-bugfix/prompt.md": {Data: []byte("fix the bug\n")},
		},
	}
	s.Tasks = []*suite.Task{task}

	return &runnerFixture{
		st:      st,
		paths:   config.Paths{Cache: t.TempDir(), Runs: t.TempDir()},
		suite:   s,
		task:    task,
		profile: p,
	}
}

func runnerRequest(f *runnerFixture) Request {
	return Request{
		Suite:   f.suite,
		Task:    f.task,
		Profile: f.profile,
		Paths:   f.paths,
		EnvPolicy: EnvPolicy{
			PassEnv: []string{fakeHelperGuard, fakeModeVar},
		},
		Agent:    "build",
		Model:    "p/m",
		Variant:  "high",
		Auto:     true,
		MCPTools: []string{"gitlab"},
	}
}

// useMode sets the fake mode for both the parent process (so BuildEnv forwards
// it) and the Real adapter's export path.
func useMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(fakeHelperGuard, "1")
	t.Setenv(fakeModeVar, mode)
}

func artifact(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("artifact %s missing: %v", name, err)
	}
	return p
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestRunHappyPath(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 0"}},
		{Kind: "answer", Name: "root cause", Patterns: []string{"off-by-one"}, Mode: "all"},
	}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (error=%q)", res.Status, res.Error)
	}
	if res.RunID == "" || res.TaskID != "py-bugfix" {
		t.Fatalf("ids = %q/%q", res.RunID, res.TaskID)
	}
	if res.SessionID != "ses_test" {
		t.Fatalf("session = %q, want ses_test", res.SessionID)
	}
	if res.DurationMS < 0 {
		t.Fatalf("duration = %d", res.DurationMS)
	}

	for _, name := range []string{
		"events.jsonl", "stderr.txt", "session.json",
		"diff.patch", "changed.json", "result.json",
		"suite.yaml", "task.yaml", "prompt.md",
		"untracked/untracked.txt",
		"validation/1-unit_tests.log", "validation/2-root_cause.log",
	} {
		artifact(t, res.ArtifactsDir, name)
	}

	wantMetrics := map[string]float64{
		"steps": 1, "tool_calls_total": 2, "mcp_calls": 1, "mcp_calls_gitlab": 1,
		"tokens_input": 10, "tokens_output": 5, "tokens_total": 15, "cost": 0.01,
		// Runner-derived spec §9 metrics.
		"files_changed": 2, "files_created": 1, "files_deleted": 0,
		"diff_lines_added": 1, "diff_lines_removed": 1,
		"files_unexpected": 0, "validator_failures": 0,
		"success": 1, "first_shot_success": 1,
	}
	for k, want := range wantMetrics {
		if got := res.Metrics[k]; got != want {
			t.Errorf("metric %s = %v, want %v", k, got, want)
		}
	}
	if res.Metrics["duration_ms"] < 0 {
		t.Errorf("duration_ms = %v, want >= 0", res.Metrics["duration_ms"])
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", res.ExitCode)
	}

	if len(res.Validations) != 2 {
		t.Fatalf("validations = %d, want 2", len(res.Validations))
	}
	for i, v := range res.Validations {
		if v.Status != "passed" {
			t.Errorf("validation %d (%s) = %q, want passed", i, v.Name, v.Status)
		}
	}
	if res.Validations[0].Seq != 1 || res.Validations[1].Seq != 2 {
		t.Fatalf("seq = %d/%d, want 1/2", res.Validations[0].Seq, res.Validations[1].Seq)
	}

	if !contains(res.ChangedFiles, "src/main.go") || !contains(res.ChangedFiles, "untracked.txt") {
		t.Fatalf("changed = %v", res.ChangedFiles)
	}
	if len(res.UnexpectedFiles) != 0 {
		t.Fatalf("unexpected = %v, want none", res.UnexpectedFiles)
	}

	// Events file is newline-terminated JSONL, one line per event.
	events, err := os.ReadFile(filepath.Join(res.ArtifactsDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(events), "\n") || strings.Count(string(events), "\n") != 5 {
		t.Fatalf("events.jsonl = %q", events)
	}

	// Persisted run row.
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if row.Status != "passed" || row.SessionID != "ses_test" || row.DryRun {
		t.Fatalf("run row = %+v", row)
	}
	if row.ExitCode == nil || *row.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", row.ExitCode)
	}
	if row.ProfileID != "p1" || row.SuiteID != "suite-hash-1" || row.TaskID != "py-bugfix" {
		t.Fatalf("run refs = %+v", row)
	}
	if row.ArtifactsDir != res.ArtifactsDir {
		t.Fatalf("artifacts dir = %q, want %q", row.ArtifactsDir, res.ArtifactsDir)
	}

	// Runner-derived metrics are persisted, not just returned.
	for name, want := range map[string]float64{"files_changed": 2, "files_created": 1, "success": 1, "validator_failures": 0} {
		var got float64
		if err := f.st.DB().QueryRowContext(context.Background(),
			`SELECT value_num FROM run_metrics WHERE run_id = ? AND name = ?`, res.RunID, name).Scan(&got); err != nil {
			t.Fatalf("run_metrics %s: %v", name, err)
		}
		if got != want {
			t.Fatalf("persisted metric %s = %v, want %v", name, got, want)
		}
	}
	var validationCount int
	if err := f.st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM run_validations WHERE run_id = ?`, res.RunID).Scan(&validationCount); err != nil {
		t.Fatal(err)
	}
	if validationCount != 2 {
		t.Fatalf("run_validations = %d, want 2", validationCount)
	}

	// Session request carries the task timeout and auto flag.
	if a.startCount() != 1 {
		t.Fatalf("Start calls = %d, want 1", a.startCount())
	}
	start := a.firstStart(t)
	if start.Dir != filepath.Join(res.ArtifactsDir, "worktree") {
		t.Fatalf("start dir = %q", start.Dir)
	}
	if start.Prompt != "fix the bug" || start.Agent != "build" || start.Model != "p/m" || start.Variant != "high" {
		t.Fatalf("start request = %+v", start)
	}
	if start.Timeout != 30*time.Second || !start.Auto || start.Pure {
		t.Fatalf("timeout/auto/pure = %v/%v/%v", start.Timeout, start.Auto, start.Pure)
	}

	// The worktree is removed by default.
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
}

func TestRunValidatorFailure(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 1"}},
	}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if len(res.Validations) != 1 || res.Validations[0].Status != "failed" {
		t.Fatalf("validations = %+v", res.Validations)
	}
	for name, want := range map[string]float64{
		"validator_failures": 1, "success": 0, "first_shot_success": 0,
		"files_changed": 2, "files_created": 1, "diff_lines_added": 1, "diff_lines_removed": 1,
	} {
		if got := res.Metrics[name]; got != want {
			t.Errorf("metric %s = %v, want %v", name, got, want)
		}
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "failed" {
		t.Fatalf("run row status = %q, want failed", row.Status)
	}
	var persistedSuccess float64
	if err := f.st.DB().QueryRowContext(context.Background(),
		`SELECT value_num FROM run_metrics WHERE run_id = ? AND name = 'success'`, res.RunID).Scan(&persistedSuccess); err != nil {
		t.Fatalf("persisted success metric: %v", err)
	}
	if persistedSuccess != 0 {
		t.Fatalf("persisted success = %v, want 0", persistedSuccess)
	}
}

func TestRunStartError(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	a := newScriptedAdapter(t)
	a.startErr = errors.New("boom: cannot start")

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run returned an infrastructure error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Error, "boom") {
		t.Fatalf("error = %q", res.Error)
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("events.jsonl exists for a failed start: %v", err)
	}
	// result.json is written even when Start fails, with an empty metric set.
	resultBytes, err := os.ReadFile(filepath.Join(res.ArtifactsDir, "result.json"))
	if err != nil {
		t.Fatalf("result.json missing on start error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "error" || !strings.Contains(result["error"].(string), "boom") {
		t.Fatalf("result.json = %v", result)
	}
	if metrics, ok := result["metrics"].(map[string]any); !ok || len(metrics) != 0 {
		t.Fatalf("result.json metrics = %v, want an empty object", result["metrics"])
	}
	if res.ExitCode != nil {
		t.Fatalf("exit code = %v, want nil", res.ExitCode)
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "error" || !strings.Contains(row.Error, "boom") {
		t.Fatalf("run row = %+v", row)
	}
	var metricRows int
	if err := f.st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM run_metrics WHERE run_id = ?`, res.RunID).Scan(&metricRows); err != nil {
		t.Fatal(err)
	}
	if metricRows != 0 {
		t.Fatalf("run_metrics rows on start error = %d, want 0 (omit, do not zero)", metricRows)
	}
}

func TestRunNonZeroExitIsError(t *testing.T) {
	useMode(t, "exit3")
	f := setupRunner(t)
	// The validator passes, but a non-zero OpenCode exit still makes the run an
	// error.
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 0"}},
	}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if res.ExitCode == nil || *res.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", res.ExitCode)
	}
	if len(res.Validations) != 1 || res.Validations[0].Status != "passed" {
		t.Fatalf("validations = %+v, want one passed", res.Validations)
	}
	if res.Metrics["success"] != 0 || res.Metrics["first_shot_success"] != 0 {
		t.Fatalf("success metrics = %v/%v, want 0/0", res.Metrics["success"], res.Metrics["first_shot_success"])
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "error" || row.ExitCode == nil || *row.ExitCode != 3 {
		t.Fatalf("run row = %+v", row)
	}
}

func TestRunNonZeroExitBeatsValidatorFailure(t *testing.T) {
	useMode(t, "exit3")
	f := setupRunner(t)
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 1"}},
	}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error (exit takes precedence over a failing validator)", res.Status)
	}
	if len(res.Validations) != 1 || res.Validations[0].Status != "failed" {
		t.Fatalf("validations = %+v", res.Validations)
	}
	if res.Metrics["validator_failures"] != 1 {
		t.Fatalf("validator_failures = %v, want 1", res.Metrics["validator_failures"])
	}
}

func TestRunExtraEnvReachesChildAndValidators(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	overlay := "OPENCODE_CONFIG=/tmp/ocbench-overlay.json"
	// The validator only passes when the overlay variable survived the sandbox
	// and reached the validator process, proving the same env feeds both.
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "overlay env", Command: []string{"sh", "-c", `test "$OPENCODE_CONFIG" = "/tmp/ocbench-overlay.json"`}},
	}
	req := runnerRequest(f)
	req.ExtraEnv = []string{overlay}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (error=%q)", res.Status, res.Error)
	}
	if len(res.Validations) != 1 || res.Validations[0].Status != "passed" {
		t.Fatalf("validations = %+v, want one passed (overlay env must reach validators)", res.Validations)
	}
	if got := a.firstStart(t).Env; !contains(got, overlay) {
		t.Errorf("adapter Start env missing %q: %v", overlay, got)
	}
}

func TestRunCompletionBeforeDeadlinePasses(t *testing.T) {
	useMode(t, "near")
	f := setupRunner(t)
	f.task.TimeoutSeconds = 5
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (a process finishing before the deadline is not a timeout)", res.Status)
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", res.ExitCode)
	}
}

func TestRunUntrackedSymlinkNotCopied(t *testing.T) {
	useMode(t, "symlink")
	f := setupRunner(t)
	f.task.AllowChanges = []string{"src/**", "untracked.txt", "escape-link"}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !contains(res.ChangedFiles, "escape-link") {
		t.Fatalf("changed = %v, want escape-link", res.ChangedFiles)
	}
	if _, err := os.Lstat(filepath.Join(res.ArtifactsDir, "untracked", "escape-link")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was copied into untracked/: %v", err)
	}
	// The target really existed outside the worktree, so the copy was skipped
	// because it is a symlink, not because the target was missing.
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "..", "outside-secret.txt")); err != nil {
		t.Fatalf("outside symlink target missing: %v", err)
	}
	// Regular untracked bytes are still captured.
	artifact(t, res.ArtifactsDir, "untracked/untracked.txt")
}

func TestRunTimeout(t *testing.T) {
	useMode(t, "slow")
	f := setupRunner(t)
	f.task.TimeoutSeconds = 1
	a := newScriptedAdapter(t)

	start := time.Now()
	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "timeout" {
		t.Fatalf("status = %q, want timeout (error=%q)", res.Status, res.Error)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "timeout" {
		t.Fatalf("run row status = %q, want timeout", row.Status)
	}
	// The post-run phase (artifact capture, persistence, cleanup) must not
	// inherit the exhausted 1s task budget: with a fresh max(timeout, 5s)
	// deadline the timed-out run still writes its result and removes its
	// worktree, even under -race instrumentation.
	artifact(t, res.ArtifactsDir, "result.json")
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree still present after timeout (err = %v)", err)
	}
}

// TestRunDrainErrorKillsAndReapsSession proves that a failure while draining
// the event stream (for example an events.jsonl open/write error) does not
// return before the session process group is killed and reaped. The slow helper
// sleeps for 30s, so the process can only be gone if Run killed and waited on
// it; the injected seam makes the drain fail after the child has started.
func TestRunDrainErrorKillsAndReapsSession(t *testing.T) {
	useMode(t, "slow")
	f := setupRunner(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv(fakePIDFileVar, pidFile)

	childReady := make(chan struct{})
	openEventsFile = func(string) (*os.File, error) {
		// Block until the helper child has recorded its PID, so the drain
		// failure provably lands after the session began.
		<-childReady
		return nil, errors.New("injected events.jsonl open failure")
	}
	t.Cleanup(func() { openEventsFile = os.Create })

	a := newScriptedAdapter(t)
	req := runnerRequest(f)
	req.EnvPolicy.PassEnv = append(req.EnvPolicy.PassEnv, fakePIDFileVar)

	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), a, f.st, req)
		done <- err
	}()

	waitForFile(t, pidFile, 20*time.Second)
	close(childReady)
	pid := readChildPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "injected events.jsonl open failure") {
			t.Fatalf("Run error = %v, want the injected drain error", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after a drain error")
	}

	// With the fix Run killed and reaped the child before returning; without it
	// the slow helper is still sleeping and this signal succeeds.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("helper child %d is still alive after Run returned the drain error", pid)
	}

	// The deferred cleanup must still have removed the disposable worktree.
	entries, err := os.ReadDir(f.paths.Runs)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(f.paths.Runs, e.Name(), "worktree")); !os.IsNotExist(err) {
			t.Fatalf("worktree still present under %s after drain error", e.Name())
		}
	}
}

// readChildPID reads the integer PID the slow helper recorded.
func readChildPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read helper pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse helper pid %q: %v", data, err)
	}
	return pid
}

func TestRunDryRunStartsNoSession(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 0"}},
	}
	a := newScriptedAdapter(t)
	req := runnerRequest(f)
	req.DryRun = true

	res, err := Run(context.Background(), a, f.st, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "dry_run" {
		t.Fatalf("status = %q, want dry_run", res.Status)
	}
	if a.startCount() != 0 {
		t.Fatalf("Start calls = %d, want 0", a.startCount())
	}
	artifact(t, res.ArtifactsDir, "plan.json")
	var plan map[string]any
	planBytes, err := os.ReadFile(filepath.Join(res.ArtifactsDir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatal(err)
	}
	if plan["profile_hash"] != "hash-1" || plan["task_id"] != "py-bugfix" || plan["fixture_sha"] == "" {
		t.Fatalf("plan = %v", plan)
	}
	names, _ := plan["env_names"].([]any)
	for _, want := range []string{"PATH", "OCBENCH", "GIT_TERMINAL_PROMPT", fakeHelperGuard} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("plan env_names missing %s: %v", want, names)
		}
	}
	validators, _ := plan["validators"].([]any)
	if len(validators) != 1 {
		t.Fatalf("plan validators = %v, want 1", plan["validators"])
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("events.jsonl exists in dry run: %v", err)
	}
	// Dry run is the documented exception: plan.json, no result.json.
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "result.json")); !os.IsNotExist(err) {
		t.Fatalf("result.json exists in dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree exists after dry run: %v", err)
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "dry_run" || !row.DryRun {
		t.Fatalf("run row = %+v", row)
	}
}

func TestRunKeepWorktree(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	a := newScriptedAdapter(t)
	req := runnerRequest(f)
	req.KeepWorktree = true

	res, err := Run(context.Background(), a, f.st, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "worktree")); err != nil {
		t.Fatalf("worktree was removed despite KeepWorktree: %v", err)
	}
}

func TestRunUnexpectedFiles(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.AllowChanges = []string{"src/**"}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(res.UnexpectedFiles, []string{"untracked.txt"}) {
		t.Fatalf("unexpected = %v, want [untracked.txt]", res.UnexpectedFiles)
	}
}

func TestRunMissingRequirementSkipped(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.Requires = []string{"ocbench-definitely-missing-binary-xyz"}
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "unit tests", Command: []string{"sh", "-c", "exit 1"}},
	}
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (a skipped validator must not fail the run)", res.Status)
	}
	if len(res.Validations) != 1 || res.Validations[0].Status != "skipped" {
		t.Fatalf("validations = %+v", res.Validations)
	}
	if !strings.Contains(res.Validations[0].Output, "ocbench-definitely-missing-binary-xyz") {
		t.Fatalf("skip reason = %q", res.Validations[0].Output)
	}
}

func TestRunUntrackedBytesCaptured(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	a := newScriptedAdapter(t)

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(res.ArtifactsDir, "untracked", "untracked.txt"))
	if err != nil {
		t.Fatalf("read untracked capture: %v", err)
	}
	if string(data) != "hello untracked\n" {
		t.Fatalf("untracked bytes = %q", data)
	}
}

func TestRunExportFailureDoesNotFail(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	a := newScriptedAdapter(t)
	a.exportErr = errors.New("export exploded")

	res, err := Run(context.Background(), a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed", res.Status)
	}
	if !strings.Contains(res.Error, "export") {
		t.Fatalf("error = %q, want an export note", res.Error)
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("session.json exists despite export failure: %v", err)
	}
}

func TestRunPureDisablesAuto(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	a := newScriptedAdapter(t)
	req := runnerRequest(f)
	req.Auto = true
	req.Pure = true

	if _, err := Run(context.Background(), a, f.st, req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	start := a.firstStart(t)
	if start.Pure != true || start.Auto != false {
		t.Fatalf("pure/auto = %v/%v, want true/false", start.Pure, start.Auto)
	}
}

func TestMatchAny(t *testing.T) {
	cases := []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"src/**"}, "src/main.go", true},
		{[]string{"src/**"}, "src/a/b/c.go", true},
		{[]string{"src/**"}, "src", true},
		{[]string{"src/**"}, "tests/a.py", false},
		{[]string{"**/*.go"}, "src/a/b.go", true},
		{[]string{"**/*.go"}, "a.go", true},
		{[]string{"**/*.go"}, "a.py", false},
		{[]string{"calc.py"}, "calc.py", true},
		{[]string{"calc.py"}, "src/calc.py", false},
		{[]string{"tests/**", "cli.py"}, "cli.py", true},
		{[]string{"tests/**", "cli.py"}, "tests/test_x.py", true},
		{[]string{"tests/**", "cli.py"}, "other.py", false},
		{nil, "anything", false},
		{[]string{}, "anything", false},
	}
	for _, tc := range cases {
		if got := matchAny(tc.globs, tc.path); got != tc.want {
			t.Errorf("matchAny(%v, %q) = %v, want %v", tc.globs, tc.path, got, tc.want)
		}
	}
}

func TestSafeLogName(t *testing.T) {
	cases := map[string]string{
		"unit tests":  "unit_tests",
		"cli smoke":   "cli_smoke",
		"a/b\\c:d":    "a_b_c_d",
		"root-cause.": "root-cause.",
		"":            "",
	}
	for in, want := range cases {
		if got := safeLogName(in); got != want {
			t.Errorf("safeLogName(%q) = %q, want %q", in, got, want)
		}
	}
}
