package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesCurrentSchema(t *testing.T) {
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
	if v != 3 {
		t.Fatalf("version = %d, want 3", v)
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
	var runnerEnvCol string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT name FROM pragma_table_info('runs') WHERE name = 'runner_env'`).Scan(&runnerEnvCol); err != nil {
		t.Fatalf("runs.runner_env missing: %v", err)
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
	if v != 3 {
		t.Fatalf("version = %d", v)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("schema_migrations rows = %d, want 3", n)
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
	// A real v1 store may already contain runs. The additive migrations must
	// preserve them while adding nullable arm and runner-environment columns.
	if _, err := st.DB().ExecContext(ctx, `
		INSERT INTO profiles (id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at)
		VALUES ('p1', 'hash-1', '1.18.32', 'dev', '{}', '2026-01-01T00:00:00Z');
		INSERT INTO runs (id, profile_id, profile_hash, suite_name, suite_version, suite_hash, task_id, task_version, fixture_sha, opencode_version, ocbench_version, status, started_at, artifacts_dir)
		VALUES ('run-v1', 'p1', 'hash-1', 'core', '1', 'suite', 'task', '1', 'fixture', '1.18.32', 'dev', 'passed', '2026-01-01T00:00:00Z', '/runs/run-v1')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Upgrading an existing v1 database applies the additive v2 and v3 migrations.
	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("upgrade v1 -> v3: %v", err)
	}
	if v != 3 {
		t.Fatalf("version after upgrade = %d, want 3", v)
	}
	var armCol string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT name FROM pragma_table_info('runs') WHERE name = 'arm_id'`).Scan(&armCol); err != nil {
		t.Fatalf("runs.arm_id missing after upgrade: %v", err)
	}
	var runnerEnvCol string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT name FROM pragma_table_info('runs') WHERE name = 'runner_env'`).Scan(&runnerEnvCol); err != nil {
		t.Fatalf("runs.runner_env missing after upgrade: %v", err)
	}
	var preservedID string
	if err := st.DB().QueryRowContext(ctx, `SELECT id FROM runs WHERE id = 'run-v1'`).Scan(&preservedID); err != nil || preservedID != "run-v1" {
		t.Fatalf("v1 run was not preserved: %q, %v", preservedID, err)
	}
	var runnerEnv string
	if err := st.DB().QueryRowContext(ctx, `SELECT runner_env FROM runs WHERE id = 'run-v1'`).Scan(&runnerEnv); err != nil || runnerEnv != "" {
		t.Fatalf("v1 runner environment = %q, %v, want empty default", runnerEnv, err)
	}
	// Re-running is a no-op.
	if again, err := st.Migrate(ctx); err != nil || again != 3 {
		t.Fatalf("second Migrate = %d, %v, want 3, nil", again, err)
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
