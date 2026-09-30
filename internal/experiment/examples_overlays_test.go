package experiment_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mbl/ocbench/internal/experiment"
)

// The shipped example overlays are the first thing a new user runs, so they must
// stay loadable, be valid JSON, and actually differ from each other — an example
// pair that profiles identically would teach the reader nothing.
func TestShippedExampleOverlaysAreValidAndDistinct(t *testing.T) {
	root := repoRoot(t)
	paths := map[string]string{
		"lean":      filepath.Join(root, "examples", "overlays", "lean", "lean.json"),
		"delegated": filepath.Join(root, "examples", "overlays", "delegated", "delegated.json"),
	}
	seen := map[string]string{}
	for label, path := range paths {
		overlay, err := experiment.ResolveOverlay(path)
		if err != nil {
			t.Errorf("%s overlay does not resolve: %v", label, err)
			continue
		}
		if overlay.Kind != experiment.OverlayFile {
			t.Errorf("%s overlay kind = %q, want file", label, overlay.Kind)
		}
		if overlay.SHA256 == "" {
			t.Errorf("%s overlay has no content hash", label)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s overlay unreadable: %v", label, err)
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Errorf("%s overlay is not valid JSON: %v", label, err)
			continue
		}
		if _, ok := decoded["agent"]; !ok {
			t.Errorf("%s overlay defines no agent block; the comparison would be a no-op", label)
		}
		if other, dup := seen[overlay.SHA256]; dup {
			t.Errorf("%s and %s overlays are byte-identical", label, other)
		}
		seen[overlay.SHA256] = label
	}
}

// repoRoot walks up from the test's directory to the module root so the examples
// are found regardless of the package under test.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find go.mod above the test directory")
	return ""
}
