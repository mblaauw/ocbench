package suite

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/evaluation"
)

// TestEveryTaskFailsUntouchedAndPassesWithItsReference is the automated form of
// the authoring rule in design §7: a task must fail on its untouched fixture and
// pass with evaluator/reference applied. Without it, a task can ship that
// measures nothing.
//
// Process validators are excluded: they observe how an agent worked, which no
// reference tree can supply. They are covered by the evaluation package's tests
// instead.
func TestEveryTaskFailsUntouchedAndPassesWithItsReference(t *testing.T) {
	for _, suiteDir := range discoverSuites(t) {
		s, err := LoadDir(suiteDir)
		if err != nil {
			t.Fatalf("LoadDir(%s): %v", suiteDir, err)
		}
		for _, task := range s.Tasks {
			t.Run(s.Name+"/"+task.ID, func(t *testing.T) {
				checkTaskHonesty(t, task)
			})
		}
	}
}

// discoverSuites lists every suite under ../../suites, so a new suite is
// covered by the honesty rule the moment it exists.
func discoverSuites(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../../suites")
	if err != nil {
		t.Fatalf("read suites dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join("../../suites", e.Name()))
	}
	if len(out) == 0 {
		t.Fatal("no suites found under ../../suites")
	}
	return out
}

func checkTaskHonesty(t *testing.T, task *Task) {
	t.Helper()

	dir := t.TempDir()
	if _, err := copyFS(task.Fixture, dir); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	if task.Reference == nil {
		t.Fatalf("task %q has no evaluator/reference, so it cannot be proven", task.ID)
	}

	// Before: nothing has been done and nothing has been answered.
	before := runTaskValidators(t, task, dir, nil, "")
	if !anyFailed(before) {
		t.Fatalf("task %q passes on its untouched fixture, so it measures nothing:\n%s",
			task.ID, describe(before))
	}

	// A real run copies evaluator/tests into the worktree before validators
	// run; without this, a task whose tests are hidden looks unsolvable here.
	// Hidden tests land where the task says, exactly as the runner does.
	hiddenDir := dir
	if dest := task.HiddenTestsDest; dest != "" && dest != "." {
		hiddenDir = filepath.Join(dir, filepath.FromSlash(dest))
	}
	if _, err := copyFS(task.HiddenTests, hiddenDir); err != nil {
		t.Fatalf("copy hidden tests: %v", err)
	}

	applied, err := copyFS(task.Reference, dir)
	if err != nil {
		t.Fatalf("apply reference: %v", err)
	}
	answer, err := referenceAnswer(task)
	if err != nil {
		t.Fatalf("task %q: %v", task.ID, err)
	}

	after := runTaskValidators(t, task, dir, applied, answer)
	for _, res := range after {
		if res.Status != "passed" && res.Status != "skipped" {
			t.Errorf("task %q: validator %q (%s) is %s with the reference applied:\n%s",
				task.ID, res.Name, res.Kind, res.Status, res.Output)
		}
	}
}

// runTaskValidators runs every non-process validator against dir. reference
// holds the relative paths the reference tree wrote, which stands in for the
// change set and diff a real run would produce.
func runTaskValidators(t *testing.T, task *Task, dir string, reference []string, answer string) []evaluation.ValidationResult {
	t.Helper()

	vctx := evaluation.ValidatorContext{
		Worktree:    dir,
		Changed:     reference,
		Diff:        syntheticDiff(reference, dir),
		FinalAnswer: answer,
	}

	var out []evaluation.ValidationResult
	seq := 0
	for _, v := range task.Validators {
		if v.Kind == "process" {
			continue // depends on agent behaviour, not on the fixture
		}
		seq++
		out = append(out, evaluation.RunValidatorContext(context.Background(), seq, evaluation.ValidatorSpec{
			Kind:           v.Kind,
			Name:           v.Name,
			Command:        v.Command,
			Patterns:       v.Patterns,
			Mode:           v.Mode,
			RequiredPaths:  v.RequiredPaths,
			ForbiddenPaths: v.ForbiddenPaths,
			MaxLines:       v.MaxLines,
			Present:        v.Present,
			Absent:         v.Absent,
			Weight:         v.Weight,
		}, vctx, harnessEnv(), 0))
	}
	return out
}

// harnessEnv mirrors the sandbox overrides a real run applies. In particular
// PYTHONDONTWRITEBYTECODE stops the "before" phase from leaving a bytecode file
// that the "after" phase would import instead of the reference: a same-length
// edit in the same second produces a cache hit on stale code.
func harnessEnv() []string {
	return append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
}

// referenceAnswer returns evaluator/reference/answer.txt, which an answer
// validator must accept. An answer task without one cannot be proven.
func referenceAnswer(task *Task) (string, error) {
	needsAnswer := false
	for _, v := range task.Validators {
		if v.Kind == "answer" {
			needsAnswer = true
			break
		}
	}
	data, err := fs.ReadFile(task.Reference, "answer.txt")
	if err != nil {
		if needsAnswer {
			return "", fmt.Errorf("has an answer validator but no evaluator/reference/answer.txt")
		}
		return "", nil
	}
	return string(data), nil
}

// syntheticDiff renders the reference's files as an added-lines-only diff so a
// max_lines validator can be evaluated without a git repository.
func syntheticDiff(reference []string, dir string) []byte {
	var b strings.Builder
	for _, rel := range reference {
		b.WriteString("+++ b/" + rel + "\n")
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			b.WriteString("+" + line + "\n")
		}
	}
	return []byte(b.String())
}

// copyFS writes every regular file in src under dst, returning the relative
// paths it wrote. A nil src is a no-op.
func copyFS(src fs.FS, dst string) ([]string, error) {
	if src == nil {
		return nil, nil
	}
	var written []string
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		written = append(written, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

func anyFailed(results []evaluation.ValidationResult) bool {
	for _, r := range results {
		if r.Status != "passed" && r.Status != "skipped" {
			return true
		}
	}
	return false
}

func describe(results []evaluation.ValidationResult) string {
	var b strings.Builder
	for _, r := range results {
		b.WriteString("  " + r.Kind + "/" + r.Name + ": " + r.Status + "\n")
	}
	return b.String()
}
