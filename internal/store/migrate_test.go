package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesSchemaV1(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	for _, table := range []string{
		"schema_migrations", "profiles", "profile_components", "suites",
		"tasks", "runs", "run_metrics", "run_validations", "profile_changes", "experiments",
	} {
		var name string
		err := st.DB().QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d", v)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1", n)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = st.DB().ExecContext(ctx,
		`INSERT INTO profile_components (profile_id, kind, name, hash, canonical_json)
		 VALUES ('missing', 'agent', 'build', 'h', '{}')`)
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}
