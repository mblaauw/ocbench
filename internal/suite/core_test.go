package suite

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mbl/ocbench/internal/evaluation"
)

// These tests prove that the two core tasks added in Task 10 are honest: their
// validators fail on the untouched fixture and pass after the intended fix,
// and the code-review answer patterns accept a complete review while rejecting
// one that omits a defect.

// writeFS materialises an fs.FS (a task fixture) into dir on disk.
func writeFS(t *testing.T, fsys fs.FS, dir string) {
	t.Helper()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path.Clean(p)))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
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
	if err != nil {
		t.Fatalf("write fixture to %s: %v", dir, err)
	}
}

func evalSpec(v Validator) evaluation.ValidatorSpec {
	return evaluation.ValidatorSpec{
		Kind:     v.Kind,
		Name:     v.Name,
		Command:  v.Command,
		Patterns: v.Patterns,
		Mode:     v.Mode,
	}
}

func TestEmbeddedCoreLoadsNewTasks(t *testing.T) {
	s := embeddedCore(t)

	config, err := s.Task("config-yaml-fix")
	if err != nil {
		t.Fatalf("Task(config-yaml-fix): %v", err)
	}
	if config.Version != "1" {
		t.Errorf("config-yaml-fix version = %q, want 1", config.Version)
	}
	if config.TimeoutSeconds != 300 {
		t.Errorf("config-yaml-fix timeout = %d, want 300", config.TimeoutSeconds)
	}
	if !reflect.DeepEqual(config.Requires, []string{"python3"}) {
		t.Errorf("config-yaml-fix requires = %v, want [python3]", config.Requires)
	}
	if !reflect.DeepEqual(config.AllowChanges, []string{"config/**"}) {
		t.Errorf("config-yaml-fix allow_changes = %v, want [config/**]", config.AllowChanges)
	}
	if len(config.Validators) != 1 || config.Validators[0].Kind != "command" {
		t.Fatalf("config-yaml-fix validators = %+v, want one command validator", config.Validators)
	}
	if got, err := fs.ReadFile(config.Fixture, "config/app.yaml"); err != nil || len(got) == 0 {
		t.Errorf("config-yaml-fix fixture config/app.yaml = %q, %v", got, err)
	}

	review, err := s.Task("code-review")
	if err != nil {
		t.Fatalf("Task(code-review): %v", err)
	}
	if review.Version != "1" {
		t.Errorf("code-review version = %q, want 1", review.Version)
	}
	if len(review.AllowChanges) != 0 {
		t.Errorf("code-review allow_changes = %v, want none", review.AllowChanges)
	}
	if len(review.Validators) != 1 || review.Validators[0].Kind != "answer" {
		t.Fatalf("code-review validators = %+v, want one answer validator", review.Validators)
	}
	if len(review.Validators[0].Patterns) < 3 {
		t.Errorf("code-review patterns = %v, want patterns for all three defects", review.Validators[0].Patterns)
	}
	if _, ok := review.Evaluator["answer.json"]; !ok {
		t.Errorf("code-review evaluator = %v, want answer.json", review.Evaluator)
	}
}

// TestConfigYamlFixValidatorFailsUntouchedPassesAfterFix copies the fixture to
// a temp dir, runs the real command validator engine, then applies the intended
// config-only correction and reruns it.
func TestConfigYamlFixValidatorFailsUntouchedPassesAfterFix(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	task, err := embeddedCore(t).Task("config-yaml-fix")
	if err != nil {
		t.Fatalf("Task(config-yaml-fix): %v", err)
	}
	cmd := task.Validators[0]
	if cmd.Kind != "command" {
		t.Fatalf("validator kind = %q, want command", cmd.Kind)
	}

	dir := filepath.Join(t.TempDir(), "fixture")
	writeFS(t, task.Fixture, dir)

	ctx := context.Background()
	before := evaluation.RunValidator(ctx, 0, evalSpec(cmd), dir, os.Environ(), 60*time.Second, "")
	if before.Status != "failed" {
		t.Fatalf("untouched fixture status = %q (exit %d), want failed; output:\n%s",
			before.Status, before.ExitCode, before.Output)
	}

	cfgPath := filepath.Join(dir, "config", "app.yaml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.ReplaceAll(string(raw), "retires:", "retries:")
	if fixed == string(raw) {
		t.Fatalf("config/app.yaml does not contain the planted misspelling:\n%s", raw)
	}
	writeFile(t, cfgPath, fixed)

	after := evaluation.RunValidator(ctx, 1, evalSpec(cmd), dir, os.Environ(), 60*time.Second, "")
	if after.Status != "passed" {
		t.Fatalf("fixed fixture status = %q (exit %d), want passed; output:\n%s",
			after.Status, after.ExitCode, after.Output)
	}
}

// TestCodeReviewAnswerPatterns proves the hidden answer patterns accept a
// complete structured review and reject reviews that omit a planted defect.
func TestCodeReviewAnswerPatterns(t *testing.T) {
	task, err := embeddedCore(t).Task("code-review")
	if err != nil {
		t.Fatalf("Task(code-review): %v", err)
	}
	spec := evalSpec(task.Validators[0])
	ctx := context.Background()

	complete := strings.Join([]string{
		"Review of batch.py; three defects found:",
		"1. chunks: off-by-one in the range step (size - 1); it should be size.",
		"2. parse_ints: swallowed exception; `except ValueError: pass` silently",
		"   ignores bad input instead of letting the error propagate.",
		"3. take: wrong default; limit defaults to 10 but should default to 3.",
	}, "\n")
	res := evaluation.RunValidator(ctx, 0, spec, "", nil, 0, complete)
	if res.Status != "passed" {
		t.Fatalf("complete review status = %q, want passed; output:\n%s", res.Status, res.Output)
	}

	missingDefault := strings.Join([]string{
		"1. chunks: off-by-one in the range step (size - 1).",
		"2. parse_ints: swallowed exception, `except ValueError: pass`.",
	}, "\n")
	res = evaluation.RunValidator(ctx, 1, spec, "", nil, 0, missingDefault)
	if res.Status != "failed" {
		t.Fatalf("review omitting the wrong default status = %q, want failed; output:\n%s", res.Status, res.Output)
	}

	missingSwallowed := strings.Join([]string{
		"chunks has an off-by-one: the range step is size - 1, should be size.",
		"take has the wrong default: limit defaults to 10, should be 3.",
	}, "\n")
	res = evaluation.RunValidator(ctx, 2, spec, "", nil, 0, missingSwallowed)
	if res.Status != "failed" {
		t.Fatalf("review omitting the swallowed exception status = %q, want failed; output:\n%s", res.Status, res.Output)
	}
}
