package suite

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/suites"
)

func embeddedCore(t *testing.T) *Suite {
	t.Helper()
	s, err := LoadFS(suites.FS(), "core")
	if err != nil {
		t.Fatalf("load embedded core: %v", err)
	}
	return s
}

// exportCore exports the embedded core suite and returns the destination dir.
func exportCore(t *testing.T) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "core")
	if err := Export(embeddedCore(t), dest); err != nil {
		t.Fatalf("export embedded core: %v", err)
	}
	return dest
}

func TestEmbeddedCoreSuiteListsThreeTasks(t *testing.T) {
	s := embeddedCore(t)

	if s.Name != "core" {
		t.Errorf("Name = %q, want core", s.Name)
	}
	if s.Hash == "" {
		t.Error("Hash is empty")
	}
	got := taskIDs(s.Tasks)
	want := []string{"multi-file-feature", "py-bugfix", "repo-investigation"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task ids = %v, want %v", got, want)
	}
	for _, task := range s.Tasks {
		if task.Version != "1" {
			t.Errorf("%s version = %q, want 1", task.ID, task.Version)
		}
		if task.TimeoutSeconds != 300 {
			t.Errorf("%s timeout = %d, want 300", task.ID, task.TimeoutSeconds)
		}
		if len(task.Validators) == 0 {
			t.Errorf("%s has no validators", task.ID)
		}
		if len(task.Tags) == 0 {
			t.Errorf("%s has no tags", task.ID)
		}
		if task.Prompt == "" {
			t.Errorf("%s has no prompt", task.ID)
		}
		var files int
		if err := fs.WalkDir(task.Fixture, ".", func(string, fs.DirEntry, error) error {
			files++
			return nil
		}); err != nil {
			t.Fatalf("%s fixture walk: %v", task.ID, err)
		}
		if files == 0 {
			t.Errorf("%s fixture is empty", task.ID)
		}
	}
}

func TestListSources(t *testing.T) {
	sources, err := ListSources(suites.FS())
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %v, want exactly core", sources)
	}
	if sources[0].Name != "core" || !sources[0].Embedded || sources[0].Dir != "" {
		t.Fatalf("source = %+v, want embedded core", sources[0])
	}
}

func TestResolveFallsBackToEmbedded(t *testing.T) {
	paths := config.Paths{Suites: filepath.Join(t.TempDir(), "suites")}
	s, src, err := Resolve(suites.FS(), paths, "core", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !src.Embedded || src.Dir != "" {
		t.Fatalf("source = %+v, want embedded", src)
	}
	if s.Name != "core" || s.Hash != embeddedCore(t).Hash {
		t.Fatalf("resolved suite %q hash %q does not match embedded", s.Name, s.Hash)
	}
}

func TestResolvePrefersOnDiskOverride(t *testing.T) {
	base := t.TempDir()
	suitesDir := filepath.Join(base, "suites")
	dest := filepath.Join(suitesDir, "core")
	if err := Export(embeddedCore(t), dest); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Mark the on-disk copy so a fallback to the embedded suite is detectable.
	marker := "on-disk override"
	yamlPath := filepath.Join(dest, "suite.yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, yamlPath, strings.Replace(string(b), "description:", "description: "+marker+" ", 1))

	s, src, err := Resolve(suites.FS(), config.Paths{Suites: suitesDir}, "core", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.Embedded {
		t.Fatal("Resolve fell back to the embedded suite instead of the on-disk override")
	}
	if src.Dir != dest {
		t.Fatalf("source dir = %q, want %q", src.Dir, dest)
	}
	if !strings.Contains(s.Description, marker) {
		t.Fatalf("description = %q, want the on-disk marker", s.Description)
	}
}

func TestResolveUsesExplicitSuiteDir(t *testing.T) {
	dest := exportCore(t)
	s, src, err := Resolve(suites.FS(), config.Paths{Suites: filepath.Join(t.TempDir(), "empty")}, "core", dest)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.Embedded || src.Dir != dest {
		t.Fatalf("source = %+v, want explicit dir %q", src, dest)
	}
	if s.Dir != dest {
		t.Fatalf("suite dir = %q, want %q", s.Dir, dest)
	}
}

func TestResolveNotFoundNamesEveryLocation(t *testing.T) {
	suitesDir := filepath.Join(t.TempDir(), "suites")
	_, _, err := Resolve(suites.FS(), config.Paths{Suites: suitesDir}, "nope", "")
	if err == nil {
		t.Fatal("Resolve succeeded, want not-found error")
	}
	for _, want := range []string{filepath.Join(suitesDir, "nope"), "embedded", "nope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name %q", err, want)
		}
	}
}

func TestExportRoundTripPreservesHash(t *testing.T) {
	s := embeddedCore(t)
	dest := filepath.Join(t.TempDir(), "exported")
	if err := Export(s, dest); err != nil {
		t.Fatalf("Export: %v", err)
	}

	for _, rel := range []string{"suite.yaml", "tasks/py-bugfix/task.yaml", "tasks/py-bugfix/prompt.md", "tasks/py-bugfix/fixture/calc.py"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("exported file %s: %v", rel, err)
		}
	}

	loaded, err := LoadDir(dest)
	if err != nil {
		t.Fatalf("LoadDir(exported): %v", err)
	}
	if loaded.Hash != s.Hash {
		t.Fatalf("round-trip hash = %q, want %q", loaded.Hash, s.Hash)
	}
	if got, want := taskIDs(loaded.Tasks), taskIDs(s.Tasks); !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip task ids = %v, want %v", got, want)
	}
}

func TestExportRefusesNonEmptyDestination(t *testing.T) {
	s := embeddedCore(t)

	t.Run("existing file", func(t *testing.T) {
		dest := t.TempDir()
		writeFile(t, filepath.Join(dest, "stray.txt"), "occupied\n")
		if err := Export(s, dest); err == nil {
			t.Fatal("Export succeeded into a non-empty dir")
		}
	})

	t.Run("second export", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "exported")
		if err := Export(s, dest); err != nil {
			t.Fatalf("first Export: %v", err)
		}
		if err := Export(s, dest); err == nil {
			t.Fatal("second Export succeeded into a non-empty dir")
		}
	})
}
