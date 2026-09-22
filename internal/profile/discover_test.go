package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"mbl/ocbench/internal/opencode"
)

// fakeAdapter is a local implementation of the opencode.Adapter contract. The
// Task 5 helper adapter is unexported in package opencode, so the profile
// package mirrors the same canned-data contract here.
type fakeAdapter struct {
	version    string
	config     []byte
	skills     []opencode.SkillInfo
	agents     map[string]opencode.AgentInfo
	agentCalls []string
	versionErr error
}

func (f *fakeAdapter) Version(context.Context) (string, error) {
	if f.versionErr != nil {
		return "", f.versionErr
	}
	return f.version, nil
}

func (f *fakeAdapter) ResolvedConfig(context.Context, string) ([]byte, error) { return f.config, nil }

func (f *fakeAdapter) Skills(context.Context, string) ([]opencode.SkillInfo, error) {
	return f.skills, nil
}

func (f *fakeAdapter) Agent(_ context.Context, _, name string) (opencode.AgentInfo, error) {
	f.agentCalls = append(f.agentCalls, name)
	if a, ok := f.agents[name]; ok {
		return a, nil
	}
	return opencode.AgentInfo{Name: name}, nil
}

func (f *fakeAdapter) MCPStatus(context.Context, string) ([]opencode.MCPStatus, error) {
	return nil, nil
}

// Start and Export satisfy the opencode.Adapter interface; profile discovery
// never streams a run or exports a session.
func (f *fakeAdapter) Start(context.Context, opencode.RunRequest) (*opencode.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAdapter) Export(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		version: "1.18.32",
		config:  []byte(`{"default_agent":"build","agent":{"plan":{},"build":{}}}`),
		skills: []opencode.SkillInfo{
			{Name: "ruff", Description: "lint", Location: "/home/u/.agents/skills/ruff/SKILL.md", Content: "# ruff"},
		},
		agents: map[string]opencode.AgentInfo{
			"build": {Name: "build", Mode: "primary"},
			"plan":  {Name: "plan", Mode: "primary"},
		},
	}
}

func TestDiscoverScopesAndAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "AGENTS.md"), []byte("global rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("project rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fa := newFakeAdapter()
	s, err := Discover(context.Background(), fa, project)
	if err != nil {
		t.Fatal(err)
	}
	if s.Home != home {
		t.Fatalf("Home = %q, want %q", s.Home, home)
	}
	if s.OpenCodeVersion != "1.18.32" {
		t.Fatalf("OpenCodeVersion = %q", s.OpenCodeVersion)
	}
	if len(s.Skills) != 1 || len(s.ResolvedConfig) == 0 || s.Dir != project {
		t.Fatalf("sources = %+v", s)
	}
	if got := string(s.Instructions["global:AGENTS.md"]); got != "global rules\n" {
		t.Fatalf("global instructions = %q", got)
	}
	if got := string(s.Instructions["project:AGENTS.md"]); got != "project rules\n" {
		t.Fatalf("project instructions = %q", got)
	}
	if got := s.InstructionPaths["global:AGENTS.md"]; got != filepath.Join(globalDir, "AGENTS.md") {
		t.Fatalf("global instruction path = %q", got)
	}
	if got := s.InstructionPaths["project:AGENTS.md"]; got != filepath.Join(project, "AGENTS.md") {
		t.Fatalf("project instruction path = %q", got)
	}
	gotCalls := append([]string(nil), fa.agentCalls...)
	sort.Strings(gotCalls)
	if !reflect.DeepEqual(gotCalls, []string{"build", "plan"}) {
		t.Fatalf("agent calls = %v, want [build plan]", gotCalls)
	}
}

func TestDiscoverFindsAncestorInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("root rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(project, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Discover(context.Background(), newFakeAdapter(), nested)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(s.Instructions["project:AGENTS.md"]); got != "root rules\n" {
		t.Fatalf("project instructions = %q", got)
	}
	if _, ok := s.Instructions["global:AGENTS.md"]; ok {
		t.Fatal("unexpected global instructions when none exist")
	}
}

func TestDiscoverPropagatesAdapterError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fa := newFakeAdapter()
	fa.versionErr = errors.New("boom")
	if _, err := Discover(context.Background(), fa, t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}
