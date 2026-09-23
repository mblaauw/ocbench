package store

import (
	"database/sql"
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

// IsBusy reports whether err is a SQLite lock-contention error (SQLITE_BUSY or
// SQLITE_LOCKED). A read-only caller can treat these as a transient, retryable
// condition rather than an internal failure.
func IsBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	return isBusyCode(se.Code())
}

// isBusyCode reports whether a SQLite result code denotes lock contention.
// SQLite encodes extended result codes by OR-ing a subtype into the high bits
// of the primary code, so masking with 0xff isolates the primary code and lets
// variants such as SQLITE_BUSY_SNAPSHOT, SQLITE_BUSY_RECOVERY, and
// SQLITE_LOCKED_SHAREDCACHE be recognized as retryable.
func isBusyCode(code int) bool {
	switch code & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}
