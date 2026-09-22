package canon

import (
	"encoding/json"
	"testing"
)

func TestNormalizePaths(t *testing.T) {
	home := "/Users/mich"
	runDir := "/tmp/ocbench/run-1"
	v := map[string]any{
		"skill":    "/Users/mich/.agents/skills/ruff/SKILL.md",
		"run":      "/tmp/ocbench/run-1/worktree/src/a.py",
		"other":    "/etc/hosts",
		"homeish":  "/Users/michelle/x",
		"embedded": "see /Users/mich/.config for details",
	}
	out, err := NormalizePaths(v, home, runDir)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["skill"] != "~/.agents/skills/ruff/SKILL.md" {
		t.Fatalf("skill = %v", m["skill"])
	}
	if m["run"] != "<run-dir>/worktree/src/a.py" {
		t.Fatalf("run = %v", m["run"])
	}
	if m["other"] != "/etc/hosts" {
		t.Fatalf("other = %v", m["other"])
	}
	if m["homeish"] != "/Users/michelle/x" {
		t.Fatalf("prefix over-matched: %v", m["homeish"])
	}
	if m["embedded"] != "see /Users/mich/.config for details" {
		t.Fatalf("embedded path should be untouched, got %v", m["embedded"])
	}
}

func TestNormalizePathsEmptyInputsAreNoOps(t *testing.T) {
	v := map[string]any{"a": "/x/y"}
	out, err := NormalizePaths(v, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"a":"/x/y"}` {
		t.Fatalf("out = %s", b)
	}
}
