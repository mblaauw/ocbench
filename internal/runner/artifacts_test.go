package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiffLineCountsHeaderVsContent(t *testing.T) {
	// A genuine content line that begins "+++" is emitted by git with a leading
	// "+", i.e. "++++ ..."; likewise "---" content becomes "---- ...". Only the
	// unified-diff file headers ("+++ "/"--- ") may be skipped.
	diff := []byte(strings.Join([]string{
		"diff --git a/foo b/foo",
		"--- a/foo",
		"+++ b/foo",
		"@@ -1,2 +1,4 @@",
		" context",
		"-removed",
		"+added",
		"++++ content starting with +++",
		"---- content starting with ---",
		"+++",
		"---",
	}, "\n"))

	added, removed := diffLineCounts(diff)
	if added != 3 {
		t.Errorf("added = %d, want 3", added)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}
}

func TestMatchGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Ordinary segment globs: * and ? never cross a separator.
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/a/main.go", false},
		{"*.go", "main.go", true},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"[ab].go", "a.go", true},
		{"[ab].go", "c.go", false},
		// ** matches zero or more whole segments.
		{"**", "a/b/c", true},
		{"a/**", "a", true},
		{"a/**", "a/b/c", true},
		{"**/x.go", "x.go", true},
		{"**/x.go", "a/b/x.go", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/b/c", false},
		// Malformed patterns are non-matches (path.Match error), not panics.
		{"[", "a", false},
		{"src/[", "src/a", false},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestMatchAnyMalformedPatternIsNonMatch(t *testing.T) {
	if matchAny([]string{"["}, "a") {
		t.Error("matchAny with a malformed pattern = true, want false")
	}
	if !matchAny([]string{"[", "a"}, "a") {
		t.Error("a malformed pattern must not mask a later valid match")
	}
}

func TestSafeUntrackedRel(t *testing.T) {
	cases := map[string]bool{
		"a.txt":        true,
		"sub/a.txt":    true,
		"..foo":        true,
		"a..b/c":       true,
		"":             false,
		"..":           false,
		"../evil":      false,
		"a/../../evil": false,
		"/abs":         false,
	}
	for rel, want := range cases {
		if got := safeUntrackedRel(rel); got != want {
			t.Errorf("safeUntrackedRel(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestCaptureUntrackedRegularSymlinkAndTraversal(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	testGit(t, repo, "config", "user.email", "test@example.com")
	testGit(t, repo, "config", "user.name", "test")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "tracked\n")
	testGit(t, repo, "add", "-A")
	testGit(t, repo, "commit", "-q", "-m", "base")

	writeFile(t, filepath.Join(repo, "regular.txt"), "regular untracked\n")
	writeFile(t, filepath.Join(repo, "sub", "nested.txt"), "nested untracked\n")

	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "outside secret\n")
	if err := os.Symlink(outside, filepath.Join(repo, "escape-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	runDir := t.TempDir()
	got, err := captureUntracked(ctx, runDir, repo)
	if err != nil {
		t.Fatalf("captureUntracked: %v", err)
	}
	want := []string{"regular.txt", "sub/nested.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("captured = %v, want %v", got, want)
	}

	if data, err := os.ReadFile(filepath.Join(runDir, "untracked", "regular.txt")); err != nil {
		t.Fatalf("read regular capture: %v", err)
	} else if string(data) != "regular untracked\n" {
		t.Errorf("regular bytes = %q", data)
	}
	if _, err := os.Lstat(filepath.Join(runDir, "untracked", "escape-link")); !os.IsNotExist(err) {
		t.Errorf("symlink was copied into untracked/: %v", err)
	}

	// Nothing may escape the destination: every captured path is contained.
	untrackedRoot := filepath.Join(runDir, "untracked")
	if err := filepath.WalkDir(untrackedRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel, err := filepath.Rel(runDir, p); err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("artifact path escapes runDir: %q (rel %q)", p, rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk untracked: %v", err)
	}
}
