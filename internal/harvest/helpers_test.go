package harvest_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB creates an OpenCode-shaped database in a temp file. The tests
// never read a real user database.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(opencodeSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// dbPath is the file the harness should read.
func dbPath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var path string
	if err := db.QueryRow(`select file from pragma_database_list where name='main'`).Scan(&path); err != nil {
		t.Fatalf("database path: %v", err)
	}
	return path
}

func seedProject(t *testing.T, db *sql.DB, id, worktree, name string) {
	t.Helper()
	if _, err := db.Exec(`insert into project (id, worktree, name) values (?,?,?)`, id, worktree, name); err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func seedSession(t *testing.T, db *sql.DB, id, projectID, parentID, directory, title string, at time.Time) {
	t.Helper()
	var parent any
	if parentID != "" {
		parent = parentID
	}
	if _, err := db.Exec(
		`insert into session (id, project_id, parent_id, directory, title, time_created) values (?,?,?,?,?,?)`,
		id, projectID, parent, directory, title, at.UnixMilli()); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// seedAssistant records an assistant reply, which is what bounds a turn's work
// window in a real session: the agent's final message comes after its commits.
func seedAssistant(t *testing.T, db *sql.DB, sessionID, msgID string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(
		`insert into message (id, session_id, time_created, data) values (?,?,?,?)`,
		msgID, sessionID, at.UnixMilli(), `{"role":"assistant"}`); err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	if _, err := db.Exec(
		`insert into part (id, message_id, session_id, time_created, data) values (?,?,?,?,?)`,
		"part-"+msgID, msgID, sessionID, at.UnixMilli(), `{"type":"text","text":"done"}`); err != nil {
		t.Fatalf("insert assistant part: %v", err)
	}
}

// jsonMarshal is a thin wrapper so the test reads as intent.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
