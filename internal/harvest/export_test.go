package harvest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mbl/ocbench/internal/harvest"
)

// exportRepo builds a repository whose second commit implements a stub and adds
// the test that proves it, which is the shape a harvested task needs.
func exportRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// The scaffold commit precedes the turn (the repository has history); the
	// implementation commit falls inside the turn's window, which is what pairs
	// it with the request. Only commits carry a date, so the clock is passed in
	// rather than advanced by every git call.
	identity := []string{
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	}
	runAt := func(at time.Time, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		env := append(os.Environ(), identity...)
		if !at.IsZero() {
			stamp := at.Format(time.RFC3339)
			env = append(env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		}
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run := func(args ...string) { runAt(time.Time{}, args...) }

	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")

	// The starting state: a stub and a module file, committed before the turn.
	write("go.mod", "module sample\n\ngo 1.21\n")
	write("calc.go", "package sample\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn 0\n}\n")
	run("add", "-A")
	runAt(time.Date(2026, 3, 1, 11, 55, 0, 0, time.UTC), "commit", "-q", "-m", "feat: scaffold the calculator")

	// The work: the implementation, and the test that proves it.
	write("calc.go", "package sample\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	write("calc_test.go", "package sample\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2,3) = %d, want 5\", got)\n\t}\n}\n")
	run("add", "-A")
	runAt(time.Date(2026, 3, 1, 12, 5, 0, 0, time.UTC), "commit", "-q", "-m", "feat: implement addition")
	return dir
}

// goTest runs the fixture's test command and reports whether it passed.
func goTest(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	return cmd.Run() == nil
}

func TestExportWritesATaskThatFailsThenPasses(t *testing.T) {
	repo := exportRepo(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	db := openTestDB(t)
	seedProject(t, db, "p1", repo, "sample")
	seedSession(t, db, "s1", "p1", "", repo, "Calculator work", base)
	seedTurn(t, db, "s1", "m1", base, "Please implement the addition function properly.")
	seedAssistant(t, db, "s1", "a1", base.Add(30*time.Minute))

	got, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db)})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}

	dest := t.TempDir()
	taskDir, verification, err := harvest.Export(context.Background(), got[0], dest)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// Export checks the honesty property itself rather than claiming it.
	if !verification.Checked {
		t.Fatalf("Export did not check the task: %+v", verification)
	}
	if !verification.OK() {
		t.Fatalf("Export produced a task that is not honest: %+v", verification)
	}

	// The layout matches a suite task.
	for _, rel := range []string{
		"task.yaml", "prompt.md",
		"fixture/go.mod", "fixture/calc.go", "fixture/calc_test.go",
		"evaluator/reference/calc.go",
	} {
		if _, err := os.Stat(filepath.Join(taskDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// The fixture carries the stub AND the new test.
	fixtureCalc, err := os.ReadFile(filepath.Join(taskDir, "fixture", "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixtureCalc), "return 0") {
		t.Errorf("fixture calc.go is not the stub:\n%s", fixtureCalc)
	}
	fixtureTest, err := os.ReadFile(filepath.Join(taskDir, "fixture", "calc_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixtureTest), "want 5") {
		t.Errorf("fixture is missing the test that proves the work:\n%s", fixtureTest)
	}

	// The reference carries the implementation, not the test.
	refCalc, err := os.ReadFile(filepath.Join(taskDir, "evaluator", "reference", "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(refCalc), "return a + b") {
		t.Errorf("reference calc.go is not the implementation:\n%s", refCalc)
	}
	if _, err := os.Stat(filepath.Join(taskDir, "evaluator", "reference", "calc_test.go")); err == nil {
		t.Error("the test was put in the reference; it belongs in the fixture")
	}

	// The inferred validator names the fixture's own test command.
	task, err := os.ReadFile(filepath.Join(taskDir, "task.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task), `"go", "test", "./..."`) {
		t.Errorf("task.yaml did not infer the Go test command:\n%s", task)
	}
	if !strings.Contains(string(task), "tags: [harvested]") {
		t.Errorf("task.yaml does not mark itself harvested:\n%s", task)
	}

	// The prompt is a scaffold, and it keeps the answer out of the prompt text.
	prompt, err := os.ReadFile(filepath.Join(taskDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TODO: state the task", "implement the addition function", "do not put it in the prompt"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("prompt.md missing %q", want)
		}
	}

	// The property every task is held to: the fixture fails, and the reference
	// makes it pass. This is what makes the export worth having.
	if goTest(t, filepath.Join(taskDir, "fixture")) {
		t.Fatal("the fixture passes untouched: the task cannot show anything")
	}
	applyReference(t, taskDir)
	if !goTest(t, filepath.Join(taskDir, "fixture")) {
		t.Fatal("the fixture still fails with the reference applied")
	}
}

// applyReference copies evaluator/reference over the fixture, which is what the
// honesty check does.
func applyReference(t *testing.T, taskDir string) {
	t.Helper()
	ref := filepath.Join(taskDir, "evaluator", "reference")
	err := filepath.Walk(ref, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(ref, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(taskDir, "fixture", rel), content, 0o644)
	})
	if err != nil {
		t.Fatalf("apply reference: %v", err)
	}
}

func TestExportRefusesACandidateWithoutAStartingState(t *testing.T) {
	// A repository whose only commit is the work itself has no parent to build
	// a fixture from.
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := base.Format(time.RFC3339)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "feat: first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@e.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@e.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}

	db := openTestDB(t)
	seedProject(t, db, "p1", dir, "root")
	seedSession(t, db, "s1", "p1", "", dir, "Root", base)
	seedTurn(t, db, "s1", "m1", base, "Create the whole project from nothing at all.")
	seedAssistant(t, db, "s1", "a1", base.Add(10*time.Minute))

	got, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db)})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	_, _, err = harvest.Export(context.Background(), got[0], t.TempDir())
	if err == nil {
		t.Fatal("Export accepted a root commit, want an error naming the missing parent")
	}
	if !strings.Contains(err.Error(), "no starting state") {
		t.Errorf("error = %v", err)
	}
}
