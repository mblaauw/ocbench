package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSpec(t *testing.T, spec ValidatorSpec, vctx ValidatorContext) ValidationResult {
	t.Helper()
	return RunValidatorContext(context.Background(), 1, spec, vctx, nil, 0)
}

func TestDiffValidatorRequiredPaths(t *testing.T) {
	vctx := ValidatorContext{Changed: []string{"src/parser.py", "README.md"}}

	ok := runSpec(t, ValidatorSpec{Kind: "diff", Name: "touched parser", RequiredPaths: []string{"src/**"}}, vctx)
	if ok.Status != "passed" {
		t.Fatalf("status = %q, want passed: %s", ok.Status, ok.Output)
	}

	bad := runSpec(t, ValidatorSpec{Kind: "diff", Name: "touched docs", RequiredPaths: []string{"docs/**"}}, vctx)
	if bad.Status != "failed" {
		t.Fatalf("status = %q, want failed", bad.Status)
	}
	if !strings.Contains(bad.Output, "docs/**") {
		t.Errorf("output %q does not name the unmatched glob", bad.Output)
	}
}

func TestDiffValidatorForbiddenPaths(t *testing.T) {
	vctx := ValidatorContext{Changed: []string{"src/parser.py", "tests/test_parser.py"}}

	bad := runSpec(t, ValidatorSpec{Kind: "diff", Name: "left tests alone", ForbiddenPaths: []string{"tests/**"}}, vctx)
	if bad.Status != "failed" {
		t.Fatalf("status = %q, want failed", bad.Status)
	}
	if !strings.Contains(bad.Output, "tests/test_parser.py") {
		t.Errorf("output %q does not name the offending path", bad.Output)
	}

	ok := runSpec(t, ValidatorSpec{Kind: "diff", Name: "left config alone", ForbiddenPaths: []string{"config/**"}}, vctx)
	if ok.Status != "passed" {
		t.Fatalf("status = %q, want passed: %s", ok.Status, ok.Output)
	}
}

func TestDiffValidatorMaxLines(t *testing.T) {
	// `--- ` and `+++ ` are file headers; a file whose own content begins with
	// plus signs appears in the diff as `++++`, which is content and must count.
	diff := []byte("--- a/x\n+++ b/x\n@@\n-old\n+new\n+extra\n++++ content that begins with plus signs\n")
	added, removed := DiffLineCounts(diff)
	if added != 3 || removed != 1 {
		t.Fatalf("DiffLineCounts = %d/%d, want 3/1 (the `++++ content` line is content)", added, removed)
	}

	over := runSpec(t, ValidatorSpec{Kind: "diff", Name: "small change", MaxLines: 2}, ValidatorContext{Diff: diff})
	if over.Status != "failed" {
		t.Fatalf("status = %q, want failed", over.Status)
	}
	if !strings.Contains(over.Output, "ceiling is 2") {
		t.Errorf("output %q does not state the ceiling", over.Output)
	}

	under := runSpec(t, ValidatorSpec{Kind: "diff", Name: "small change", MaxLines: 4}, ValidatorContext{Diff: diff})
	if under.Status != "passed" {
		t.Fatalf("status = %q, want passed: %s", under.Status, under.Output)
	}
}

func TestGrepValidatorPresentAndAbsent(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"src/app.py": "def run():\n    return 1\n",
		"notes.md":   "TODO: tidy this up\n",
	})

	present := runSpec(t, ValidatorSpec{Kind: "grep", Name: "has run", Present: []string{`def run\(`}},
		ValidatorContext{Worktree: dir})
	if present.Status != "passed" {
		t.Fatalf("present status = %q, want passed: %s", present.Status, present.Output)
	}

	missing := runSpec(t, ValidatorSpec{Kind: "grep", Name: "has stop", Present: []string{`def stop\(`}},
		ValidatorContext{Worktree: dir})
	if missing.Status != "failed" {
		t.Fatalf("missing status = %q, want failed", missing.Status)
	}

	absent := runSpec(t, ValidatorSpec{Kind: "grep", Name: "no todos", Absent: []string{"TODO"}},
		ValidatorContext{Worktree: dir})
	if absent.Status != "failed" {
		t.Fatalf("absent status = %q, want failed", absent.Status)
	}
	if !strings.Contains(absent.Output, "notes.md") {
		t.Errorf("output %q does not name the matching file", absent.Output)
	}
}

func TestGrepValidatorSkipsGitAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"src/app.py":          "clean\n",
		".git/COMMIT_EDITMSG": "FORBIDDEN-IN-GIT\n",
		"big.txt":             strings.Repeat("x", 0), // replaced below
	})
	// A file above the ceiling must be skipped rather than read.
	big := strings.Repeat("y", maxGrepFileBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	git := runSpec(t, ValidatorSpec{Kind: "grep", Name: "no git hits", Absent: []string{"FORBIDDEN-IN-GIT"}},
		ValidatorContext{Worktree: dir})
	if git.Status != "passed" {
		t.Fatalf("status = %q, want passed (the .git directory must be skipped): %s", git.Status, git.Output)
	}

	oversized := runSpec(t, ValidatorSpec{Kind: "grep", Name: "big skipped", Absent: []string{"yyy"}},
		ValidatorContext{Worktree: dir})
	if oversized.Status != "passed" {
		t.Fatalf("status = %q, want passed (an oversized file must be skipped): %s", oversized.Status, oversized.Output)
	}
}

func TestGrepValidatorInvalidPatternIsError(t *testing.T) {
	res := runSpec(t, ValidatorSpec{Kind: "grep", Name: "bad", Present: []string{"("}},
		ValidatorContext{Worktree: t.TempDir()})
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
}

func TestProcessValidatorMatchesToolCalls(t *testing.T) {
	events := parseEvents(t,
		`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"python3 -m unittest discover -s tests"}}}}`,
		`{"type":"tool_use","part":{"type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"src/app.py"}}}}`,
	)

	ok := runSpec(t, ValidatorSpec{Kind: "process", Name: "ran tests", ToolPattern: `unittest|pytest`},
		ValidatorContext{Events: events})
	if ok.Status != "passed" {
		t.Fatalf("status = %q, want passed: %s", ok.Status, ok.Output)
	}

	bad := runSpec(t, ValidatorSpec{Kind: "process", Name: "ran tests", ToolPattern: `go test`},
		ValidatorContext{Events: events})
	if bad.Status != "failed" {
		t.Fatalf("status = %q, want failed", bad.Status)
	}
	if !strings.Contains(bad.Output, "python3 -m unittest") {
		t.Errorf("output %q should list the tool calls that were seen", bad.Output)
	}
}

func TestProcessValidatorBadSpecIsError(t *testing.T) {
	if res := runSpec(t, ValidatorSpec{Kind: "process", Name: "x"}, ValidatorContext{}); res.Status != "error" {
		t.Errorf("empty tool_pattern: status = %q, want error", res.Status)
	}
	if res := runSpec(t, ValidatorSpec{Kind: "process", Name: "x", ToolPattern: "("}, ValidatorContext{}); res.Status != "error" {
		t.Errorf("invalid tool_pattern: status = %q, want error", res.Status)
	}
}

func TestUnknownKindIsError(t *testing.T) {
	res := runSpec(t, ValidatorSpec{Kind: "telepathy", Name: "x"}, ValidatorContext{})
	if res.Status != "error" || !strings.Contains(res.Output, "telepathy") {
		t.Fatalf("status = %q output = %q", res.Status, res.Output)
	}
}

func TestRunValidatorWrapperStillWorks(t *testing.T) {
	// The legacy entry point must keep behaving as a command/answer runner.
	res := RunValidator(context.Background(), 1,
		ValidatorSpec{Kind: "answer", Name: "cause", Patterns: []string{"off-by-one"}},
		"", nil, 0, "the bug is an off-by-one in the loop")
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed", res.Status)
	}
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func parseEvents(t *testing.T, lines ...string) []Event {
	t.Helper()
	out := make([]Event, 0, len(lines))
	for _, l := range lines {
		e, err := ParseLine([]byte(l))
		if err != nil {
			t.Fatalf("ParseLine(%s): %v", l, err)
		}
		out = append(out, e)
	}
	return out
}

func TestGrepValidatorSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"src/app.py": "clean\n"})
	// A bytecode-like artefact containing a tab must not be read as text.
	pyc := append([]byte("\x00\x00\x00\x00"), []byte("\tTAB-IN-BINARY\n")...)
	if err := os.WriteFile(filepath.Join(dir, "app.pyc"), pyc, 0o644); err != nil {
		t.Fatal(err)
	}
	res := runSpec(t, ValidatorSpec{Kind: "grep", Name: "no tabs", Absent: []string{"\t"}},
		ValidatorContext{Worktree: dir})
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (binary files must be skipped): %s", res.Status, res.Output)
	}
}
