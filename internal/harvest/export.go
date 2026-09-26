package harvest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// ExportOptions controls an export.
type ExportOptions struct {
	// Dir is where the task directory is written.
	Dir string
	// Exclude drops paths from the fixture. A pattern matches a repository
	// path, or a directory and everything under it, so "suites" removes the
	// whole tree. It is how a fixture is cut down to the part of a repository
	// the task is actually about: the default is the entire tree, which for a
	// large repository buries the task in unrelated code the agent then reads.
	Exclude []string
}

// Export writes one candidate as a task scaffold under dir.
//
// The layout matches a suite task so the result can be moved into a suite and
// loaded unchanged:
//
//	fixture/                     the repository before the candidate's first
//	                             commit, with the changed test files at their
//	                             final content
//	evaluator/reference/<path>   the changed non-test files at their final
//	                             content
//	prompt.md                    the raw material, for a human to rewrite
//	task.yaml                    a scaffold whose validator is inferred
//
// The fixture deliberately carries the new tests: that is what makes the task
// fail before the reference is applied and pass after, which is the property
// every existing task is held to. Export checks that property before returning,
// because a task that passes untouched measures nothing.
//
// The prompt is NOT finished — a harvested user turn is usually a conversational
// continuation, so a human has to state the goal. Export returns the task
// directory it wrote and what the check found.
func Export(ctx context.Context, c Candidate, opts ExportOptions) (string, Verification, error) {
	dir := opts.Dir
	if len(c.Commits) == 0 {
		return "", Verification{}, fmt.Errorf("harvest: candidate has no commits")
	}
	if c.Repo == "" {
		return "", Verification{}, fmt.Errorf("harvest: candidate has no repository")
	}
	first, last := c.Commits[0].Hash, c.Commits[len(c.Commits)-1].Hash

	// The parent of the first commit is the state the work started from. A
	// root commit has no parent, so there is nothing to start from.
	parent, err := revParse(ctx, c.Repo, first+"^")
	if err != nil {
		return "", Verification{}, fmt.Errorf("harvest: %s is the first commit in %s, so there is no "+
			"starting state to build a fixture from", short(first), c.Repo)
	}

	changed, err := changedFiles(ctx, c.Repo, parent, last)
	if err != nil {
		return "", Verification{}, err
	}
	if len(changed) == 0 {
		return "", Verification{}, fmt.Errorf("harvest: the commits changed no files")
	}

	taskID := taskIDFor(c, first)
	dest := filepath.Join(dir, taskID)
	if _, err := os.Stat(dest); err == nil {
		return "", Verification{}, fmt.Errorf("harvest: %s already exists", dest)
	}

	fixture := filepath.Join(dest, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		return "", Verification{}, err
	}
	if err := archiveTo(ctx, c.Repo, parent, fixture); err != nil {
		return "", Verification{}, err
	}
	if err := applyExcludes(fixture, opts.Exclude); err != nil {
		return "", Verification{}, err
	}

	// Split the change set: tests become part of the fixture so the task fails
	// before the reference is applied; everything else is the reference.
	var tests, sources []string
	for _, p := range changed {
		if looksLikeTest(p) {
			tests = append(tests, p)
		} else {
			sources = append(sources, p)
		}
	}
	for _, p := range tests {
		content, err := showFile(ctx, c.Repo, last, p)
		if err != nil {
			return "", Verification{}, err
		}
		if err := writeUnder(fixture, p, content); err != nil {
			return "", Verification{}, err
		}
	}
	for _, p := range sources {
		content, err := showFile(ctx, c.Repo, last, p)
		if err != nil {
			return "", Verification{}, err
		}
		if err := writeUnder(filepath.Join(dest, "evaluator", "reference"), p, content); err != nil {
			return "", Verification{}, err
		}
	}

	command := inferTestCommand(fixture)
	if err := os.WriteFile(filepath.Join(dest, "prompt.md"),
		[]byte(promptDraft(c, tests, sources, command)), 0o644); err != nil {
		return "", Verification{}, err
	}
	if err := os.WriteFile(filepath.Join(dest, "task.yaml"),
		[]byte(taskScaffold(taskID, c, command)), 0o644); err != nil {
		return "", Verification{}, err
	}

	// Check the property before claiming it.
	verification, err := Verify(ctx, dest)
	if err != nil {
		return dest, Verification{}, err
	}
	return dest, verification, nil
}

// applyExcludes removes matching paths from an extracted fixture. A pattern
// matches a path exactly, or matches one of its ancestor directories, so a
// single name removes a whole tree.
func applyExcludes(root string, patterns []string) error {
	if len(patterns) == 0 {
		return nil
	}
	var doomed []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if excluded(rel, patterns) {
			doomed = append(doomed, p)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Deepest first, so removing a directory does not invalidate a later path.
	sort.Sort(sort.Reverse(sort.StringSlice(doomed)))
	for _, p := range doomed {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("harvest: exclude %s: %w", p, err)
		}
	}
	return nil
}

// excluded reports whether a repository-relative path matches any pattern,
// directly or through one of its parent directories.
func excluded(rel string, patterns []string) bool {
	slashed := filepath.ToSlash(rel)
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, slashed); ok {
			return true
		}
		for dir := path.Dir(slashed); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if ok, _ := path.Match(pattern, dir); ok {
				return true
			}
		}
	}
	return false
}

// taskIDFor builds a stable, filesystem-safe task id from the candidate.
func taskIDFor(c Candidate, firstCommit string) string {
	base := c.Commits[0].Subject
	// Drop a conventional-commit prefix: the type is metadata, not the name.
	if i := strings.Index(base, ": "); i > 0 && i < 16 {
		base = base[i+2:]
	}
	slug := slugify(base)
	if slug == "" {
		slug = "harvested"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	return slug + "-" + short(firstCommit)
}

// slugify lowercases and reduces a string to dashes and alphanumerics.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// writeUnder writes content at root/rel, creating parents and refusing to
// escape the root.
func writeUnder(root, rel string, content []byte) error {
	clean := path.Clean(rel)
	if strings.HasPrefix(clean, "../") || clean == ".." || path.IsAbs(clean) {
		return fmt.Errorf("harvest: unsafe path %q", rel)
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o644)
}

// archiveTo extracts a revision's tree into dir without its history.
func archiveTo(ctx context.Context, repo, rev, dir string) error {
	// git archive writes a tar stream; tar extracts it. Both are present
	// wherever git is, and this avoids copying .git.
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", rev)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", dir)
	untar.Stdin = pipe
	untar.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := untar.Run(); err != nil {
		return fmt.Errorf("extract %s: %w", short(rev), err)
	}
	return cmd.Wait()
}

// revParse resolves a revision to its full hash.
func revParse(ctx context.Context, repo, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--verify", rev)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// changedFiles lists the paths that differ between two revisions.
func changedFiles(ctx context.Context, repo, from, to string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--name-only", from, to)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("harvest: diff %s..%s: %w", short(from), short(to), err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files, nil
}

// showFile reads one path's content at a revision.
func showFile(ctx context.Context, repo, rev, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "show", rev+":"+path)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("harvest: read %s at %s: %w", path, short(rev), err)
	}
	return out, nil
}

// inferTestCommand guesses how to run a repository's tests from its manifest.
// It returns "" when nothing recognisable is present, and the caller says so
// rather than inventing a command that would silently pass.
func inferTestCommand(root string) []string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	switch {
	case exists("go.mod"):
		return []string{"go", "test", "./..."}
	case exists("package.json"):
		return []string{"npm", "test"}
	case exists("pyproject.toml") || exists("pytest.ini") || exists("requirements.txt"):
		return []string{"python3", "-m", "pytest"}
	case exists("Cargo.toml"):
		return []string{"cargo", "test"}
	}
	return nil
}

// short abbreviates a commit hash for messages.
func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// promptDraft writes the material a human needs to state the task, with the
// answer kept clearly apart from the prompt.
func promptDraft(c Candidate, tests, sources []string, command []string) string {
	var b strings.Builder
	b.WriteString("# TODO: state the task\n\n")
	b.WriteString("This prompt is a scaffold, not a finished task. The user turn below is what was\n")
	b.WriteString("actually said, and it is usually a continuation of a conversation: it may carry\n")
	b.WriteString("no goal, no acceptance criterion and no context. Rewrite it as a standalone\n")
	b.WriteString("instruction before running the task, or the run measures nothing.\n\n")

	b.WriteString("## What was said\n\n")
	b.WriteString("> " + strings.ReplaceAll(strings.TrimSpace(c.Prompt), "\n", "\n> ") + "\n\n")

	b.WriteString("## Context\n\n")
	fmt.Fprintf(&b, "- Session: %s\n", c.SessionTitle)
	fmt.Fprintf(&b, "- Repository: %s\n", c.Repo)
	fmt.Fprintf(&b, "- Asked: %s\n", c.AskedAt.Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(&b, "- Commits in this turn: %d, across %d file(s)\n\n", len(c.Commits), c.FilesChanged)

	b.WriteString("### What was committed — this is the answer, do not put it in the prompt\n\n")
	for _, cm := range c.Commits {
		fmt.Fprintf(&b, "- %s %s\n", short(cm.Hash), cm.Subject)
	}
	b.WriteString("\n")

	b.WriteString("## What the fixture already contains\n\n")
	if len(tests) == 0 {
		b.WriteString("No test file changed in this turn, so the fixture carries none of the\n")
		b.WriteString("work's tests. Add a validator that can fail before the change, or this\n")
		b.WriteString("task cannot show anything.\n\n")
	} else {
		b.WriteString("The tests the work added or changed are already in the fixture, at their\n")
		b.WriteString("final content, so the task fails until the change is made:\n\n")
		for _, p := range tests {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}

	b.WriteString("## The reference solution\n\n")
	b.WriteString("The changed non-test files are under `evaluator/reference/`, laid out at the\n")
	b.WriteString("same paths they occupy in the fixture:\n\n")
	for _, p := range sources {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\n")

	b.WriteString("## The inferred validator\n\n")
	if len(command) == 0 {
		b.WriteString("No test command could be inferred from the fixture. Write one in\n")
		b.WriteString("`task.yaml`, or the task cannot be graded.\n")
	} else {
		fmt.Fprintf(&b, "`%s` was inferred from the fixture's manifest. Confirm it, and add a\n",
			strings.Join(command, " "))
		b.WriteString("`diff` validator if the change must stay inside particular files.\n")
	}
	return b.String()
}

// taskScaffold writes a task.yaml that loads, with the validator inferred.
func taskScaffold(id string, c Candidate, command []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\n", id)
	b.WriteString("version: 1\n")
	fmt.Fprintf(&b, "name: %s\n", yamlScalar(c.SessionTitle))
	b.WriteString("difficulty: medium\n")
	b.WriteString("capabilities: []\n")
	b.WriteString("tags: [harvested]\n")
	b.WriteString("timeout: 900\n")
	b.WriteString("validators:\n")
	if len(command) == 0 {
		b.WriteString("  # TODO: no test command could be inferred; add one.\n")
		b.WriteString("  []\n")
		return b.String()
	}
	b.WriteString("  - kind: command\n")
	b.WriteString("    name: tests\n")
	fmt.Fprintf(&b, "    command: [%s]\n", yamlList(command))
	return b.String()
}

// yamlScalar quotes a value when YAML would otherwise misread it.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#[]{}&*!|>'\"%@`") || strings.HasPrefix(s, " ") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// yamlList renders a command as an inline YAML list.
func yamlList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, i := range items {
		quoted = append(quoted, `"`+i+`"`)
	}
	return strings.Join(quoted, ", ")
}
