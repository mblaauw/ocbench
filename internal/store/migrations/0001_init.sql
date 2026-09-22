-- Schema v1. Applied in a single transaction by the embedded migration
-- runner. Foreign key enforcement is configured on the connection (see
-- store.go), not here. schema_migrations is created by Store.Migrate.
CREATE TABLE profiles (
  id TEXT PRIMARY KEY,
  profile_hash TEXT NOT NULL UNIQUE,
  opencode_version TEXT NOT NULL,
  ocbench_version TEXT NOT NULL,
  canonical_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE profile_components (
  profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  hash TEXT NOT NULL,
  canonical_json TEXT NOT NULL,
  PRIMARY KEY (profile_id, kind, name)
);

CREATE TABLE suites (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  hash TEXT NOT NULL,
  source TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE (name, version, hash)
);

CREATE TABLE tasks (
  suite_id TEXT NOT NULL REFERENCES suites(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL,
  version TEXT NOT NULL,
  name TEXT NOT NULL,
  tags_json TEXT NOT NULL,
  timeout_seconds INTEGER NOT NULL,
  fixture_sha TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  PRIMARY KEY (suite_id, task_id, version)
);

CREATE TABLE experiments (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  experiment_id TEXT REFERENCES experiments(id),
  repeat_index INTEGER NOT NULL DEFAULT 0,
  profile_id TEXT NOT NULL REFERENCES profiles(id),
  profile_hash TEXT NOT NULL,
  suite_id TEXT REFERENCES suites(id),
  suite_name TEXT NOT NULL,
  suite_version TEXT NOT NULL,
  suite_hash TEXT NOT NULL,
  task_id TEXT NOT NULL,
  task_version TEXT NOT NULL,
  fixture_sha TEXT NOT NULL,
  opencode_version TEXT NOT NULL,
  ocbench_version TEXT NOT NULL,
  model TEXT, agent TEXT, variant TEXT,
  status TEXT NOT NULL,
  dry_run INTEGER NOT NULL DEFAULT 0,
  exit_code INTEGER,
  session_id TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  duration_ms INTEGER,
  artifacts_dir TEXT NOT NULL,
  error TEXT
);

CREATE TABLE run_metrics (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value_num REAL,
  value_text TEXT,
  unit TEXT,
  PRIMARY KEY (run_id, name)
);

CREATE TABLE run_validations (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  command TEXT,
  status TEXT NOT NULL,
  exit_code INTEGER,
  duration_ms INTEGER,
  output_path TEXT,
  output_excerpt TEXT,
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE profile_changes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  from_profile_id TEXT NOT NULL REFERENCES profiles(id),
  to_profile_id TEXT NOT NULL REFERENCES profiles(id),
  component_kind TEXT NOT NULL,
  component_name TEXT NOT NULL,
  change TEXT NOT NULL,
  from_hash TEXT, to_hash TEXT,
  from_summary TEXT, to_summary TEXT,
  detected_at TEXT NOT NULL
);
