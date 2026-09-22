package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestAdapter builds a Real that impersonates opencode with the test binary
// itself. All subprocesses are the helper process; the real binary is never
// invoked.
func newTestAdapter(t *testing.T, timeout time.Duration, mode string) *Real {
	t.Helper()
	env := helperEnv()
	if mode != "" {
		env = append(env, "FAKE_MODE="+mode)
	}
	return NewReal(Options{
		Bin:        os.Args[0],
		Timeout:    timeout,
		Env:        env,
		TestPrefix: []string{"-test.run=TestHelperProcess", "--"},
	})
}

func TestRealVersion(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.18.32" {
		t.Fatalf("version = %q", got)
	}
}

func TestRealResolvedConfig(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.ResolvedConfig(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) {
		t.Fatalf("not valid JSON: %s", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["default_agent"] != "build" {
		t.Fatalf("default_agent = %v", decoded["default_agent"])
	}
}

func TestRealResolvedConfigInvalidJSON(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "garbage")
	_, err := a.ResolvedConfig(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestRealSkills(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.Skills(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("skills = %+v", got)
	}
	if got[0].Name != "ruff" || got[0].Description != "lint" || got[0].Content != "# ruff" {
		t.Fatalf("skill = %+v", got[0])
	}
}

func TestRealSkillsEmpty(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "empty")
	got, err := a.Skills(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("skills = %+v", got)
	}
}

func TestRealAgent(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.Agent(context.Background(), "", "build")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "build" || got.Mode != "subagent" || got.Native {
		t.Fatalf("agent = %+v", got)
	}
	if got.Steps == nil || *got.Steps != 20 {
		t.Fatalf("steps = %v", got.Steps)
	}
	if !strings.Contains(string(got.Tools), "read") {
		t.Fatalf("tools = %s", got.Tools)
	}
	if !json.Valid(got.Raw) {
		t.Fatalf("raw = %s", got.Raw)
	}
}

func TestRealMCPStatus(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.MCPStatus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("servers = %+v", got)
	}
	if got[0].Name != "gitlab" || got[0].Enabled || got[0].Target != "https://mcp.example/v2/mcp" {
		t.Fatalf("server = %+v", got[0])
	}
}

func TestRealExitError(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "fail")
	_, err := a.MCPStatus(context.Background(), "")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if exitErr.Code != 3 {
		t.Fatalf("code = %d", exitErr.Code)
	}
	if len(exitErr.Args) == 0 || exitErr.Args[0] != "mcp" {
		t.Fatalf("args = %v", exitErr.Args)
	}
}

func TestRealTimeout(t *testing.T) {
	a := newTestAdapter(t, 200*time.Millisecond, "sleep")
	start := time.Now()
	_, err := a.Version(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), "200ms") {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}
