package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/store"
)

// fakeAdapter mirrors the canned-data contract of the Task 5 helper adapter.
// The helper is unexported in package opencode, so doctor keeps its own.
type fakeAdapter struct {
	version    string
	versionErr error
	config     []byte
	configErr  error
	skills     []opencode.SkillInfo
	skillsErr  error
	agents     map[string]opencode.AgentInfo
	mcp        []opencode.MCPStatus
	mcpErr     error
	mcpCalls   int
}

func (f *fakeAdapter) Version(context.Context) (string, error) {
	if f.versionErr != nil {
		return "", f.versionErr
	}
	return f.version, nil
}

func (f *fakeAdapter) ResolvedConfig(context.Context, string) ([]byte, error) {
	if f.configErr != nil {
		return nil, f.configErr
	}
	return f.config, nil
}

func (f *fakeAdapter) Skills(context.Context, string) ([]opencode.SkillInfo, error) {
	if f.skillsErr != nil {
		return nil, f.skillsErr
	}
	return f.skills, nil
}

func (f *fakeAdapter) Agent(_ context.Context, _, name string) (opencode.AgentInfo, error) {
	if a, ok := f.agents[name]; ok {
		return a, nil
	}
	return opencode.AgentInfo{Name: name}, nil
}

func (f *fakeAdapter) MCPStatus(context.Context, string) ([]opencode.MCPStatus, error) {
	f.mcpCalls++
	if f.mcpErr != nil {
		return nil, f.mcpErr
	}
	return f.mcp, nil
}

// Start and Export satisfy the opencode.Adapter interface; doctor tests never
// stream a run or export a session.
func (f *fakeAdapter) Start(context.Context, opencode.RunRequest) (*opencode.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAdapter) Export(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

// newFakeAdapter returns a fake that describes a healthy environment: two
// agents, two skills and one enabled MCP server.
func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		version: "1.18.32",
		config:  []byte(`{"default_agent":"build","agent":{"build":{"mode":"primary"},"plan":{"mode":"subagent"}},"mcp":{"gitlab":{"enabled":true}}}`),
		skills: []opencode.SkillInfo{
			{Name: "ruff", Description: "lint", Location: "/nope/ruff/SKILL.md", Content: "# ruff"},
			{Name: "go", Description: "go help", Location: "/nope/go/SKILL.md", Content: "# go"},
		},
		agents: map[string]opencode.AgentInfo{
			"build": {Name: "build", Mode: "primary"},
			"plan":  {Name: "plan", Mode: "subagent"},
		},
		mcp: []opencode.MCPStatus{{Name: "gitlab", Enabled: true}},
	}
}

// isolateHome points HOME at a temp dir so Discover cannot read the
// developer's global OpenCode instructions.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// tempPaths builds a Paths rooted entirely inside the test temp dir.
func tempPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		Home:       filepath.Join(base, "data"),
		ConfigFile: filepath.Join(base, "config", "ocbench", "config.yaml"),
		DB:         filepath.Join(base, "data", "ocbench.db"),
		Profiles:   filepath.Join(base, "data", "profiles"),
		Runs:       filepath.Join(base, "data", "runs"),
		Suites:     filepath.Join(base, "data", "suites"),
		Cache:      filepath.Join(base, "cache", "ocbench"),
	}
}

// mustExec writes an executable file to stand in for the opencode binary so
// the binary-resolution check passes without touching the real installation.
func mustExec(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func healthyConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.DefaultsConfig()
	cfg.OpenCodeBin = mustExec(t)
	return cfg
}

func checkByName(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, r.Checks)
	return Check{}
}

func TestRunReportsHealthyEnvironment(t *testing.T) {
	isolateHome(t)
	paths := tempPaths(t)
	cfg := healthyConfig(t)

	report, err := Run(context.Background(), newFakeAdapter(), paths, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("report is not healthy: %+v", report.Checks)
	}
	for _, name := range []string{"opencode", "git", "db", "config", "profile", "agents", "skills", "mcp", "sandbox"} {
		c := checkByName(t, report, name)
		if c.Status != StatusOK {
			t.Fatalf("check %s = %s (%s), want ok", name, c.Status, c.Detail)
		}
	}
	if report.OpenCodeVersion != "1.18.32" {
		t.Fatalf("OpenCodeVersion = %q", report.OpenCodeVersion)
	}
	if report.ProfileHash == "" {
		t.Fatal("ProfileHash is empty")
	}
	if report.AgentCount != 2 {
		t.Fatalf("AgentCount = %d, want 2", report.AgentCount)
	}
	if report.SkillCount != 2 {
		t.Fatalf("SkillCount = %d, want 2", report.SkillCount)
	}
	if report.EnabledMCPs != 1 {
		t.Fatalf("EnabledMCPs = %d, want 1", report.EnabledMCPs)
	}

	// The DB check must actually apply the embedded migration.
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer st.Close()
	v, err := st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != 1 {
		t.Fatalf("schema version = %d, want 1", v)
	}
}

func TestRunFailsWhenBinaryMissing(t *testing.T) {
	isolateHome(t)
	paths := tempPaths(t)
	cfg := healthyConfig(t)
	cfg.OpenCodeBin = filepath.Join(t.TempDir(), "does-not-exist")

	report, err := Run(context.Background(), newFakeAdapter(), paths, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Healthy() {
		t.Fatal("report must be unhealthy when the binary is missing")
	}
	c := checkByName(t, report, "opencode")
	if c.Status != StatusFail {
		t.Fatalf("opencode check = %s (%s), want fail", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "not found") {
		t.Fatalf("opencode detail = %q, want it to mention not found", c.Detail)
	}
}

func TestRunFailsOnConfigParseError(t *testing.T) {
	isolateHome(t)
	paths := tempPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(": : :"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(context.Background(), newFakeAdapter(), paths, healthyConfig(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := checkByName(t, report, "config")
	if c.Status != StatusFail {
		t.Fatalf("config check = %s (%s), want fail", c.Status, c.Detail)
	}
	if report.Healthy() {
		t.Fatal("report must be unhealthy when config does not parse")
	}
}

func TestRunFailsWhenProfileDiscoveryFails(t *testing.T) {
	isolateHome(t)
	fa := newFakeAdapter()
	fa.configErr = errors.New("boom")

	report, err := Run(context.Background(), fa, tempPaths(t), healthyConfig(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := checkByName(t, report, "profile")
	if c.Status != StatusFail {
		t.Fatalf("profile check = %s (%s), want fail", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "boom") {
		t.Fatalf("profile detail = %q, want it to carry the adapter error", c.Detail)
	}
}

func TestRunWarnsOnZeroCounts(t *testing.T) {
	isolateHome(t)
	fa := newFakeAdapter()
	fa.config = []byte(`{"agent":{}}`)
	fa.skills = nil
	fa.mcp = nil

	report, err := Run(context.Background(), fa, tempPaths(t), healthyConfig(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"agents", "skills", "mcp"} {
		c := checkByName(t, report, name)
		if c.Status != StatusWarn {
			t.Fatalf("check %s = %s (%s), want warn", name, c.Status, c.Detail)
		}
	}
	if !report.Healthy() {
		t.Fatal("warnings alone must keep the report healthy")
	}
}

func TestRunFallsBackToResolvedConfigWhenMCPStatusFails(t *testing.T) {
	isolateHome(t)
	fa := newFakeAdapter()
	fa.mcpErr = errors.New("connection refused")

	report, err := Run(context.Background(), fa, tempPaths(t), healthyConfig(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := checkByName(t, report, "mcp")
	if c.Status != StatusWarn {
		t.Fatalf("mcp check = %s (%s), want warn", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "connection refused") {
		t.Fatalf("mcp detail = %q, want the adapter error", c.Detail)
	}
	if report.EnabledMCPs != 1 {
		t.Fatalf("EnabledMCPs = %d, want 1 from resolved config", report.EnabledMCPs)
	}
	if !report.Healthy() {
		t.Fatal("an mcp status failure alone must not make the report unhealthy")
	}
}

func TestRunRejectsNilAdapter(t *testing.T) {
	isolateHome(t)
	if _, err := Run(context.Background(), nil, tempPaths(t), healthyConfig(t)); err == nil {
		t.Fatal("expected an error for a nil adapter")
	}
}

func TestReportHealthy(t *testing.T) {
	if !(Report{Checks: []Check{{Status: StatusOK}, {Status: StatusWarn}}}).Healthy() {
		t.Fatal("warnings must not make a report unhealthy")
	}
	if (Report{Checks: []Check{{Status: StatusOK}, {Status: StatusFail}}}).Healthy() {
		t.Fatal("a fail must make a report unhealthy")
	}
}
