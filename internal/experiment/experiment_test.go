package experiment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/runner"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
)

// The fake OpenCode binary for experiment tests is this test binary re-invoked
// with -test.run=TestExperimentHelperProcess. It is only active when
// OCBENCH_EXP_FAKE_OPENCODE=1, which the runner forwards through the sandbox
// allowlist. `debug config` varies with the overlay environment so each arm
// fingerprints to a distinct profile hash.
const (
	expHelperGuard = "OCBENCH_EXP_FAKE_OPENCODE"
	expHelperMode  = "OCBENCH_EXP_FAKE_MODE"
	expSessionID   = "ses_exp"
)

// TestExperimentHelperProcess impersonates opencode for the experiment tests.
func TestExperimentHelperProcess(t *testing.T) {
	if os.Getenv(expHelperGuard) != "1" {
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
		// The overlay env (set for that arm's discovery adapter) changes the
		// resolved config, so each arm gets its own profile hash.
		overlay := os.Getenv("OPENCODE_CONFIG")
		if overlay == "" {
			overlay = os.Getenv("OPENCODE_CONFIG_DIR")
		}
		if overlay == "" {
			overlay = "none"
		}
		write(fmt.Sprintf(`{"default_agent":"build","model":"p/m","agent":{"build":{}},"overlay":%q}`, overlay))
	case len(args) > 1 && args[0] == "debug" && args[1] == "skill":
		write("[]")
	case len(args) > 2 && args[0] == "debug" && args[1] == "agent":
		write(fmt.Sprintf(`{"name":%q,"mode":"primary","native":false,"model":"p/m"}`, args[2]))
	case len(args) > 0 && args[0] == "export":
		write(`{"info":{"id":"ses_exp","tokens":{"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01},"messages":[]}`)
	case len(args) > 0 && args[0] == "run":
		fmt.Fprintln(os.Stdout, `{"type":"step_start","timestamp":1,"sessionID":"ses_exp","part":{"type":"step-start"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"text","timestamp":2,"sessionID":"ses_exp","part":{"type":"text","text":"done"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"step_finish","timestamp":3,"sessionID":"ses_exp","part":{"type":"step-finish","reason":"stop","tokens":{"total":10,"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01}}`)
		os.Exit(0)
	}
	os.Exit(42)
}

func TestParseArmValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseArm("B=" + path)
	if err != nil {
		t.Fatalf("ParseArm: %v", err)
	}
	if got.Label != "B" {
		t.Errorf("label = %q, want B", got.Label)
	}
	if got.Overlay.Kind != OverlayFile || got.Overlay.Path != path {
		t.Errorf("overlay = %+v, want file %s", got.Overlay, path)
	}
	if len(got.Overlay.Env) != 1 || got.Overlay.Env[0] != "OPENCODE_CONFIG="+path {
		t.Errorf("overlay env = %v, want OPENCODE_CONFIG=%s", got.Overlay.Env, path)
	}
}

func TestParseArmValidDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseArm("A=" + dir)
	if err != nil {
		t.Fatalf("ParseArm: %v", err)
	}
	if got.Label != "A" || got.Overlay.Kind != OverlayDir || got.Overlay.Path != dir {
		t.Errorf("got %+v", got)
	}
}

func TestParseArmInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"empty label":  "=" + path,
		"empty path":   "A=",
		"no separator": "A" + path,
		"empty spec":   "",
	}
	for name, spec := range cases {
		if _, err := ParseArm(spec); err == nil {
			t.Errorf("%s: ParseArm(%q) = nil error, want error", name, spec)
		}
	}
}

func TestBuildPlanInterleavesTaskMajor(t *testing.T) {
	tasks := []*suite.Task{{ID: "t1"}, {ID: "t2"}}
	arms := []ArmSpec{{Label: "A"}, {Label: "B"}}

	got := BuildPlan(tasks, arms, 2)
	want := []struct {
		task   string
		arm    string
		repeat int
	}{
		{"t1", "A", 0}, {"t1", "B", 0}, {"t1", "A", 1}, {"t1", "B", 1},
		{"t2", "A", 0}, {"t2", "B", 0}, {"t2", "A", 1}, {"t2", "B", 1},
	}
	if len(got) != len(want) {
		t.Fatalf("plan length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		s := got[i]
		if s.Task.ID != w.task || s.Arm.Label != w.arm || s.RepeatIndex != w.repeat {
			t.Errorf("step %d = (%s,%s,%d), want (%s,%s,%d)",
				i, s.Task.ID, s.Arm.Label, s.RepeatIndex, w.task, w.arm, w.repeat)
		}
	}
}

// recordingAdapter records every Start request while delegating discovery and
// the session itself to a helper-backed Real adapter.
type recordingAdapter struct {
	*opencode.Real

	mu     sync.Mutex
	starts []opencode.RunRequest
}

func (a *recordingAdapter) Start(ctx context.Context, req opencode.RunRequest) (*opencode.Session, error) {
	a.mu.Lock()
	a.starts = append(a.starts, req)
	a.mu.Unlock()
	return a.Real.Start(ctx, req)
}

func (a *recordingAdapter) startsCopy() []opencode.RunRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]opencode.RunRequest(nil), a.starts...)
}

type expFixture struct {
	st    *store.Store
	paths config.Paths
	s     *suite.Suite
	tasks []*suite.Task
}

func writeExpFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupExperiment(t *testing.T) *expFixture {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	writeExpFile(t, filepath.Join(dir, "suite.yaml"), "name: mini\nversion: \"1\"\n")
	for _, id := range []string{"t1", "t2"} {
		base := filepath.Join(dir, "tasks", id)
		writeExpFile(t, filepath.Join(base, "task.yaml"),
			fmt.Sprintf("id: %s\nversion: \"1\"\nname: %s\n", id, id))
		writeExpFile(t, filepath.Join(base, "prompt.md"), "do "+id+"\n")
		writeExpFile(t, filepath.Join(base, "fixture", "hello.txt"), "hi\n")
	}
	s, err := suite.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

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
	if err := config.EnsureDirs(paths); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	return &expFixture{st: st, paths: paths, s: s, tasks: s.Tasks}
}

func TestRunOrchestratesInterleavedArms(t *testing.T) {
	ctx := context.Background()
	t.Setenv(expHelperGuard, "1")
	t.Setenv("HOME", t.TempDir())
	f := setupExperiment(t)

	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var adapters []*recordingAdapter
	adapterFor := func(arm ArmSpec) opencode.Adapter {
		a := &recordingAdapter{Real: opencode.NewReal(opencode.Options{
			Bin:        bin,
			Timeout:    20 * time.Second,
			Env:        append(os.Environ(), arm.Overlay.Env...),
			TestPrefix: []string{"-test.run=TestExperimentHelperProcess", "--"},
		})}
		mu.Lock()
		adapters = append(adapters, a)
		mu.Unlock()
		return a
	}

	fileOverlay := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(fileOverlay, []byte(`{"agent":{"build":{"temperature":0.1}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dirOverlay := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirOverlay, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	armA, err := ParseArm("A=" + fileOverlay)
	if err != nil {
		t.Fatal(err)
	}
	armB, err := ParseArm("B=" + dirOverlay)
	if err != nil {
		t.Fatal(err)
	}
	arms := []ArmSpec{armA, armB}

	out, err := Run(ctx, f.st, Request{
		Suite:      f.s,
		Tasks:      f.tasks,
		Arms:       arms,
		Baseline:   "A",
		Paths:      f.paths,
		Profile:    profile.Options{Dir: t.TempDir(), Auto: true},
		AdapterFor: adapterFor,
		EnvPolicy:  runner.EnvPolicy{PassEnv: []string{expHelperGuard, expHelperMode}},
		Repeat:     2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExperimentID == "" {
		t.Fatal("Outcome.ExperimentID is empty")
	}

	// Exactly one experiment row.
	var experiments int
	if err := f.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM experiments WHERE id = ?`, out.ExperimentID).Scan(&experiments); err != nil {
		t.Fatal(err)
	}
	if experiments != 1 {
		t.Fatalf("experiment rows = %d, want 1", experiments)
	}

	// Two arm rows with distinct profile hashes, keyed by label.
	armRows, err := f.st.ListExperimentArms(ctx, out.ExperimentID)
	if err != nil {
		t.Fatalf("ListExperimentArms: %v", err)
	}
	if len(armRows) != 2 {
		t.Fatalf("arm rows = %d, want 2", len(armRows))
	}
	armIDByLabel := map[string]string{}
	for _, a := range armRows {
		armIDByLabel[a.Label] = a.ID
		if a.ProfileHash == "" {
			t.Errorf("arm %s has empty profile hash", a.Label)
		}
	}
	if armRows[0].ProfileHash == armRows[1].ProfileHash {
		t.Errorf("arm profile hashes are not distinct: %q", armRows[0].ProfileHash)
	}
	if armIDByLabel["A"] == "" || armIDByLabel["B"] == "" {
		t.Fatalf("arm labels not persisted: %+v", armRows)
	}

	// Every run carries its plan step's (task, arm_id, repeat_index), in plan
	// order.
	plan := BuildPlan(f.tasks, arms, 2)
	if len(out.RunIDs) != len(plan) {
		t.Fatalf("run ids = %d, want %d", len(out.RunIDs), len(plan))
	}
	for i, step := range plan {
		row, err := f.st.GetRun(ctx, out.RunIDs[i])
		if err != nil {
			t.Fatalf("GetRun(%s): %v", out.RunIDs[i], err)
		}
		if row.TaskID != step.Task.ID {
			t.Errorf("run %d task = %q, want %q", i, row.TaskID, step.Task.ID)
		}
		if row.ArmID == nil || *row.ArmID != armIDByLabel[step.Arm.Label] {
			t.Errorf("run %d arm = %v, want %s", i, row.ArmID, armIDByLabel[step.Arm.Label])
		}
		if row.RepeatIndex != step.RepeatIndex {
			t.Errorf("run %d repeat = %d, want %d", i, row.RepeatIndex, step.RepeatIndex)
		}
	}

	// The overlay env reaches each arm's run child as ExtraEnv.
	mu.Lock()
	gotAdapters := append([]*recordingAdapter(nil), adapters...)
	mu.Unlock()
	if len(gotAdapters) != 2 {
		t.Fatalf("AdapterFor calls = %d, want 2 (one per arm)", len(gotAdapters))
	}
	for _, a := range gotAdapters {
		for _, req := range a.startsCopy() {
			joined := strings.Join(req.Env, "\n")
			hasFile := strings.Contains(joined, "OPENCODE_CONFIG="+fileOverlay)
			hasDir := strings.Contains(joined, "OPENCODE_CONFIG_DIR="+dirOverlay)
			if !hasFile && !hasDir {
				t.Errorf("run env carries no overlay: %v", req.Env)
			}
		}
	}
}

func TestRunRejectsBaselineNotAmongArms(t *testing.T) {
	f := setupExperiment(t)
	_, err := Run(context.Background(), f.st, Request{
		Suite:      f.s,
		Tasks:      f.tasks,
		Arms:       []ArmSpec{{Label: "A"}, {Label: "B"}},
		Baseline:   "C",
		Paths:      f.paths,
		AdapterFor: func(ArmSpec) opencode.Adapter { return nil },
		Repeat:     1,
	})
	if err == nil {
		t.Fatal("Run with unknown baseline: want error, got nil")
	}
	if !strings.Contains(err.Error(), "baseline") {
		t.Errorf("error %q does not mention baseline", err)
	}
}
