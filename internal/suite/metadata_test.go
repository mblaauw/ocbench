package suite

import (
	"path/filepath"
	"strings"
	"testing"
)

// setSuiteTier rewrites suite.yaml with an extra tier line.
func setSuiteTier(t *testing.T, dir, tier string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "suite.yaml"),
		"name: mini\nversion: \"1.0.0\"\ndescription: Minimal suite used by loader tests.\ntier: "+tier+"\ndefaults:\n  timeout: 120\n")
}

// setTaskMetadata rewrites tasks/t1/task.yaml with extra metadata lines.
func setTaskMetadata(t *testing.T, dir, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "tasks", "t1", "task.yaml"),
		"id: t1\nversion: 1\nname: Task one\ntags: [alpha, beta]\n"+extra+
			"timeout: 300\nrequires: [sh]\nallow_changes:\n  - \"src/**\"\nvalidators:\n  - kind: command\n    name: check\n    command: [\"sh\", \"-c\", \"true\"]\n  - kind: answer\n    name: cause\n    patterns: [\"yes\"]\n")
}

func TestTaskMetadataLoads(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		setSuiteTier(t, dir, "standard")
		setTaskMetadata(t, dir, "difficulty: medium\ncapabilities: [debugging, multi-file]\nexpected_tokens: 40000\n")
	})
	s := mustLoad(t, dir)

	if s.Tier != "standard" {
		t.Errorf("Suite.Tier = %q, want standard", s.Tier)
	}
	task, err := s.Task("t1")
	if err != nil {
		t.Fatalf("Task(t1): %v", err)
	}
	if task.Difficulty != "medium" {
		t.Errorf("Difficulty = %q, want medium", task.Difficulty)
	}
	if got := strings.Join(task.Capabilities, ","); got != "debugging,multi-file" {
		t.Errorf("Capabilities = %q", got)
	}
	if task.ExpectedTokens != 40000 {
		t.Errorf("ExpectedTokens = %d, want 40000", task.ExpectedTokens)
	}
}

func TestTaskWithoutMetadataLoads(t *testing.T) {
	s := mustLoad(t, tempMini(t, nil))
	task, err := s.Task("t1")
	if err != nil {
		t.Fatalf("Task(t1): %v", err)
	}
	if task.Difficulty != "" || len(task.Capabilities) != 0 || task.ExpectedTokens != 0 {
		t.Errorf("unexpected metadata: %+v", task)
	}
	if s.Tier != "" {
		t.Errorf("Suite.Tier = %q, want empty", s.Tier)
	}
}

func TestMetadataValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		tier  string
		extra string
		want  string
	}{
		{"unknown difficulty", "", "difficulty: impossible\n", "difficulty"},
		{"unknown capability", "", "capabilities: [telekinesis]\n", "capabilit"},
		{"negative expected tokens", "", "expected_tokens: -1\n", "expected_tokens"},
		{"unknown tier", "gigantic", "", "tier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempMini(t, func(dir string) {
				if tc.tier != "" {
					setSuiteTier(t, dir, tc.tier)
				}
				if tc.extra != "" {
					setTaskMetadata(t, dir, tc.extra)
				}
			})
			_, err := LoadDir(dir)
			if err == nil {
				t.Fatal("LoadDir succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestMetadataAffectsHashButEstimateDoesNot(t *testing.T) {
	base := mustLoad(t, tempMini(t, func(dir string) {
		setTaskMetadata(t, dir, "capabilities: [debugging]\n")
	}))

	withCapability := mustLoad(t, tempMini(t, func(dir string) {
		setTaskMetadata(t, dir, "capabilities: [debugging, restraint]\n")
	}))
	if withCapability.Hash == base.Hash {
		t.Error("changing capabilities did not change the suite hash")
	}

	withEstimate := mustLoad(t, tempMini(t, func(dir string) {
		setTaskMetadata(t, dir, "capabilities: [debugging]\nexpected_tokens: 99999\n")
	}))
	if withEstimate.Hash != base.Hash {
		t.Error("changing expected_tokens changed the suite hash; it is an estimate")
	}
}
