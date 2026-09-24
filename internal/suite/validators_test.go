package suite

import (
	"path/filepath"
	"strings"
	"testing"
)

// setTaskValidators rewrites tasks/t1/task.yaml with a caller-supplied
// validators block.
func setTaskValidators(t *testing.T, dir, validators string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "tasks", "t1", "task.yaml"),
		"id: t1\nversion: 1\nname: Task one\ntimeout: 300\nrequires: [sh]\nallow_changes:\n  - \"src/**\"\nvalidators:\n"+validators)
}

func TestDiffGrepAndProcessValidatorsLoad(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		setTaskValidators(t, dir, `  - kind: diff
    name: touched only src
    required_paths: ["src/**"]
    forbidden_paths: ["tests/**"]
    max_lines: 60
    weight: 2
  - kind: grep
    name: no debug leftovers
    absent: ["print\\("]
  - kind: process
    name: ran the tests
    tool_pattern: "(unittest|pytest)"
`)
	})
	s := mustLoad(t, dir)
	task, err := s.Task("t1")
	if err != nil {
		t.Fatalf("Task(t1): %v", err)
	}
	if len(task.Validators) != 3 {
		t.Fatalf("validators = %d, want 3", len(task.Validators))
	}

	d := task.Validators[0]
	if d.Kind != "diff" || d.MaxLines != 60 || d.Weight != 2 {
		t.Errorf("diff validator = %+v", d)
	}
	if strings.Join(d.RequiredPaths, ",") != "src/**" || strings.Join(d.ForbiddenPaths, ",") != "tests/**" {
		t.Errorf("diff paths = %v / %v", d.RequiredPaths, d.ForbiddenPaths)
	}
	if g := task.Validators[1]; g.Kind != "grep" || strings.Join(g.Absent, ",") != `print\(` {
		t.Errorf("grep validator = %+v", g)
	}
	if p := task.Validators[2]; p.Kind != "process" || p.ToolPattern != "(unittest|pytest)" {
		t.Errorf("process validator = %+v", p)
	}
}

func TestNewValidatorKindsRequireTheirKeys(t *testing.T) {
	cases := []struct {
		name       string
		validators string
		want       string
	}{
		{"diff without constraints", "  - kind: diff\n    name: d\n", "diff validator needs"},
		{"grep without patterns", "  - kind: grep\n    name: g\n", "grep validator needs"},
		{"process without pattern", "  - kind: process\n    name: p\n", "tool_pattern"},
		{"process with bad pattern", "  - kind: process\n    name: p\n    tool_pattern: \"(\"\n", "invalid tool_pattern"},
		{"negative max_lines", "  - kind: diff\n    name: d\n    max_lines: -1\n", "max_lines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempMini(t, func(dir string) { setTaskValidators(t, dir, tc.validators) })
			_, err := LoadDir(dir)
			if err == nil {
				t.Fatal("LoadDir succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestValidatorWeightDoesNotChangeExistingBehaviour(t *testing.T) {
	// A validator without a weight keeps weight zero, which the engine reads as
	// one; loading must not invent a value.
	s := mustLoad(t, tempMini(t, nil))
	task, err := s.Task("t1")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range task.Validators {
		if v.Weight != 0 {
			t.Errorf("validator %q weight = %v, want 0 (meaning 1)", v.Name, v.Weight)
		}
	}
}
