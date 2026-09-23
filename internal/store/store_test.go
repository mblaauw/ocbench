package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestOpenReturnsUsableStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if st.DB() == nil {
		t.Fatal("DB() = nil")
	}
	if err := st.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIsBusyCodeRecognizesBaseAndExtendedCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
		want bool
	}{
		{"busy", sqlite3.SQLITE_BUSY, true},
		{"busy_recovery", sqlite3.SQLITE_BUSY_RECOVERY, true},
		{"busy_snapshot", sqlite3.SQLITE_BUSY_SNAPSHOT, true},
		{"busy_timeout", sqlite3.SQLITE_BUSY_TIMEOUT, true},
		{"locked", sqlite3.SQLITE_LOCKED, true},
		{"locked_sharedcache", sqlite3.SQLITE_LOCKED_SHAREDCACHE, true},
		{"locked_vtab", sqlite3.SQLITE_LOCKED_VTAB, true},
		{"ok", sqlite3.SQLITE_OK, false},
		{"error", sqlite3.SQLITE_ERROR, false},
		{"constraint", sqlite3.SQLITE_CONSTRAINT, false},
		{"ioerr_read", sqlite3.SQLITE_IOERR | (1 << 8), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBusyCode(tt.code); got != tt.want {
				t.Errorf("isBusyCode(%d) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsBusyIgnoresNonSQLiteErrors(t *testing.T) {
	if IsBusy(nil) {
		t.Error("IsBusy(nil) = true, want false")
	}
	if IsBusy(errors.New("connection reset")) {
		t.Error("IsBusy(plain error) = true, want false")
	}
}
