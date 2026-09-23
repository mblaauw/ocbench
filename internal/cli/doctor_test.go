package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/doctor"
	"mbl/ocbench/internal/opencode"
)

// cliFakeAdapter is a canned Adapter for CLI tests. Injecting it through Deps
// keeps the tests away from the real opencode binary.
type cliFakeAdapter struct {
	config []byte
}

func (f *cliFakeAdapter) Version(context.Context) (string, error) { return "1.18.32", nil }
func (f *cliFakeAdapter) ResolvedConfig(context.Context, string) ([]byte, error) {
	return f.config, nil
}
func (f *cliFakeAdapter) Skills(context.Context, string) ([]opencode.SkillInfo, error) {
	return []opencode.SkillInfo{{Name: "ruff", Description: "lint", Location: "/nope/ruff/SKILL.md", Content: "# ruff"}}, nil
}
func (f *cliFakeAdapter) Agent(_ context.Context, _, name string) (opencode.AgentInfo, error) {
	return opencode.AgentInfo{Name: name, Mode: "primary"}, nil
}
func (f *cliFakeAdapter) MCPStatus(context.Context, string) ([]opencode.MCPStatus, error) {
	return []opencode.MCPStatus{{Name: "gitlab", Enabled: true}}, nil
}

// Start and Export satisfy the opencode.Adapter interface; CLI tests never
// stream a run or export a session.
func (f *cliFakeAdapter) Start(context.Context, opencode.RunRequest) (*opencode.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *cliFakeAdapter) Export(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func cliTestExec(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// cliTestDeps returns fully injected deps rooted in temp dirs so no test reads
// or writes the developer's real config, DB or home.
func cliTestDeps(t *testing.T) Deps {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
	cfg.OpenCodeBin = cliTestExec(t)
	return Deps{
		Adapter: &cliFakeAdapter{config: []byte(`{"default_agent":"build","agent":{"build":{}},"mcp":{"gitlab":{"enabled":true}}}`)},
		Paths:   paths,
		Config:  cfg,
	}
}

func TestNewRootWithDepsRegistersCommands(t *testing.T) {
	root := NewRootWithDeps(Deps{})
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"doctor", "version", "snapshot", "run", "experiment", "history", "compare"} {
		if !names[want] {
			t.Fatalf("commands = %v, want %q registered", names, want)
		}
	}
}

func TestExecuteDoctorHealthy(t *testing.T) {
	d := cliTestDeps(t)
	if err := execute(context.Background(), []string{"doctor"}, d); err != nil {
		t.Fatalf("execute doctor: %v", err)
	}
}

func TestExecuteDoctorReturnsErrUnhealthy(t *testing.T) {
	d := cliTestDeps(t)
	d.Config.OpenCodeBin = filepath.Join(t.TempDir(), "missing-opencode")

	err := execute(context.Background(), []string{"doctor"}, d)
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("execute doctor error = %v, want ErrUnhealthy", err)
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	cmd := newDoctorCmd(cliTestDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report doctor.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if !report.Healthy() {
		t.Fatalf("report unhealthy: %+v", report.Checks)
	}
	if report.ProfileHash == "" || report.OpenCodeVersion != "1.18.32" {
		t.Fatalf("report = %+v", report)
	}
	if report.AgentCount != 1 || report.SkillCount != 1 || report.EnabledMCPs != 1 {
		t.Fatalf("counts = %+v", report)
	}
}

func TestDoctorHumanOutput(t *testing.T) {
	cmd := newDoctorCmd(cliTestDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("output too short: %q", out.String())
	}
	header := strings.Fields(lines[0])
	if !reflect.DeepEqual(header, []string{"NAME", "STATUS", "DETAIL"}) {
		t.Fatalf("header = %v, want NAME STATUS DETAIL", header)
	}
	if !strings.Contains(lines[len(lines)-1], "0 warn, 0 fail") {
		t.Fatalf("summary = %q, want warnings and failures", lines[len(lines)-1])
	}
}

func TestDepsResolveFillsZeroFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("OCBENCH_HOME", "")

	got, err := (Deps{Adapter: &cliFakeAdapter{}}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(home, ".config", "ocbench", "config.yaml"); got.Paths.ConfigFile != want {
		t.Fatalf("ConfigFile = %q, want %q", got.Paths.ConfigFile, want)
	}
	if got.Config.OpenCodeBin != "opencode" {
		t.Fatalf("OpenCodeBin = %q, want default", got.Config.OpenCodeBin)
	}
	if got.Adapter == nil {
		t.Fatal("Adapter was not resolved")
	}
}

func TestDepsResolveKeepsInjectedValues(t *testing.T) {
	d := cliTestDeps(t)
	got, err := d.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Paths != d.Paths {
		t.Fatalf("Paths = %+v, want %+v", got.Paths, d.Paths)
	}
	if !reflect.DeepEqual(got.Config, d.Config) {
		t.Fatalf("Config = %+v, want %+v", got.Config, d.Config)
	}
	if got.Adapter != d.Adapter {
		t.Fatal("Adapter was replaced")
	}
}

func TestDepsResolveReturnsConfigError(t *testing.T) {
	base := t.TempDir()
	paths := config.Paths{
		Home:       filepath.Join(base, "data"),
		ConfigFile: filepath.Join(base, "config.yaml"),
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(": : :"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Deps{Adapter: &cliFakeAdapter{}, Paths: paths}).resolve(); err == nil {
		t.Fatal("expected config parse error")
	}
}
