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
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	for _, table := range []string{
		"schema_migrations", "profiles", "profile_components", "suites",
		"tasks", "runs", "run_metrics", "run_validations", "profile_changes", "experiments",
		"experiment_arms",
	} {
		var name string
		err := st.DB().QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	// v2 adds runs.arm_id referencing experiment_arms(id).
	var armCol string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT name FROM pragma_table_info('runs') WHERE name = 'arm_id'`).Scan(&armCol); err != nil {
		t.Fatalf("runs.arm_id missing: %v", err)
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
	if v != 2 {
		t.Fatalf("version = %d", v)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("schema_migrations rows = %d, want 2", n)
	}
	// Re-running is a no-op: the v2 table still exists exactly once.
	var arms int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='experiment_arms'`).Scan(&arms); err != nil {
		t.Fatal(err)
	}
	if arms != 1 {
		t.Fatalf("experiment_arms count = %d, want 1", arms)
	}
}

func TestMigrateUpgradesExistingV1Database(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// Simulate a database created before v2: only 0001 is applied.
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	var applied bool
	for _, m := range migs {
		if m.version != 1 {
			continue
		}
		if _, err := st.DB().ExecContext(ctx, m.sql); err != nil {
			t.Fatalf("apply v1: %v", err)
		}
		applied = true
	}
	if !applied {
		t.Fatal("no v1 migration found")
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Upgrading an existing v1 database applies only the additive v2 migration.
	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("upgrade v1 -> v2: %v", err)
	}
	if v != 2 {
		t.Fatalf("version after upgrade = %d, want 2", v)
	}
	var armCol string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT name FROM pragma_table_info('runs') WHERE name = 'arm_id'`).Scan(&armCol); err != nil {
		t.Fatalf("runs.arm_id missing after upgrade: %v", err)
	}
	// Re-running is a no-op.
	if again, err := st.Migrate(ctx); err != nil || again != 2 {
		t.Fatalf("second Migrate = %d, %v, want 2, nil", again, err)
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
