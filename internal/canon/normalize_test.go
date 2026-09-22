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
	worktree := "/Users/mich/.cache/ocbench/worktrees/6f1e-uuid"
	v := map[string]any{
		"run":       runDir + "/events.jsonl",
		"worktree":  worktree + "/src/main.py",
		"home":      home + "/.config/opencode/opencode.json",
		"runExact":  runDir,
		"homeExact": home,
	}
	out, err := NormalizePaths(v,
		PathPrefix{From: home, To: "~"},
		PathPrefix{From: runDir, To: "<run-dir>"},
		PathPrefix{From: worktree, To: "<worktree>"},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["run"] != "<run-dir>/events.jsonl" {
		t.Fatalf("run = %v", m["run"])
	}
	if m["worktree"] != "<worktree>/src/main.py" {
		t.Fatalf("worktree = %v", m["worktree"])
	}
	if m["home"] != "~/.config/opencode/opencode.json" {
		t.Fatalf("home = %v", m["home"])
	}
	if m["runExact"] != "<run-dir>" {
		t.Fatalf("runExact = %v", m["runExact"])
	}
	if m["homeExact"] != "~" {
		t.Fatalf("homeExact = %v", m["homeExact"])
	}
}

func TestNormalizePathsIsIdempotentAndNonMutating(t *testing.T) {
	home := "/Users/mich"
	runDir := "/Users/mich/.local/share/ocbench/runs/6f1e-uuid"
	prefixes := []PathPrefix{
		{From: home, To: "~"},
		{From: runDir, To: "<run-dir>"},
	}
	v := map[string]any{
		"run":  runDir + "/events.jsonl",
		"home": home + "/.config/opencode/opencode.json",
	}
	before, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := NormalizePaths(v, prefixes...)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("input mutated: %s -> %s", before, after)
	}
	again, err := NormalizePaths(out, prefixes...)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
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
