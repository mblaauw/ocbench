package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CreateWorktree adds a detached worktree of the baseline commit at dest. Any
// missing parent directories of dest are created; an existing dest is refused
// by git rather than overwritten.
func CreateWorktree(ctx context.Context, b Baseline, dest string) error {
	if err := validateBaseline(b); err != nil {
		return err
	}
	if dest == "" {
		return errors.New("create worktree: empty destination")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create worktree %s: %w", dest, err)
	}
	if _, err := runGit(ctx, b.RepoDir, nil, "worktree", "add", "--detach", dest, b.SHA); err != nil {
		return fmt.Errorf("create worktree %s: %w", dest, err)
	}
	return nil
}

// RemoveWorktree removes the worktree at dest with `git worktree remove
// --force` and then prunes stale metadata. It tolerates an already-absent
// destination: a missing directory is only pruned, never an error, so callers
// may unconditionally clean up.
func RemoveWorktree(ctx context.Context, b Baseline, dest string) error {
	if err := validateBaseline(b); err != nil {
		return err
	}
	if dest == "" {
		return errors.New("remove worktree: empty destination")
	}
	if _, err := os.Stat(dest); err == nil {
		if _, err := runGit(ctx, b.RepoDir, nil, "worktree", "remove", "--force", dest); err != nil {
			return fmt.Errorf("remove worktree %s: %w", dest, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove worktree %s: %w", dest, err)
	}
	if _, err := runGit(ctx, b.RepoDir, nil, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktrees %s: %w", b.RepoDir, err)
	}
	return nil
}

// ChangedFiles lists every path whose content differs from baselineSHA,
// relative to the worktree root and sorted. It unions `git status --porcelain
// -z` (uncommitted modifications, untracked files, staged renames) with the
// name-status diff against baselineSHA, so changes the agent committed inside
// the worktree are still detected even though `git status` reports them clean.
// Rename and copy entries contribute both of their paths.
func ChangedFiles(ctx context.Context, worktree, baselineSHA string) ([]string, error) {
	if worktree == "" {
		return nil, errors.New("changed files: empty worktree")
	}
	if baselineSHA == "" {
		return nil, errors.New("changed files: empty baseline sha")
	}
	status, err := runGit(ctx, worktree, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("changed files %s: %w", worktree, err)
	}
	diff, err := runGit(ctx, worktree, nil, "diff", "--name-status", "-z", baselineSHA, "--")
	if err != nil {
		return nil, fmt.Errorf("changed files %s: %w", worktree, err)
	}
	paths := append(parseStatusZ(status), parseNameStatusZ(diff)...)
	sort.Strings(paths)
	return dedupeSorted(paths), nil
}

// DiffAgainstBaseline returns the tracked diff between baselineSHA and the
// worktree, as produced by `git -C <worktree> diff <sha> --`. Untracked file
// bytes are NOT included: they never appear in a git diff. Callers capture
// untracked files separately — ChangedFiles reports their paths and the runner
// stores each file under untracked/<path>.
func DiffAgainstBaseline(ctx context.Context, worktree, baselineSHA string) ([]byte, error) {
	if worktree == "" {
		return nil, errors.New("diff against baseline: empty worktree")
	}
	if baselineSHA == "" {
		return nil, errors.New("diff against baseline: empty baseline sha")
	}
	out, err := runGit(ctx, worktree, nil, "diff", baselineSHA, "--")
	if err != nil {
		return nil, fmt.Errorf("diff against baseline %s: %w", worktree, err)
	}
	return out, nil
}

// parseStatusZ parses `git status --porcelain -z` output. Each record is
// "XY path"; rename/copy records (X or Y is R/C) are followed by a second NUL
// field holding the other path. Both paths are returned.
func parseStatusZ(data []byte) []string {
	fields := bytes.Split(data, []byte{0})
	var out []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		status := string(f[:2])
		out = append(out, string(f[3:]))
		if strings.ContainsAny(status, "RC") {
			if i+1 < len(fields) && len(fields[i+1]) > 0 {
				i++
				out = append(out, string(fields[i]))
			}
		}
	}
	return out
}

// parseNameStatusZ parses `git diff --name-status -z` output. Fields alternate
// status and path; rename/copy statuses are followed by a second path. Both
// paths of a rename/copy are returned.
func parseNameStatusZ(data []byte) []string {
	fields := bytes.Split(data, []byte{0})
	var out []string
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if len(status) == 0 {
			continue
		}
		i++
		if i >= len(fields) {
			break
		}
		out = append(out, string(fields[i]))
		if status[0] == 'R' || status[0] == 'C' {
			i++
			if i < len(fields) && len(fields[i]) > 0 {
				out = append(out, string(fields[i]))
			}
		}
	}
	return out
}

// dedupeSorted removes adjacent duplicates from a sorted slice.
func dedupeSorted(in []string) []string {
	out := make([]string, 0, len(in))
	prev := ""
	for i, p := range in {
		if i > 0 && p == prev {
			continue
		}
		out = append(out, p)
		prev = p
	}
	return out
}

func validateBaseline(b Baseline) error {
	if b.RepoDir == "" {
		return errors.New("baseline: empty repo dir")
	}
	if b.SHA == "" {
		return errors.New("baseline: empty sha")
	}
	return nil
}
