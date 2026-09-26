package harvest_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mbl/ocbench/internal/harvest"
)

// opencodeSchema is the subset of the OpenCode database the harvester reads. It
// is created in a temp file; the tests never touch a real user database.
const opencodeSchema = `
create table project (id text primary key, worktree text not null, name text);
create table session (
  id text primary key, project_id text not null, parent_id text,
  directory text not null, title text not null, time_created integer not null);
create table message (
  id text primary key, session_id text not null, time_created integer not null, data text not null);
create table part (
  id text primary key, message_id text not null, session_id text not null,
  time_created integer not null, data text not null);
`

// seedTurn records one user turn with its text.
func seedTurn(t *testing.T, db *sql.DB, sessionID, msgID string, at time.Time, text string) {
	t.Helper()
	if _, err := db.Exec(
		`insert into message (id, session_id, time_created, data) values (?,?,?,?)`,
		msgID, sessionID, at.UnixMilli(), `{"role":"user"}`); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := db.Exec(
		`insert into part (id, message_id, session_id, time_created, data) values (?,?,?,?,?)`,
		"part-"+msgID, msgID, sessionID, at.UnixMilli(),
		`{"type":"text","text":`+jsonString(text)+`}`); err != nil {
		t.Fatalf("insert part: %v", err)
	}
}

// jsonString quotes a string as a JSON literal for embedding in a part.
func jsonString(s string) string {
	encoded, _ := jsonMarshal(s)
	return string(encoded)
}

// gitRepo creates a repository with one commit per entry, dated in order.
func gitRepo(t *testing.T, commits []struct {
	subject string
	at      time.Time
	file    string
	body    string
}) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for i, c := range commits {
		path := filepath.Join(dir, c.file)
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", c.file, err)
		}
		run("add", "-A")
		stamp := c.at.Format(time.RFC3339)
		cmd := exec.Command("git", "commit", "-q", "-m", c.subject)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit %d: %v\n%s", i, err, out)
		}
	}
	return dir
}

func TestCandidatesLinkUserTurnsToCommits(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repo := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{
		{"feat: add the parser", base.Add(10 * time.Minute), "parser.go", "package p\n"},
		{"test: cover the parser", base.Add(20 * time.Minute), "parser_test.go", "package p\n"},
		{"feat: add the cli", base.Add(70 * time.Minute), "cli.go", "package p\n"},
	})

	db := openTestDB(t)
	seedProject(t, db, "proj-1", repo, "sample")
	seedSession(t, db, "ses-1", "proj-1", "", repo, "Sample work", base)
	// Turn 1 at 12:00 spans the first two commits. Turn 2 at 12:30 has none.
	// Turn 3 at 13:00 spans the third.
	seedTurn(t, db, "ses-1", "m1", base, "Please implement the parser and cover it with a test.")
	seedAssistant(t, db, "ses-1", "a1", base.Add(25*time.Minute))
	seedTurn(t, db, "ses-1", "m2", base.Add(30*time.Minute), "thanks, looks good")
	seedAssistant(t, db, "ses-1", "a2", base.Add(35*time.Minute))
	seedTurn(t, db, "ses-1", "m3", base.Add(time.Hour), "Now add a small command line entry point please.")
	seedAssistant(t, db, "ses-1", "a3", base.Add(80*time.Minute))

	got, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db)})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2 (the turn with no commit is skipped)", len(got))
	}

	// Candidates are newest first, like `history`: recent work has the fresher
	// fixture and is more likely to be worth turning into a task.
	newest, oldest := got[0], got[1]

	if !strings.Contains(newest.Prompt, "command line entry point") {
		t.Errorf("newest prompt = %q", newest.Prompt)
	}
	if len(newest.Commits) != 1 || newest.Commits[0].Subject != "feat: add the cli" {
		t.Errorf("newest commits = %+v", newest.Commits)
	}
	if newest.TestsChanged {
		t.Error("newest candidate flagged a test change it did not have")
	}

	if !strings.Contains(oldest.Prompt, "implement the parser") {
		t.Errorf("oldest prompt = %q", oldest.Prompt)
	}
	if oldest.Repo != repo {
		t.Errorf("repo = %q, want %q", oldest.Repo, repo)
	}
	if len(oldest.Commits) != 2 {
		t.Fatalf("oldest candidate commits = %d, want 2", len(oldest.Commits))
	}
	if oldest.Commits[0].Subject != "feat: add the parser" {
		t.Errorf("oldest first commit = %q", oldest.Commits[0].Subject)
	}
	// Two commits, one of them a test file.
	if oldest.FilesChanged != 2 {
		t.Errorf("files changed = %d, want 2", oldest.FilesChanged)
	}
	if !oldest.TestsChanged {
		t.Error("a changed *_test.go file was not flagged as a test change")
	}
	// The acknowledgement was dropped, so the first turn's window ran until the
	// next substantive instruction rather than stopping at the "thanks".
	if oldest.EndedAt.Before(base.Add(time.Hour)) {
		t.Errorf("oldest window ended at %v, want it to run to the next real turn", oldest.EndedAt)
	}
}

// A turn whose text is a bare acknowledgement is not a task, even when commits
// happen to land in its window.
func TestCandidatesSkipAcknowledgements(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repo := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{{"feat: something", base.Add(5 * time.Minute), "a.go", "package p\n"}})

	db := openTestDB(t)
	seedProject(t, db, "proj-1", repo, "sample")
	seedSession(t, db, "ses-1", "proj-1", "", repo, "Sample", base)
	seedTurn(t, db, "ses-1", "m1", base, "ok")
	seedTurn(t, db, "ses-1", "m2", base.Add(10*time.Minute), "yes")

	got, err := harvest.Candidates(context.Background(), harvest.Options{
		DBPath: dbPath(t, db), MinPrompt: 20,
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("candidates = %d, want 0: acknowledgements are not tasks", len(got))
	}
}

// Only top-level sessions are harvested: a subagent session has no user turn of
// its own and would duplicate its parent's work.
func TestCandidatesIgnoreChildSessions(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repo := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{{"feat: something", base.Add(5 * time.Minute), "a.go", "package p\n"}})

	db := openTestDB(t)
	seedProject(t, db, "proj-1", repo, "sample")
	seedSession(t, db, "ses-parent", "proj-1", "", repo, "Parent", base)
	seedSession(t, db, "ses-child", "proj-1", "ses-parent", repo, "Child", base)
	seedTurn(t, db, "ses-child", "c1", base, "Do the subagent thing that is long enough.")
	seedAssistant(t, db, "ses-child", "c2", base.Add(20*time.Minute))

	got, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db)})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("candidates = %d, want 0 from a child session", len(got))
	}
}

func TestCandidatesFilterByRepo(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repoA := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{{"feat: a", base.Add(5 * time.Minute), "a.go", "package p\n"}})
	repoB := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{{"feat: b", base.Add(5 * time.Minute), "b.go", "package p\n"}})

	db := openTestDB(t)
	seedProject(t, db, "proj-a", repoA, "a")
	seedProject(t, db, "proj-b", repoB, "b")
	seedSession(t, db, "ses-a", "proj-a", "", repoA, "A", base)
	seedSession(t, db, "ses-b", "proj-b", "", repoB, "B", base)
	seedTurn(t, db, "ses-a", "a1", base, "Work on the first repository please.")
	seedAssistant(t, db, "ses-a", "a2", base.Add(20*time.Minute))
	seedTurn(t, db, "ses-b", "b1", base, "Work on the second repository please.")
	seedAssistant(t, db, "ses-b", "b2", base.Add(20*time.Minute))

	got, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db), Repo: repoB})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 || got[0].Repo != repoB {
		t.Fatalf("candidates = %+v, want only repo B", got)
	}
}

// A turn that produced a whole project is not a task: no fixture can represent
// it, so it is dropped rather than proposed.
func TestCandidatesDropOversizedTurns(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repo := gitRepo(t, []struct {
		subject string
		at      time.Time
		file    string
		body    string
	}{
		{"feat: one", base.Add(5 * time.Minute), "a.go", "package p\n"},
		{"feat: two", base.Add(6 * time.Minute), "b.go", "package p\n"},
		{"feat: three", base.Add(7 * time.Minute), "c.go", "package p\n"},
	})

	db := openTestDB(t)
	seedProject(t, db, "proj-1", repo, "sample")
	seedSession(t, db, "ses-1", "proj-1", "", repo, "Sample", base)
	seedTurn(t, db, "ses-1", "m1", base, "Build the whole thing from scratch please, all of it.")
	seedAssistant(t, db, "ses-1", "a1", base.Add(20*time.Minute))

	// Unfiltered: the turn is proposed.
	all, err := harvest.Candidates(context.Background(), harvest.Options{DBPath: dbPath(t, db)})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("unfiltered candidates = %d, want 1", len(all))
	}
	if len(all[0].Commits) != 3 || all[0].FilesChanged != 3 {
		t.Fatalf("candidate = %d commits, %d files", len(all[0].Commits), all[0].FilesChanged)
	}

	for _, tc := range []struct {
		name string
		opts harvest.Options
	}{
		{"commits", harvest.Options{MaxCommits: 2}},
		{"files", harvest.Options{MaxFiles: 2}},
		{"lines", harvest.Options{MaxLines: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.DBPath = dbPath(t, db)
			got, err := harvest.Candidates(context.Background(), opts)
			if err != nil {
				t.Fatalf("Candidates: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("candidates = %d, want 0 with a %s cap", len(got), tc.name)
			}
		})
	}

	// A cap that the candidate fits keeps it.
	fits, err := harvest.Candidates(context.Background(), harvest.Options{
		DBPath: dbPath(t, db), MaxCommits: 3, MaxFiles: 3, MaxLines: 100,
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(fits) != 1 {
		t.Errorf("candidates = %d, want 1 when the caps are not exceeded", len(fits))
	}
}
