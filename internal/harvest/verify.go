package harvest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Verification is the outcome of checking that a scaffolded task has the
// property every task is held to: it fails before the reference is applied and
// passes after it.
type Verification struct {
	// Checked is false when the task has no runnable command validator, so the
	// property could not be tested at all. That is not the same as passing.
	Checked bool
	// FailsBefore and PassesAfter are the two observations.
	FailsBefore bool
	PassesAfter bool
	// Command is what was run.
	Command []string
	// Detail explains a failure, quoting the tail of the output.
	Detail string
}

// OK reports whether the task is honest.
func (v Verification) OK() bool { return v.Checked && v.FailsBefore && v.PassesAfter }

// Verify checks a scaffolded task directory.
//
// It copies the fixture to a temporary directory, runs the task's command
// validator, applies the reference, and runs it again. A task that passes
// untouched shows nothing, and a task that still fails with its own reference
// cannot be solved, so either finding is reported rather than assumed.
func Verify(ctx context.Context, taskDir string) (Verification, error) {
	command, err := commandValidator(filepath.Join(taskDir, "task.yaml"))
	if err != nil {
		return Verification{}, err
	}
	if len(command) == 0 {
		return Verification{}, nil
	}
	v := Verification{Checked: true, Command: command}

	work := filepath.Join(os.TempDir(), "ocbench-verify-"+slugify(filepath.Base(taskDir)))
	if err := os.RemoveAll(work); err != nil {
		return Verification{}, err
	}
	defer os.RemoveAll(work)

	if err := copyTree(filepath.Join(taskDir, "fixture"), work); err != nil {
		return Verification{}, fmt.Errorf("copy fixture: %w", err)
	}
	out, err := runCommand(ctx, work, command)
	v.FailsBefore = err != nil
	if !v.FailsBefore {
		v.Detail = "the fixture passes untouched, so the task cannot show anything"
		return v, nil
	}
	_ = out

	if err := copyTree(filepath.Join(taskDir, "evaluator", "reference"), work); err != nil {
		return Verification{}, fmt.Errorf("apply reference: %w", err)
	}
	out, err = runCommand(ctx, work, command)
	v.PassesAfter = err == nil
	if !v.PassesAfter {
		v.Detail = "still failing with the reference applied: " + tail(out)
	}
	return v, nil
}

// commandValidator reads the first command validator's argv from a task.yaml.
// It parses the file directly rather than loading a suite, because a scaffold is
// a single task and need not be part of one yet.
func commandValidator(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read task.yaml: %w", err)
	}
	var spec struct {
		Validators []struct {
			Kind    string   `yaml:"kind"`
			Command []string `yaml:"command"`
		} `yaml:"validators"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse task.yaml: %w", err)
	}
	for _, val := range spec.Validators {
		if val.Kind == "command" && len(val.Command) > 0 {
			return val.Command, nil
		}
	}
	return nil, nil
}

// runCommand executes argv in dir and returns its combined output.
func runCommand(ctx context.Context, dir string, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	return cmd.CombinedOutput()
}

// copyTree copies a directory over another, creating what it needs. A missing
// source is not an error: a task may legitimately have no reference files.
func copyTree(from, to string) error {
	if _, err := os.Stat(from); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, content, 0o644)
	})
}

// tail returns the last few lines of command output for an explanation.
func tail(out []byte) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}
