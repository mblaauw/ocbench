package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// harvestDB builds an OpenCode-shaped database with one session whose turn
// produced one commit, and returns the database path.
func harvestDB(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	d := cliTestDeps(t)
	_ = d

	// Reuse the harvest package's schema through a tiny sqlite program is not
	// possible here, so the CLI test builds its database with the store's
	// driver directly.
	db := openHarvestDB(t, path)
	t.Cleanup(func() { db.Close() })

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `insert into project (id, worktree, name) values ('p','`+repo+`','r')`)
	mustExec(t, db, `insert into session (id, project_id, parent_id, directory, title, time_created)
		values ('s','p',NULL,'`+repo+`','Work','`+ms(base)+`')`)
	mustExec(t, db, `insert into message (id, session_id, time_created, data) values
		('m1','s','`+ms(base)+`','{"role":"user"}')`)
	mustExec(t, db, `insert into part (id, message_id, session_id, time_created, data) values
		('p1','m1','s','`+ms(base)+`','{"type":"text","text":"Please add a small helper function to the package."}')`)
	mustExec(t, db, `insert into message (id, session_id, time_created, data) values
		('m2','s','`+ms(base.Add(20*time.Minute))+`','{"role":"assistant"}')`)
	return path
}

func TestHarvestListsCandidatesAndWritesNothing(t *testing.T) {
	repo := harvestGitRepo(t)
	db := harvestDB(t, repo)

	d := cliTestDeps(t)
	cmd := newHarvestCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--db", db})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("harvest: %v", err)
	}
	got := out.String()
	for _, want := range []string{"add a small helper function", "COMMITS", "read-only", "Nothing was written"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestHarvestJSONAndMissingDatabase(t *testing.T) {
	repo := harvestGitRepo(t)
	db := harvestDB(t, repo)

	d := cliTestDeps(t)
	cmd := newHarvestCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", db, "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("harvest --json: %v", err)
	}
	var got struct {
		Database   string `json:"database"`
		Candidates []struct {
			Prompt       string `json:"prompt"`
			FilesChanged int    `json:"files_changed"`
			Commits      []struct {
				Subject string `json:"subject"`
			} `json:"commits"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if got.Database != db || len(got.Candidates) != 1 {
		t.Fatalf("envelope = %+v", got)
	}
	if len(got.Candidates[0].Commits) != 1 || got.Candidates[0].Commits[0].Subject != "feat: helper" {
		t.Errorf("commits = %+v", got.Candidates[0].Commits)
	}

	// A missing database is a usage error naming the path, not a crash.
	cmd = newHarvestCmd(d)
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", filepath.Join(t.TempDir(), "absent.db")})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("missing database = nil error")
	} else if !strings.Contains(err.Error(), "no OpenCode database") {
		t.Errorf("error = %v", err)
	}
}

// harvestGitRepo creates a repository with one commit dated inside the session.
func harvestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	identity := []string{
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	}
	run(identity, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(identity, "add", "-A")
	stamp := base.Add(5 * time.Minute).Format(time.RFC3339)
	run(append(identity, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp),
		"commit", "-q", "-m", "feat: helper")
	return dir
}
