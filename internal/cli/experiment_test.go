package cli

import (
	"bytes"
	"context"
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
