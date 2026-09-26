package cli

import (
	"database/sql"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openHarvestDB creates an OpenCode-shaped database for the CLI tests.
func openHarvestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		create table project (id text primary key, worktree text not null, name text);
		create table session (id text primary key, project_id text not null, parent_id text,
			directory text not null, title text not null, time_created integer not null);
		create table message (id text primary key, session_id text not null,
			time_created integer not null, data text not null);
		create table part (id text primary key, message_id text not null, session_id text not null,
			time_created integer not null, data text not null);`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// ms renders a time as milliseconds since the epoch, which is how OpenCode
// stores its timestamps.
func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
