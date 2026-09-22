// Package runner materialises deterministic git baselines and disposable
// worktrees for benchmark tasks (spec §7–§8). Every fixture normalises to
// source → immutable commit SHA → temporary worktree.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mbl/ocbench/internal/canon"
)

// Baseline is an immutable git revision plus the local repository that holds
// it. RepoDir is a real (non-bare) repository, so worktrees can be added to it.
type Baseline struct {
	RepoDir string // local git repo holding the baseline commit
	SHA     string
}

const (
	// fixtureCommitMessage is fixed so identical fixture trees produce the
	// identical commit SHA on every machine.
	fixtureCommitMessage = "ocbench fixture baseline"

	lockPollInitial = 5 * time.Millisecond
	lockPollMax     = 100 * time.Millisecond
	lockWaitMax     = 30 * time.Second
	gitErrExcerpt   = 512
)

// commitIdentity pins author/committer identity and dates. Together with a
// fixed message, a fixed file mode (0644) and lexical traversal this makes the
// baseline commit independent of the cache root, wall clock and user config.
var commitIdentity = []string{
	"GIT_AUTHOR_NAME=ocbench",
	"GIT_AUTHOR_EMAIL=ocbench@localhost",
	"GIT_COMMITTER_NAME=ocbench",
	"GIT_COMMITTER_EMAIL=ocbench@localhost",
	"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
}

// deterministicGitFlags disables signing and CRLF rewriting for the commands
// that create the baseline commit.
var deterministicGitFlags = []string{
	"-c", "commit.gpgsign=false",
	"-c", "core.autocrlf=false",
}

// MaterializeFixture writes an embedded fixture into the cache as a real git
// repo with one deterministic commit and returns it. Idempotent per
// fixtureHash: an existing repo with a commit is reused and its HEAD SHA read
// back. Creation is guarded by a lock file so concurrent calls cannot corrupt
// the cache.
func MaterializeFixture(ctx context.Context, cacheDir string, fixture fs.FS, fixtureHash string) (Baseline, error) {
	if fixture == nil {
		return Baseline{}, errors.New("materialize fixture: nil fixture")
	}
	if cacheDir == "" {
		return Baseline{}, errors.New("materialize fixture: empty cache dir")
	}
	if fixtureHash == "" || strings.ContainsAny(fixtureHash, `/\`) {
		return Baseline{}, fmt.Errorf("materialize fixture: invalid fixture hash %q", fixtureHash)
	}

	repoDir := filepath.Join(cacheDir, "fixtures", fixtureHash)
	if sha, ok := baselineSHA(ctx, repoDir); ok {
		return Baseline{RepoDir: repoDir, SHA: sha}, nil
	}
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}

	unlock, err := acquireLock(ctx, repoDir+".lock")
	if err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}
	defer unlock()

	// Another process may have completed the repo while we waited for the lock.
	if sha, ok := baselineSHA(ctx, repoDir); ok {
		return Baseline{RepoDir: repoDir, SHA: sha}, nil
	}
	// Clear anything left by a crashed materialisation so `git add -A` cannot
	// pick up stray files and change the resulting tree.
	if err := os.RemoveAll(repoDir); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: clear stale dir: %w", fixtureHash, err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}
	if _, err := runGit(ctx, repoDir, nil, "init", "-q", "-b", "main"); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}
	if err := writeFixture(fixture, repoDir); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: write fixture: %w", fixtureHash, err)
	}
	if _, err := runGit(ctx, repoDir, nil, append(append([]string{}, deterministicGitFlags...), "add", "-A", "--")...); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}
	commitArgs := append(append([]string{}, deterministicGitFlags...), "commit", "-q", "-m", fixtureCommitMessage)
	if _, err := runGit(ctx, repoDir, gitEnv(commitIdentity...), commitArgs...); err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}

	out, err := runGit(ctx, repoDir, nil, "rev-parse", "HEAD")
	if err != nil {
		return Baseline{}, fmt.Errorf("materialize fixture %s: %w", fixtureHash, err)
	}
	sha := strings.TrimSpace(string(out))
	if !isHexSHA(sha) {
		return Baseline{}, fmt.Errorf("materialize fixture %s: HEAD is not a commit id: %q", fixtureHash, sha)
	}
	return Baseline{RepoDir: repoDir, SHA: sha}, nil
}

// MaterializeRepo normalises an external git source (an existing local path or
// a URL, plus a ref) to a local repo and an immutable commit SHA. Local sources
// are used in place; URLs are cached as a clone under <cacheDir>/repos/<hash>.
// An unknown ref is a named error.
func MaterializeRepo(ctx context.Context, cacheDir, source, ref string) (Baseline, error) {
	if source == "" {
		return Baseline{}, errors.New("materialize repo: empty source")
	}
	if ref == "" {
		ref = "HEAD"
	}
	repoDir, err := resolveSourceRepo(ctx, cacheDir, source)
	if err != nil {
		return Baseline{}, err
	}
	out, err := runGit(ctx, repoDir, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return Baseline{}, fmt.Errorf("materialize repo %q: unknown ref %q: %w", source, ref, err)
	}
	sha := strings.TrimSpace(string(out))
	if !isHexSHA(sha) {
		return Baseline{}, fmt.Errorf("materialize repo %q: ref %q did not resolve to a commit", source, ref)
	}
	return Baseline{RepoDir: repoDir, SHA: sha}, nil
}

// resolveSourceRepo maps source to a local repository directory. A path that
// exists is used directly; otherwise source must look like a URL and is cloned
// into (or refreshed under) the cache.
func resolveSourceRepo(ctx context.Context, cacheDir, source string) (string, error) {
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		abs, err := filepath.Abs(source)
		if err != nil {
			return "", fmt.Errorf("materialize repo %q: %w", source, err)
		}
		return abs, nil
	}
	if !looksLikeURL(source) {
		return "", fmt.Errorf("materialize repo %q: not a local directory or URL", source)
	}
	if cacheDir == "" {
		return "", fmt.Errorf("materialize repo %q: empty cache dir", source)
	}
	dest := filepath.Join(cacheDir, "repos", canon.SHA256Hex([]byte(source)))
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		if _, err := runGit(ctx, dest, nil, "fetch", "--quiet", "--tags", "--force", "origin"); err != nil {
			return "", fmt.Errorf("materialize repo %q: refresh cached clone: %w", source, err)
		}
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("materialize repo %q: %w", source, err)
	}
	if _, err := runGit(ctx, "", nil, "clone", "--quiet", "--no-checkout", source, dest); err != nil {
		return "", fmt.Errorf("materialize repo %q: %w", source, err)
	}
	return dest, nil
}

// baselineSHA reports the HEAD commit of an already-materialised repo. It is
// false when repoDir is not a repo, has no .git, or has no commit yet.
func baselineSHA(ctx context.Context, repoDir string) (string, bool) {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return "", false
	}
	out, err := runGit(ctx, repoDir, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(out))
	if !isHexSHA(sha) {
		return "", false
	}
	return sha, true
}

// writeFixture copies every regular file of fsys into dest, creating
// directories with 0755 and files with 0644. Traversal is lexical
// (fs.WalkDir), which is required for a deterministic tree.
func writeFixture(fsys fs.FS, dest string) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !fs.ValidPath(p) {
			return fmt.Errorf("invalid fixture path %q", p)
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// acquireLock creates lockPath with O_CREATE|O_EXCL, retrying with bounded
// exponential backoff until it succeeds, the context is cancelled, or the wait
// budget is exhausted. The returned release function removes the lock.
func acquireLock(ctx context.Context, lockPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWaitMax)
	backoff := lockPollInitial
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for lock %s", lockPath)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for lock %s: %w", lockPath, ctx.Err())
		case <-time.After(backoff):
		}
		if backoff < lockPollMax {
			backoff *= 2
			if backoff > lockPollMax {
				backoff = lockPollMax
			}
		}
	}
}

// runGit invokes git, prefixing `-C dir` when dir is non-empty and running with
// a GIT_* sanitised environment. It returns stdout on success and a wrapped
// error carrying a bounded stderr excerpt on failure.
func runGit(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	argv := args
	if dir != "" {
		argv = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", argv...)
	if env == nil {
		env = gitEnv()
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > gitErrExcerpt {
			msg = msg[:gitErrExcerpt]
		}
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}

// gitEnv returns os.Environ with inherited GIT_* variables removed (they could
// redirect git to an unrelated repository or config), plus extra appended.
func gitEnv(extra ...string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+len(extra)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	return append(env, extra...)
}

// looksLikeURL reports whether source is a plausible remote git URL (scheme://
// or scp-like host:path).
func looksLikeURL(source string) bool {
	if strings.Contains(source, "://") {
		return true
	}
	if at := strings.IndexByte(source, '@'); at > 0 {
		return strings.Contains(source[at+1:], ":")
	}
	return false
}

// isHexSHA reports whether s is a 40- or 64-character hex object id.
func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
