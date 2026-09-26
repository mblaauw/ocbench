// Package harvest proposes benchmark tasks from a user's own OpenCode history.
//
// It reads a session database read-only and pairs each user turn with the
// commits that landed in that turn's window, because that pair is exactly what
// a task needs: what the human asked for, and the change that answered it.
//
// It proposes; it never publishes. A harvested task is built from a private
// repository, so the caller decides where it is written.
package harvest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultMinPrompt is the shortest user turn considered a task. A turn like
// "ok" or "continue" is an acknowledgement, and pairing one with whatever
// commit happened to land in its window would produce a task whose prompt does
// not describe the work.
const DefaultMinPrompt = 24

// Options controls a harvest.
type Options struct {
	// DBPath is the OpenCode session database. It is opened read-only.
	DBPath string
	// Repo limits the harvest to one repository worktree ("" for all).
	Repo string
	// MinPrompt overrides DefaultMinPrompt when positive.
	MinPrompt int
	// Limit caps the number of candidates returned (0 for no cap).
	Limit int
	// MaxCommits, MaxFiles and MaxLines drop candidates too large to be one
	// task. A turn that produced 39 commits across 2219 files is a project,
	// not a task, and no fixture can represent it. Zero means no cap.
	MaxCommits int
	MaxFiles   int
	MaxLines   int
}

// Commit is one commit that landed in a user turn's window.
type Commit struct {
	Hash    string
	Subject string
	At      time.Time
}

// Candidate is one user turn that produced at least one commit.
type Candidate struct {
	SessionID    string
	SessionTitle string
	Repo         string
	Prompt       string
	AskedAt      time.Time
	// EndedAt is the start of the next user turn, which bounds the work.
	EndedAt time.Time
	Commits []Commit

	FilesChanged int
	LinesAdded   int
	LinesRemoved int
	// TestsChanged is true when any changed path looks like a test file. A task
	// whose work included tests can often be graded by them.
	TestsChanged bool
}

// Candidates pairs user turns with the commits that followed them.
func Candidates(ctx context.Context, opts Options) ([]Candidate, error) {
	if opts.DBPath == "" {
		return nil, fmt.Errorf("harvest: no database path")
	}
	if opts.MinPrompt <= 0 {
		opts.MinPrompt = DefaultMinPrompt
	}

	// Read-only: a harvest must not be able to disturb the history it reads.
	db, err := sql.Open("sqlite", "file:"+opts.DBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", opts.DBPath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	sessions, err := loadSessions(ctx, db, opts.Repo)
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, s := range sessions {
		turns, err := loadTurns(ctx, db, s.id, opts.MinPrompt)
		if err != nil {
			return nil, err
		}
		if len(turns) == 0 {
			continue
		}
		commits, err := repoCommits(ctx, s.repo)
		if err != nil {
			// A worktree that is no longer a repository cannot be harvested,
			// and that is not an error in the history.
			continue
		}
		for i, turn := range turns {
			end := s.lastSeen
			if i+1 < len(turns) {
				end = turns[i+1].at
			}
			inWindow := commitsIn(commits, turn.at, end)
			if len(inWindow) == 0 {
				continue
			}
			c := Candidate{
				SessionID:    s.id,
				SessionTitle: s.title,
				Repo:         s.repo,
				Prompt:       turn.text,
				AskedAt:      turn.at,
				EndedAt:      end,
				Commits:      inWindow,
			}
			if err := attachStats(ctx, &c); err != nil {
				// Stats are a convenience; a candidate without them is still
				// worth proposing, and an unmeasurable one is not filtered by
				// size because its size is unknown.
				c.FilesChanged, c.LinesAdded, c.LinesRemoved, c.TestsChanged = 0, 0, 0, false
			} else if exceedsSize(c, opts) {
				continue
			}
			out = append(out, c)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AskedAt.After(out[j].AskedAt) })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// exceedsSize reports whether a candidate is too large to be a single task.
func exceedsSize(c Candidate, opts Options) bool {
	if opts.MaxCommits > 0 && len(c.Commits) > opts.MaxCommits {
		return true
	}
	if opts.MaxFiles > 0 && c.FilesChanged > opts.MaxFiles {
		return true
	}
	if opts.MaxLines > 0 && c.LinesAdded+c.LinesRemoved > opts.MaxLines {
		return true
	}
	return false
}

// sessionRow is one top-level session in a repository.
type sessionRow struct {
	id       string
	title    string
	repo     string
	lastSeen time.Time
}

// loadSessions returns top-level sessions in a git worktree, newest first. A
// child session has no user turn of its own: its work belongs to its parent.
func loadSessions(ctx context.Context, db *sql.DB, repo string) ([]sessionRow, error) {
	query := `
		select s.id, s.title, p.worktree, max(m.time_created)
		from session s
		join project p on p.id = s.project_id
		left join message m on m.session_id = s.id
		where s.parent_id is null and p.worktree <> ''
		group by s.id
		order by max(m.time_created) desc`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	defer rows.Close()

	var out []sessionRow
	for rows.Next() {
		var (
			s        sessionRow
			lastSeen sql.NullInt64
		)
		if err := rows.Scan(&s.id, &s.title, &s.repo, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if repo != "" && s.repo != repo {
			continue
		}
		if lastSeen.Valid {
			s.lastSeen = time.UnixMilli(lastSeen.Int64).UTC()
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// turn is one user message and its text.
type turn struct {
	at   time.Time
	text string
}

// loadTurns returns the user turns of a session, oldest first, with turns
// shorter than minPrompt dropped.
func loadTurns(ctx context.Context, db *sql.DB, sessionID string, minPrompt int) ([]turn, error) {
	rows, err := db.QueryContext(ctx, `
		select m.id, m.time_created
		from message m
		where m.session_id = ? and json_extract(m.data, '$.role') = 'user'
		order by m.time_created`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read turns: %w", err)
	}
	defer rows.Close()

	type msg struct {
		id string
		at time.Time
	}
	var msgs []msg
	for rows.Next() {
		var (
			id string
			ts int64
		)
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, fmt.Errorf("scan turn: %w", err)
		}
		msgs = append(msgs, msg{id: id, at: time.UnixMilli(ts).UTC()})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []turn
	for _, m := range msgs {
		text, err := turnText(ctx, db, m.id)
		if err != nil {
			return nil, err
		}
		if len([]rune(text)) < minPrompt {
			continue
		}
		out = append(out, turn{at: m.at, text: text})
	}
	return out, nil
}

// turnText joins the text parts of one user message.
func turnText(ctx context.Context, db *sql.DB, messageID string) (string, error) {
	rows, err := db.QueryContext(ctx, `
		select json_extract(data, '$.text')
		from part
		where message_id = ? and json_extract(data, '$.type') = 'text'
		order by time_created`, messageID)
	if err != nil {
		return "", fmt.Errorf("read parts: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var text sql.NullString
		if err := rows.Scan(&text); err != nil {
			return "", fmt.Errorf("scan part: %w", err)
		}
		if text.Valid && strings.TrimSpace(text.String) != "" {
			parts = append(parts, strings.TrimSpace(text.String))
		}
	}
	return strings.Join(parts, "\n\n"), rows.Err()
}

// repoCommits reads the repository's history, oldest first.
func repoCommits(ctx context.Context, repo string) ([]Commit, error) {
	// %ct is the committer time in seconds since the epoch, so no timezone
	// arithmetic is needed to compare it with the database's millisecond
	// stamps.
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "log",
		"--no-merges", "--format=%ct%x1f%H%x1f%s", "--reverse")
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log in %s: %w", repo, err)
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		seconds, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, Commit{
			Hash: fields[1], Subject: fields[2],
			At: time.Unix(seconds, 0).UTC(),
		})
	}
	return commits, nil
}

// commitsIn returns the commits whose time falls in [from, to).
func commitsIn(commits []Commit, from, to time.Time) []Commit {
	var out []Commit
	for _, c := range commits {
		if c.At.Before(from) {
			continue
		}
		if !to.IsZero() && !c.At.Before(to) {
			break
		}
		out = append(out, c)
	}
	return out
}

// attachStats sums the changed files and lines across a candidate's commits.
func attachStats(ctx context.Context, c *Candidate) error {
	seen := map[string]bool{}
	for _, commit := range c.Commits {
		cmd := exec.CommandContext(ctx, "git", "-C", c.Repo, "show",
			"--numstat", "--format=", commit.Hash)
		cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("git show %s: %w", commit.Hash, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) != 3 {
				continue
			}
			path := fields[2]
			if !seen[path] {
				seen[path] = true
				c.FilesChanged++
				if looksLikeTest(path) {
					c.TestsChanged = true
				}
			}
			// A binary file reports "-" rather than a count.
			if added, err := strconv.Atoi(fields[0]); err == nil {
				c.LinesAdded += added
			}
			if removed, err := strconv.Atoi(fields[1]); err == nil {
				c.LinesRemoved += removed
			}
		}
	}
	return nil
}

// looksLikeTest reports whether a path is conventionally a test file.
func looksLikeTest(path string) bool {
	lower := strings.ToLower(path)
	base := lower
	if i := strings.LastIndex(lower, "/"); i >= 0 {
		base = lower[i+1:]
	}
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.HasSuffix(base, ".test.js"),
		strings.HasSuffix(base, ".test.ts"),
		strings.HasSuffix(base, ".spec.js"),
		strings.HasSuffix(base, ".spec.ts"),
		strings.HasPrefix(base, "test_"),
		strings.HasSuffix(base, "_test.py"):
		return true
	}
	return strings.Contains(lower, "/tests/") || strings.Contains(lower, "/test/")
}

// MarshalJSON keeps the JSON shape stable for the CLI's --json output.
func (c Candidate) MarshalJSON() ([]byte, error) {
	type commitJSON struct {
		Hash    string `json:"hash"`
		Subject string `json:"subject"`
		At      string `json:"at"`
	}
	type candidateJSON struct {
		SessionID    string       `json:"session_id"`
		SessionTitle string       `json:"session_title"`
		Repo         string       `json:"repo"`
		Prompt       string       `json:"prompt"`
		AskedAt      string       `json:"asked_at"`
		EndedAt      string       `json:"ended_at"`
		Commits      []commitJSON `json:"commits"`
		FilesChanged int          `json:"files_changed"`
		LinesAdded   int          `json:"lines_added"`
		LinesRemoved int          `json:"lines_removed"`
		TestsChanged bool         `json:"tests_changed"`
	}
	out := candidateJSON{
		SessionID: c.SessionID, SessionTitle: c.SessionTitle, Repo: c.Repo,
		Prompt: c.Prompt, AskedAt: c.AskedAt.Format(time.RFC3339),
		EndedAt:      c.EndedAt.Format(time.RFC3339),
		FilesChanged: c.FilesChanged, LinesAdded: c.LinesAdded,
		LinesRemoved: c.LinesRemoved, TestsChanged: c.TestsChanged,
	}
	out.Commits = make([]commitJSON, 0, len(c.Commits))
	for _, cm := range c.Commits {
		out.Commits = append(out.Commits, commitJSON{
			Hash: cm.Hash, Subject: cm.Subject, At: cm.At.Format(time.RFC3339),
		})
	}
	return json.Marshal(out)
}
