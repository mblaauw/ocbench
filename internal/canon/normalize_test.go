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
	out, err := NormalizePaths(v,
		PathPrefix{From: home, To: "~"},
		PathPrefix{From: runDir, To: "<run-dir>"},
	)
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

func TestNormalizePathsLongestPrefixWins(t *testing.T) {
	home := "/Users/mich"
	runDir := "/Users/mich/.local/share/ocbench/runs/6f1e-uuid"
	wt := "/Users/mich/.cache/ocbench/worktrees/6f1e-uuid"
	v := map[string]any{
		"run_artifact": runDir + "/events.jsonl",
		"worktree":     wt + "/src/main.py",
		"home_file":    home + "/.config/opencode/opencode.json",
		"exact_run":    runDir,
		"exact_home":   home,
	}
	out, err := NormalizePaths(v,
		PathPrefix{From: home, To: "~"},
		PathPrefix{From: runDir, To: "<run-dir>"},
		PathPrefix{From: wt, To: "<worktree>"},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["run_artifact"] != "<run-dir>/events.jsonl" {
		t.Fatalf("run dir shadowed by home: %v", m["run_artifact"])
	}
	if m["worktree"] != "<worktree>/src/main.py" {
		t.Fatalf("worktree = %v", m["worktree"])
	}
	if m["home_file"] != "~/.config/opencode/opencode.json" {
		t.Fatalf("home = %v", m["home_file"])
	}
	if m["exact_run"] != "<run-dir>" || m["exact_home"] != "~" {
		t.Fatalf("exact matches: %v %v", m["exact_run"], m["exact_home"])
	}
}

func TestNormalizePathsIsIdempotentAndNonMutating(t *testing.T) {
	home := "/Users/mich"
	original := map[string]any{"p": "/Users/mich/x", "n": 1}
	out, err := NormalizePaths(original, PathPrefix{From: home, To: "~"})
	if err != nil {
		t.Fatal(err)
	}
	if original["p"] != "/Users/mich/x" {
		t.Fatal("input mutated")
	}
	again, err := NormalizePaths(out, PathPrefix{From: home, To: "~"})
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(out)
	b2, _ := json.Marshal(again)
	if string(b1) != string(b2) {
		t.Fatalf("not idempotent: %s vs %s", b1, b2)
	}
}

func TestNormalizePathsEmptyPrefixesAreNoOps(t *testing.T) {
	v := map[string]any{"a": "/x/y"}
	out, err := NormalizePaths(v, PathPrefix{From: "", To: "~"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"a":"/x/y"}` {
		t.Fatalf("out = %s", b)
	}
}
