package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// testGit runs git in dir (empty dir for commands that create the repo) and
// fails the test on any error. It is independent of the production git helper
// so the tests exercise the real command-line behaviour.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	argv := args
	if dir != "" {
		argv = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", argv...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(argv, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// testFixtureHash is a valid content hash: exactly 64 lowercase hex chars.
const testFixtureHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testFixture is a small nested fixture tree built in-test.
func testFixture() fstest.MapFS {
	return fstest.MapFS{
		"README.md":          {Data: []byte("# fixture\n")},
		"src/main.go":        {Data: []byte("package main\n")},
		"src/util/helper.go": {Data: []byte("package util\n")},
	}
}

func TestMaterializeFixtureDeterministicAcrossRoots(t *testing.T) {
	ctx := context.Background()
	const hash = testFixtureHash
	rootA, rootB := t.TempDir(), t.TempDir()

	a, err := MaterializeFixture(ctx, rootA, testFixture(), hash)
	if err != nil {
		t.Fatalf("MaterializeFixture(rootA): %v", err)
	}
	b, err := MaterializeFixture(ctx, rootB, testFixture(), hash)
	if err != nil {
		t.Fatalf("MaterializeFixture(rootB): %v", err)
	}

	if a.SHA != b.SHA {
		t.Fatalf("baseline SHA differs across cache roots: %s != %s", a.SHA, b.SHA)
	}
	if want := filepath.Join(rootA, "fixtures", hash); a.RepoDir != want {
		t.Errorf("RepoDir = %q, want %q", a.RepoDir, want)
	}
	if !isHexSHA(a.SHA) {
		t.Errorf("SHA %q is not a hex commit id", a.SHA)
	}
	if got := testGit(t, a.RepoDir, "rev-parse", "--show-object-format"); got != "sha1" {
		t.Errorf("baseline object format = %q, want sha1 (host object format must not leak in)", got)
	}

	// Determinism proof: both SHAs and the fixed commit metadata.
	t.Logf("root A: %s -> %s", a.RepoDir, a.SHA)
	t.Logf("root B: %s -> %s", b.RepoDir, b.SHA)
	body := testGit(t, a.RepoDir, "cat-file", "-p", "HEAD")
	t.Logf("git cat-file -p HEAD:\n%s", body)
	for _, want := range []string{
		"author ocbench <ocbench@localhost> 946684800 +0000",
		"committer ocbench <ocbench@localhost> 946684800 +0000",
		"ocbench fixture baseline",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("commit object missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "gpgsig") {
		t.Errorf("commit is signed:\n%s", body)
	}
}

func TestMaterializeFixtureRejectsInvalidHash(t *testing.T) {
	ctx := context.Background()
	invalid := []string{
		".",
		"..",
		"abc",
		"",
		"..%2f..",
		"0123456789abcdef0123456789abcdef/0123456789abcdef0123456789abcdef",
		strings.ToUpper(testFixtureHash),
	}
	for _, hash := range invalid {
		cache := t.TempDir()
		if _, err := MaterializeFixture(ctx, cache, testFixture(), hash); err == nil {
			t.Errorf("MaterializeFixture accepted invalid hash %q", hash)
			continue
		}
		if entries, err := os.ReadDir(cache); err != nil {
			t.Fatalf("read cache %s: %v", cache, err)
		} else if len(entries) != 0 {
			t.Errorf("invalid hash %q modified the cache: %v", hash, entries)
		}
	}

	// The valid shape must still be accepted and materialise a repo.
	valid, err := MaterializeFixture(ctx, t.TempDir(), testFixture(), testFixtureHash)
	if err != nil {
		t.Fatalf("MaterializeFixture(valid hash): %v", err)
	}
	if !isHexSHA(valid.SHA) {
		t.Errorf("valid hash produced non-hex SHA %q", valid.SHA)
	}
}

func TestMaterializeFixtureReusesExistingRepo(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	first, err := MaterializeFixture(ctx, root, testFixture(), testFixtureHash)
	if err != nil {
		t.Fatalf("first MaterializeFixture: %v", err)
	}
	second, err := MaterializeFixture(ctx, root, testFixture(), testFixtureHash)
	if err != nil {
		t.Fatalf("second MaterializeFixture: %v", err)
	}
	if first.SHA != second.SHA {
		t.Fatalf("reused SHA %s != %s", second.SHA, first.SHA)
	}
	if got := testGit(t, first.RepoDir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("rev-list --count HEAD = %q, want 1 (a second commit was created)", got)
	}
}

func TestMaterializeFixtureConcurrent(t *testing.T) {
	const n = 8
	root := t.TempDir()
	results := make([]Baseline, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = MaterializeFixture(context.Background(), root, testFixture(), testFixtureHash)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if results[i].SHA != results[0].SHA {
			t.Fatalf("goroutine %d SHA %s != %s", i, results[i].SHA, results[0].SHA)
		}
	}
	if got := testGit(t, results[0].RepoDir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("rev-list --count HEAD = %q, want 1", got)
	}
	if _, err := os.Stat(results[0].RepoDir + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestMaterializeRepoResolvesLocalRef(t *testing.T) {
	src := t.TempDir()
	testGit(t, src, "init", "-q", "-b", "main")
	testGit(t, src, "config", "user.email", "test@example.com")
	testGit(t, src, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", "-A")
	testGit(t, src, "commit", "-q", "-m", "base")
	want := testGit(t, src, "rev-parse", "HEAD")
	testGit(t, src, "tag", "v1")

	b, err := MaterializeRepo(context.Background(), t.TempDir(), src, "v1")
	if err != nil {
		t.Fatalf("MaterializeRepo: %v", err)
	}
	if b.SHA != want {
		t.Errorf("SHA = %s, want %s", b.SHA, want)
	}
	if b.RepoDir != src {
		t.Errorf("RepoDir = %q, want %q", b.RepoDir, src)
	}
}

func TestMaterializeRepoUnknownRefNamesRef(t *testing.T) {
	src := t.TempDir()
	testGit(t, src, "init", "-q", "-b", "main")
	testGit(t, src, "config", "user.email", "test@example.com")
	testGit(t, src, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", "-A")
	testGit(t, src, "commit", "-q", "-m", "base")

	_, err := MaterializeRepo(context.Background(), t.TempDir(), src, "does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unknown ref")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q does not name the ref", err)
	}
}

func TestMaterializeRepoRejectsUnknownSource(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	_, err := MaterializeRepo(context.Background(), t.TempDir(), missing, "HEAD")
	if err == nil {
		t.Fatal("expected an error for a missing source")
	}
}
