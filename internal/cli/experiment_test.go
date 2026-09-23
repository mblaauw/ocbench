package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/store"
)

// failArmOverlayMarker is a substring of the overlay path the regression test
// passes as the failing arm. The fake adapter returns a scripted Start error
// whenever the run environment carries an OPENCODE_CONFIG entry containing it.
const failArmOverlayMarker = "fail-arm.json"

// experimentTestAdapter serves discovery through the helper-backed Real
// adapter and lets a test script Start to fail for one arm, identified by the
// arm's overlay environment (the OPENCODE_CONFIG entry the runner overlays).
type experimentTestAdapter struct {
	*opencode.Real

	mu     sync.Mutex
	starts []opencode.RunRequest
	failOn string
}

func (a *experimentTestAdapter) Start(ctx context.Context, req opencode.RunRequest) (*opencode.Session, error) {
	a.mu.Lock()
	a.starts = append(a.starts, req)
	a.mu.Unlock()
	if a.failOn != "" && envContains(req.Env, a.failOn) {
		return nil, errors.New("scripted arm failure")
	}
	return a.Real.Start(ctx, req)
}

func (a *experimentTestAdapter) startCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.starts)
}

// envContains reports whether any KEY=VALUE entry contains marker.
func envContains(env []string, marker string) bool {
	for _, kv := range env {
		if strings.Contains(kv, marker) {
			return true
		}
	}
	return false
}

// newExperimentTestDeps builds deps whose injected adapter is the guarded test
// binary, exactly like newRunTestDeps but with the experiment fake that can
// fail one arm on demand.
func newExperimentTestDeps(t *testing.T) (Deps, *experimentTestAdapter) {
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
	cfg.Sandbox.PassEnv = []string{runHelperGuard, runHelperMode, runHelperCounterDir, runHelperMCPTool, runHelperStartedDir}
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	adapter := &experimentTestAdapter{
		Real: opencode.NewReal(opencode.Options{
			Bin:        bin,
			Timeout:    20 * time.Second,
			Env:        os.Environ(),
			TestPrefix: []string{"-test.run=TestRunHelperProcess", "--"},
		}),
		failOn: failArmOverlayMarker,
	}
	return Deps{Adapter: adapter, Paths: paths, Config: cfg}, adapter
}

// runExperimentCmd executes `experiment run` with injected deps and returns its
// output and error.
func runExperimentCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newExperimentCmd(d)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

func TestExperimentRunUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"one profile", []string{"run", "--profile", "a="}},
		{"duplicate labels", []string{"run", "--profile", "a=", "--profile", "a="}},
		{"unknown baseline", []string{"run", "--profile", "a=", "--profile", "b=", "--baseline", "c"}},
		{"zero repeat", []string{"run", "--profile", "a=", "--profile", "b=", "--repeat", "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := cliTestDeps(t)
			_, err := runExperimentCmd(t, d, tc.args...)
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v, want *UsageError", err)
			}
		})
	}
}

func TestExperimentRunHappyPathPersistsDistinctArms(t *testing.T) {
	d, adapter := newExperimentTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	out, err := runExperimentCmd(t, d, "run", "--profile", "base=", "--profile", "candidate=", "mini", "--suite-dir", suiteDir)
	if err != nil {
		t.Fatalf("experiment run: %v\n%s", err, out)
	}
	if got := adapter.startCount(); got != 2 {
		t.Fatalf("Start calls = %d, want 2 (one per arm)", got)
	}

	st := openRunStore(t, d)
	ctx := context.Background()
	experiments, err := st.ListExperiments(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(experiments) != 1 {
		t.Fatalf("experiments = %d, want 1", len(experiments))
	}
	arms, err := st.ListExperimentArms(ctx, experiments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(arms))
	}

	runs, err := st.ListRuns(ctx, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	armIDs := map[string]bool{}
	for _, r := range runs {
		if r.ArmID == nil {
			t.Fatalf("run %s has no arm id", r.ID)
		}
		armIDs[*r.ArmID] = true
	}
	if len(armIDs) != 2 {
		t.Fatalf("distinct arm ids = %d, want 2", len(armIDs))
	}
	if !strings.Contains(out, "regression: insufficient data\n") {
		t.Fatalf("output missing insufficient-data regression line:\n%s", out)
	}
}

func TestRenderExperimentJSONEmptyShapes(t *testing.T) {
	var buf bytes.Buffer
	if err := renderExperimentJSON(&buf, experiment.ExperimentSummary{}); err != nil {
		t.Fatalf("renderExperimentJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`"Arms": []`,
		`"Tasks": []`,
		`"DriftWarnings": []`,
		`"CostPerSolved": {}`,
		`"PassRateTests": {}`,
		`"CostTests": {}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON missing %q:\n%s", want, out)
		}
	}
}

func TestExperimentRunRegressionExit(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), failArmOverlayMarker)
	if err := os.WriteFile(overlay, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failSpec := "fail=" + overlay
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	t.Run("with flag exits 3", func(t *testing.T) {
		d, _ := newExperimentTestDeps(t)
		out, err := runExperimentCmd(t, d, "run",
			"--profile", "base=", "--profile", failSpec,
			"--repeat", "4", "--exit-on-regression",
			"mini", "--suite-dir", suiteDir)
		if !errors.Is(err, ErrRegression) {
			t.Fatalf("err = %v, want ErrRegression\n%s", err, out)
		}
		if got := exitCode(err); got != 3 {
			t.Fatalf("exitCode = %d, want 3", got)
		}
		if !strings.Contains(out, "regression: arm fail pass rate") {
			t.Fatalf("output missing verbatim regression reason:\n%s", out)
		}
		if strings.Contains(out, "regression: fail:") {
			t.Fatalf("regression line re-prefixes the arm:\n%s", out)
		}
	})

	t.Run("without flag exits 0", func(t *testing.T) {
		d, _ := newExperimentTestDeps(t)
		out, err := runExperimentCmd(t, d, "run",
			"--profile", "base=", "--profile", failSpec,
			"--repeat", "4",
			"mini", "--suite-dir", suiteDir)
		if err != nil {
			t.Fatalf("experiment run: %v\n%s", err, out)
		}
		if got := exitCode(err); got != 0 {
			t.Fatalf("exitCode = %d, want 0", got)
		}
	})
}

// openExperimentStore opens and migrates the deps' database so a test can seed
// experiments and arms before invoking a command.
func openExperimentStore(t *testing.T, d Deps) *store.Store {
	t.Helper()
	if err := config.EnsureDirs(d.Paths); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(d.Paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(context.Background()); err != nil {
		st.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestExperimentListOrderingLimitAndJSON(t *testing.T) {
	d := cliTestDeps(t)
	st := openExperimentStore(t, d)
	ctx := context.Background()
	for _, e := range []store.ExperimentRow{
		{ID: "exp-a", Name: "a", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z"},
		{ID: "exp-b", Name: "b", SpecJSON: `{}`, CreatedAt: "2026-03-02T10:00:00Z"},
		{ID: "exp-c", Name: "c", SpecJSON: `{}`, CreatedAt: "2026-03-03T10:00:00Z"},
	} {
		if err := st.InsertExperiment(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []store.ExperimentArmRow{
		{ID: "arm-c1", ExperimentID: "exp-c", Label: "a", ProfileHash: "h", OverlayKind: "none", CreatedAt: "2026-03-03T10:00:00Z"},
		{ID: "arm-c2", ExperimentID: "exp-c", Label: "b", ProfileHash: "h", OverlayKind: "none", CreatedAt: "2026-03-03T10:00:00Z"},
		{ID: "arm-b1", ExperimentID: "exp-b", Label: "a", ProfileHash: "h", OverlayKind: "none", CreatedAt: "2026-03-02T10:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	out, err := runExperimentCmd(t, d, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("list lines = %d, want header + 3 rows:\n%s", len(lines), out)
	}
	for i, want := range []string{"exp-c", "exp-b", "exp-a"} {
		fields := strings.Fields(lines[i+1])
		if len(fields) == 0 || fields[0] != want {
			t.Fatalf("row %d = %q, want %s:\n%s", i, lines[i+1], want, out)
		}
	}
	if got := strings.Fields(lines[1])[3]; got != "2" {
		t.Fatalf("exp-c arms = %q, want 2:\n%s", got, out)
	}
	if got := strings.Fields(lines[3])[3]; got != "0" {
		t.Fatalf("exp-a arms = %q, want 0:\n%s", got, out)
	}

	out, err = runExperimentCmd(t, d, "list", "--limit", "1")
	if err != nil {
		t.Fatalf("list --limit 1: %v\n%s", err, out)
	}
	if !strings.Contains(out, "exp-c") || strings.Contains(out, "exp-b") {
		t.Fatalf("limit output = %q, want only exp-c", out)
	}

	out, err = runExperimentCmd(t, d, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	var report struct {
		Experiments []struct {
			ID   string `json:"id"`
			Arms int    `json:"arms"`
		} `json:"experiments"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, out)
	}
	if len(report.Experiments) != 3 || report.Experiments[0].ID != "exp-c" {
		t.Fatalf("JSON experiments = %+v", report.Experiments)
	}
	if report.Experiments[0].Arms != 2 {
		t.Fatalf("exp-c arms = %d, want 2", report.Experiments[0].Arms)
	}

	_, err = runExperimentCmd(t, d, "list", "--limit", "0")
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("--limit 0 err = %v, want *UsageError", err)
	}
}

func TestExperimentListEmptyJSON(t *testing.T) {
	d := cliTestDeps(t)
	out, err := runExperimentCmd(t, d, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"experiments": []`) {
		t.Fatalf("empty list JSON = %s, want []", out)
	}
}

func TestExperimentShowUsageErrors(t *testing.T) {
	d := cliTestDeps(t)
	cases := [][]string{
		{"show"},
		{"show", "missing"},
		{"show", "missing", "--json"},
		{"show", "missing", "--format", "jsonl"},
		{"show", "x", "--format", "bogus"},
		{"show", "x", "--json", "--format", "jsonl"},
	}
	for _, args := range cases {
		_, err := runExperimentCmd(t, d, args...)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Fatalf("%v err = %v, want *UsageError", args, err)
		}
		if got := exitCode(err); got != 2 {
			t.Fatalf("%v exitCode = %d, want 2", args, got)
		}
	}
}

func TestExperimentShowEmptyRenders(t *testing.T) {
	d := cliTestDeps(t)
	st := openExperimentStore(t, d)
	if err := st.InsertExperiment(context.Background(), store.ExperimentRow{
		ID: "exp-empty", Name: "empty", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	out, err := runExperimentCmd(t, d, "show", "exp-empty")
	if err != nil {
		t.Fatalf("show empty: %v\n%s", err, out)
	}
	if !strings.Contains(out, "regression:") {
		t.Fatalf("show empty output = %q", out)
	}
}

func TestExperimentShowJSONL(t *testing.T) {
	d, _ := newExperimentTestDeps(t)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})
	out, err := runExperimentCmd(t, d, "run", "--profile", "base=", "--profile", "candidate=", "mini", "--suite-dir", suiteDir)
	if err != nil {
		t.Fatalf("experiment run: %v\n%s", err, out)
	}

	st := openRunStore(t, d)
	ctx := context.Background()
	exps, err := st.ListExperiments(ctx, 0)
	if err != nil || len(exps) != 1 {
		t.Fatalf("experiments = %+v, %v, want 1", exps, err)
	}
	expID := exps[0].ID
	runs, err := st.RunsForExperiment(ctx, expID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %d, %v, want 2", len(runs), err)
	}

	// An armed run with no metrics and no validations must emit {} and [].
	clone := runs[0]
	clone.ID = "run-novalidations"
	clone.SessionID = "ses_noval"
	clone.StartedAt = "2099-01-01T00:00:00Z"
	clone.FinishedAt = clone.StartedAt
	if err := st.InsertRun(ctx, clone); err != nil {
		t.Fatal(err)
	}
	// A run with no arm is not part of the experiment and must be skipped.
	orphan := runs[0]
	orphan.ID = "run-orphan"
	orphan.SessionID = "ses_orphan"
	orphan.ArmID = nil
	orphan.StartedAt = "2099-01-02T00:00:00Z"
	orphan.FinishedAt = orphan.StartedAt
	if err := st.InsertRun(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	st.Close()

	out, err = runExperimentCmd(t, d, "show", expID, "--format", "jsonl")
	if err != nil {
		t.Fatalf("show --format jsonl: %v\n%s", err, out)
	}
	if strings.Contains(out, "run-orphan") {
		t.Fatalf("nil-arm run leaked into JSONL:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("JSONL lines = %d, want 3:\n%s", len(lines), out)
	}
	noValidations := ""
	for _, line := range lines {
		var env struct {
			SchemaVersion int `json:"schema_version"`
			Run           struct {
				ID string `json:"id"`
			} `json:"run"`
			Validations json.RawMessage `json:"validations"`
			Metrics     json.RawMessage `json:"metrics"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line does not parse: %v\n%s", err, line)
		}
		if env.SchemaVersion != 1 {
			t.Fatalf("schema_version = %d, want 1", env.SchemaVersion)
		}
		if env.Run.ID == "run-novalidations" {
			noValidations = line
			if string(env.Validations) != "[]" {
				t.Fatalf("validations = %s, want []", env.Validations)
			}
			if string(env.Metrics) != "{}" {
				t.Fatalf("metrics = %s, want {}", env.Metrics)
			}
		}
	}
	if noValidations == "" {
		t.Fatalf("run-novalidations missing from JSONL:\n%s", out)
	}
}
