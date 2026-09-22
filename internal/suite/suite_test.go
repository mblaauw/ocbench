package suite

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const miniDir = "testdata/mini"

func mustLoad(t *testing.T, dir string) *Suite {
	t.Helper()
	s, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir(%q): %v", dir, err)
	}
	return s
}

// copyTree copies a directory tree verbatim. Used to give each destructive test
// its own writable suite in a temp dir.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// tempMini returns a writable copy of testdata/mini, optionally mutated before
// the caller loads it.
func tempMini(t *testing.T, mutate func(dir string)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mini")
	copyTree(t, miniDir, dir)
	if mutate != nil {
		mutate(dir)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadMiniHappyPath(t *testing.T) {
	s := mustLoad(t, miniDir)

	if s.Name != "mini" {
		t.Errorf("Name = %q, want mini", s.Name)
	}
	if s.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0", s.Version)
	}
	if s.Description == "" {
		t.Error("Description is empty")
	}
	if s.Dir == "" || !filepath.IsAbs(s.Dir) {
		t.Errorf("Dir = %q, want absolute path", s.Dir)
	}
	if s.Hash == "" {
		t.Error("suite Hash is empty")
	}
	if s.FS == nil {
		t.Fatal("Suite.FS is nil")
	}
	if _, err := fs.ReadFile(s.FS, "suite.yaml"); err != nil {
		t.Errorf("Suite.FS does not open suite.yaml: %v", err)
	}

	if got := taskIDs(s.Tasks); !reflect.DeepEqual(got, []string{"t1", "t2"}) {
		t.Fatalf("task ids = %v, want [t1 t2]", got)
	}

	t1, err := s.Task("t1")
	if err != nil {
		t.Fatalf("Task(t1): %v", err)
	}
	if t1.Version != "1" {
		t.Errorf("t1.Version = %q, want 1", t1.Version)
	}
	if t1.Name != "Task one" {
		t.Errorf("t1.Name = %q", t1.Name)
	}
	if !reflect.DeepEqual(t1.Tags, []string{"alpha", "beta"}) {
		t.Errorf("t1.Tags = %v", t1.Tags)
	}
	if t1.TimeoutSeconds != 300 {
		t.Errorf("t1.TimeoutSeconds = %d, want 300", t1.TimeoutSeconds)
	}
	if !reflect.DeepEqual(t1.Requires, []string{"sh"}) {
		t.Errorf("t1.Requires = %v", t1.Requires)
	}
	if !reflect.DeepEqual(t1.AllowChanges, []string{"src/**"}) {
		t.Errorf("t1.AllowChanges = %v", t1.AllowChanges)
	}
	if t1.Prompt == "" {
		t.Error("t1.Prompt is empty")
	}
	if t1.FixtureHash == "" || t1.SpecHash == "" {
		t.Errorf("t1 hashes: fixture=%q spec=%q", t1.FixtureHash, t1.SpecHash)
	}
	if t1.Dir == "" || !filepath.IsAbs(t1.Dir) {
		t.Errorf("t1.Dir = %q, want absolute path", t1.Dir)
	}
	if got, err := fs.ReadFile(t1.Fixture, "a.txt"); err != nil || string(got) != "hello\n" {
		t.Errorf("t1 fixture a.txt = %q, %v", got, err)
	}
	if len(t1.Evaluator) != 0 {
		t.Errorf("t1.Evaluator = %v, want empty", t1.Evaluator)
	}
	if len(t1.Validators) != 2 {
		t.Fatalf("t1 validators = %d, want 2", len(t1.Validators))
	}
	cmd := t1.Validators[0]
	if cmd.Kind != "command" || cmd.Name != "check" {
		t.Errorf("t1[0] = %+v", cmd)
	}
	if !reflect.DeepEqual(cmd.Command, []string{"sh", "-c", "true"}) {
		t.Errorf("t1[0].Command = %v", cmd.Command)
	}
	answer := t1.Validators[1]
	if answer.Kind != "answer" || !reflect.DeepEqual(answer.Patterns, []string{"yes"}) {
		t.Errorf("t1[1] = %+v", answer)
	}
	if answer.Mode != "all" {
		t.Errorf("t1[1].Mode = %q, want all", answer.Mode)
	}

	t2, err := s.Task("t2")
	if err != nil {
		t.Fatalf("Task(t2): %v", err)
	}
	if t2.TimeoutSeconds != 120 {
		t.Errorf("t2.TimeoutSeconds = %d, want suite default 120", t2.TimeoutSeconds)
	}
	if len(t2.Tags) != 0 || len(t2.Requires) != 0 || len(t2.AllowChanges) != 0 {
		t.Errorf("t2 defaults: tags=%v requires=%v allow=%v", t2.Tags, t2.Requires, t2.AllowChanges)
	}
	if got, err := fs.ReadFile(t2.Fixture, "b.txt"); err != nil || string(got) != "world\n" {
		t.Errorf("t2 fixture b.txt = %q, %v", got, err)
	}
	if _, ok := t2.Evaluator["answer.json"]; !ok {
		t.Errorf("t2.Evaluator = %v, want answer.json", t2.Evaluator)
	}
	if len(t2.Validators) != 1 {
		t.Fatalf("t2 validators = %d, want 1", len(t2.Validators))
	}

	if _, err := s.Task("missing"); err == nil {
		t.Error("Task(missing) returned no error")
	}
}

func taskIDs(tasks []*Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func TestAnswerPatternsFromEvaluator(t *testing.T) {
	s := mustLoad(t, miniDir)
	t2, err := s.Task("t2")
	if err != nil {
		t.Fatalf("Task(t2): %v", err)
	}
	v := t2.Validators[0]
	if v.Kind != "answer" {
		t.Fatalf("kind = %q, want answer", v.Kind)
	}
	if !reflect.DeepEqual(v.Patterns, []string{"alpha", "beta"}) {
		t.Errorf("Patterns = %v, want [alpha beta]", v.Patterns)
	}
	if v.Mode != "any" {
		t.Errorf("Mode = %q, want any", v.Mode)
	}
}

func TestTimeoutDefaultingChain(t *testing.T) {
	s := mustLoad(t, miniDir)

	t1, _ := s.Task("t1")
	if got := t1.EffectiveTimeout(s); got != 300*time.Second {
		t.Errorf("t1.EffectiveTimeout = %v, want 300s", got)
	}
	t2, _ := s.Task("t2")
	if got := t2.EffectiveTimeout(s); got != 120*time.Second {
		t.Errorf("t2.EffectiveTimeout = %v, want suite default 120s", got)
	}

	// No suite default: fall back to the built-in 900s.
	noDefaults := tempMini(t, func(dir string) {
		writeFile(t, filepath.Join(dir, "suite.yaml"), "name: mini\nversion: \"1.0.0\"\n")
	})
	s2 := mustLoad(t, noDefaults)
	t2b, _ := s2.Task("t2")
	if t2b.TimeoutSeconds != 900 {
		t.Errorf("t2.TimeoutSeconds = %d, want 900 fallback", t2b.TimeoutSeconds)
	}
	if got := t2b.EffectiveTimeout(s2); got != 900*time.Second {
		t.Errorf("t2.EffectiveTimeout = %v, want 900s", got)
	}

	// EffectiveTimeout resolves bare tasks too.
	bare := &Task{}
	if got := bare.EffectiveTimeout(s2); got != 900*time.Second {
		t.Errorf("bare.EffectiveTimeout = %v, want 900s", got)
	}
	withDefault := &Suite{}
	withDefault.Defaults.TimeoutSeconds = 42
	if got := bare.EffectiveTimeout(withDefault); got != 42*time.Second {
		t.Errorf("bare.EffectiveTimeout(suite default) = %v, want 42s", got)
	}
	if got := (&Task{TimeoutSeconds: 7}).EffectiveTimeout(withDefault); got != 7*time.Second {
		t.Errorf("explicit task timeout = %v, want 7s", got)
	}
}

func TestFixtureHashIsContentAddressed(t *testing.T) {
	base := mustLoad(t, miniDir)
	baseT1, _ := base.Task("t1")
	if baseT1.FixtureHash == "" {
		t.Fatal("FixtureHash is empty")
	}

	// A copy at a different absolute path must not change the hash: the hash
	// never embeds paths.
	dir := tempMini(t, nil)
	copied := mustLoad(t, dir)
	copiedT1, _ := copied.Task("t1")
	if copiedT1.FixtureHash != baseT1.FixtureHash {
		t.Errorf("FixtureHash depends on location: %q != %q", copiedT1.FixtureHash, baseT1.FixtureHash)
	}
	if copied.Hash != base.Hash {
		t.Errorf("Suite.Hash depends on location: %q != %q", copied.Hash, base.Hash)
	}

	// Changing one fixture byte changes the fixture hash and the suite hash.
	writeFile(t, filepath.Join(dir, "tasks", "t1", "fixture", "a.txt"), "changed\n")
	modified := mustLoad(t, dir)
	modifiedT1, _ := modified.Task("t1")
	if modifiedT1.FixtureHash == baseT1.FixtureHash {
		t.Error("FixtureHash did not change after fixture content changed")
	}
	if modified.Hash == base.Hash {
		t.Error("Suite.Hash did not change after fixture content changed")
	}
}

func TestHashOrderIndependent(t *testing.T) {
	s := mustLoad(t, miniDir)

	h1, err := HashTasks(s.Tasks)
	if err != nil {
		t.Fatalf("HashTasks: %v", err)
	}
	if h1 == "" {
		t.Fatal("HashTasks returned empty hash")
	}

	reversed := make([]*Task, len(s.Tasks))
	for i := range s.Tasks {
		reversed[i] = s.Tasks[len(s.Tasks)-1-i]
	}
	h2, err := HashTasks(reversed)
	if err != nil {
		t.Fatalf("HashTasks(reversed): %v", err)
	}
	if h1 != h2 {
		t.Errorf("HashTasks is order dependent: %q != %q", h1, h2)
	}

	// Suite.Hash must use the same order-independent helper.
	shuffled, err := suiteHash(s.Name, s.Version, reversed)
	if err != nil {
		t.Fatalf("suiteHash: %v", err)
	}
	if shuffled != s.Hash {
		t.Errorf("Suite.Hash is order dependent: %q != %q", shuffled, s.Hash)
	}
}

func TestLoadFSSubtree(t *testing.T) {
	// Loading through an fs.FS subtree must behave like LoadDir.
	s, err := LoadFS(os.DirFS("testdata"), "mini")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	if s.Name != "mini" || len(s.Tasks) != 2 {
		t.Errorf("LoadFS = name %q tasks %d", s.Name, len(s.Tasks))
	}
	if s.Dir != "" {
		t.Errorf("embedded Dir = %q, want empty", s.Dir)
	}
	if s.Tasks[0].Dir != "" {
		t.Errorf("embedded task Dir = %q, want empty", s.Tasks[0].Dir)
	}
	disk := mustLoad(t, miniDir)
	if s.Hash != disk.Hash {
		t.Errorf("LoadFS hash %q != LoadDir hash %q", s.Hash, disk.Hash)
	}
}

func TestMissingPromptIsError(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		if err := os.Remove(filepath.Join(dir, "tasks", "t1", "prompt.md")); err != nil {
			t.Fatal(err)
		}
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), "prompt.md") {
		t.Errorf("error %q must name task t1 and prompt.md", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error %q should wrap fs.ErrNotExist", err)
	}
}

func TestMissingFixtureIsError(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		if err := os.RemoveAll(filepath.Join(dir, "tasks", "t2", "fixture")); err != nil {
			t.Fatal(err)
		}
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "t2") || !strings.Contains(err.Error(), "fixture") {
		t.Errorf("error %q must name task t2 and fixture", err)
	}
}

func TestTaskIDDirectoryMismatchIsError(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		p := filepath.Join(dir, "tasks", "t1", "task.yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, strings.Replace(string(b), "id: t1", "id: other", 1))
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), "other") {
		t.Errorf("error %q must name directory t1 and id other", err)
	}
}

func TestUnknownValidatorKindIsError(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		p := filepath.Join(dir, "tasks", "t1", "task.yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, string(b)+"  - kind: bogus\n    name: nope\n")
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "t1") {
		t.Errorf("error %q must name task t1 and kind bogus", err)
	}
}

func TestCommandValidatorNeedsArgv(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		p := filepath.Join(dir, "tasks", "t1", "task.yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, strings.Replace(string(b), `command: ["sh", "-c", "true"]`, "command: []", 1))
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "t1") {
		t.Errorf("error %q must name task t1", err)
	}
}

func TestAnswerValidatorNeedsPatterns(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		if err := os.RemoveAll(filepath.Join(dir, "tasks", "t2", "evaluator")); err != nil {
			t.Fatal(err)
		}
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error")
	}
	if !strings.Contains(err.Error(), "t2") || !strings.Contains(err.Error(), "patterns") {
		t.Errorf("error %q must name task t2 and patterns", err)
	}
}

func TestSuiteYAMLRequiresNameAndVersion(t *testing.T) {
	for name, content := range map[string]string{
		"missing name":    "version: \"1.0.0\"\n",
		"missing version": "name: mini\n",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := tempMini(t, func(dir string) {
				writeFile(t, filepath.Join(dir, "suite.yaml"), content)
			})
			if _, err := LoadDir(dir); err == nil {
				t.Fatal("LoadDir succeeded, want error")
			}
		})
	}
}

func TestUnknownSuiteKeyIsError(t *testing.T) {
	// spec §7 spells the key defaults.timeout; a suite written with the
	// wrong key must fail loudly instead of silently defaulting to 900s.
	dir := tempMini(t, func(dir string) {
		writeFile(t, filepath.Join(dir, "suite.yaml"),
			"name: mini\nversion: \"1.0.0\"\ndefaults:\n  timeout_seconds: 5\n")
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error for unknown suite key")
	}
	for _, want := range []string{"suite.yaml", "timeout_seconds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name %s", err, want)
		}
	}
}

func TestUnknownTaskKeyIsError(t *testing.T) {
	dir := tempMini(t, func(dir string) {
		p := filepath.Join(dir, "tasks", "t1", "task.yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, string(b)+"timeout_secnds: 5\n")
	})
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded, want error for unknown task key")
	}
	for _, want := range []string{"task.yaml", "timeout_secnds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name %s", err, want)
		}
	}
}
