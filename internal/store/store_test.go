package store

import (
	"context"
	"path/filepath"
	"testing"
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
