package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// materializeWorktree materialises the in-test fixture and creates a detached
// worktree from it. The worktree parent is symlink-resolved so that the path
// git records matches the path the test asserts on (macOS /var -> /private/var).
func materializeWorktree(t *testing.T) (Baseline, string) {
	t.Helper()
	ctx := context.Background()
	b, err := MaterializeFixture(ctx, t.TempDir(), testFixture(), testFixtureHash)
	if err != nil {
		t.Fatalf("MaterializeFixture: %v", err)
	}
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	dest := filepath.Join(base, "worktree")
	if err := CreateWorktree(ctx, b, dest); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	return b, dest
}

func worktreeListHas(list, dest string) bool {
	for _, line := range strings.Split(list, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && p == dest {
			return true
		}
	}
	return false
}

func TestCreateWorktreeDetachedAtBaseline(t *testing.T) {
	b, wt := materializeWorktree(t)

	if got := testGit(t, wt, "rev-parse", "HEAD"); got != b.SHA {
		t.Errorf("worktree HEAD = %q, want baseline %q", got, b.SHA)
	}
	if got := testGit(t, wt, "status", "--porcelain"); got != "" {
		t.Errorf("fresh worktree status = %q, want clean", got)
	}
	list := testGit(t, b.RepoDir, "worktree", "list", "--porcelain")
	if !worktreeListHas(list, wt) {
		t.Errorf("worktree list does not include %s:\n%s", wt, list)
	}
}

func TestRemoveWorktreeRemovesAndPrunes(t *testing.T) {
	b, wt := materializeWorktree(t)

	if err := RemoveWorktree(context.Background(), b, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present: %v", err)
	}
	list := testGit(t, b.RepoDir, "worktree", "list", "--porcelain")
	if worktreeListHas(list, wt) {
		t.Errorf("worktree still registered:\n%s", list)
	}

	// A second removal of an already-absent destination must be tolerated.
	if err := RemoveWorktree(context.Background(), b, wt); err != nil {
		t.Errorf("second RemoveWorktree on absent dest: %v", err)
	}
}

func TestChangedFilesDetectsModifyUntrackedAndCommit(t *testing.T) {
	ctx := context.Background()
	b, wt := materializeWorktree(t)

	writeFile(t, filepath.Join(wt, "README.md"), "changed\n")
	writeFile(t, filepath.Join(wt, "untracked.txt"), "new\n")
	writeFile(t, filepath.Join(wt, "src", "main.go"), "package main // committed\n")

	// Commit one tracked change in the worktree: HEAD moves away from baseline,
	// so `git status` reports it clean even though it differs from baseline.
	testGit(t, wt, "add", "src/main.go")
	testGit(t, wt, "-c", "user.name=test", "-c", "user.email=test@example.com",
		"commit", "-q", "-m", "agent commit")
	if got := testGit(t, wt, "status", "--porcelain", "--", "src/main.go"); got != "" {
		t.Fatalf("committed file still shows in status: %q", got)
	}

	got, err := ChangedFiles(ctx, wt, b.SHA)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	want := []string{"README.md", "src/main.go", "untracked.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedFiles = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("ChangedFiles not sorted: %v", got)
	}
}

func TestChangedFilesReportsBothRenamePaths(t *testing.T) {
	ctx := context.Background()
	b, wt := materializeWorktree(t)
	testGit(t, wt, "mv", "README.md", "GUIDE.md")

	got, err := ChangedFiles(ctx, wt, b.SHA)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	want := []string{"GUIDE.md", "README.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedFiles = %v, want %v", got, want)
	}
}

func TestDiffAgainstBaselineExcludesUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	b, wt := materializeWorktree(t)

	writeFile(t, filepath.Join(wt, "README.md"), "changed\n")
	writeFile(t, filepath.Join(wt, "untracked.txt"), "new\n")

	diff, err := DiffAgainstBaseline(ctx, wt, b.SHA)
	if err != nil {
		t.Fatalf("DiffAgainstBaseline: %v", err)
	}
	text := string(diff)
	if !strings.Contains(text, "README.md") {
		t.Errorf("tracked diff does not mention README.md:\n%s", text)
	}
	if strings.Contains(text, "untracked.txt") {
		t.Errorf("untracked file leaked into the tracked diff:\n%s", text)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
