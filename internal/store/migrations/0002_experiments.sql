-- Schema v2. Applied in a single transaction by the embedded migration
-- runner after 0001_init.sql. arms are the profile/overlay variants of an
-- experiment; runs may be attributed to one arm. arm_id is nullable with no
-- default so SQLite can ADD COLUMN ... REFERENCES with foreign keys enabled.
CREATE TABLE experiment_arms (
  id TEXT PRIMARY KEY,
  experiment_id TEXT NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  profile_id TEXT REFERENCES profiles(id),
  profile_hash TEXT NOT NULL,
  overlay_kind TEXT NOT NULL,
  overlay_path TEXT,
  overlay_sha256 TEXT,
  created_at TEXT NOT NULL,
  UNIQUE (experiment_id, label)
);

ALTER TABLE runs ADD COLUMN arm_id TEXT REFERENCES experiment_arms(id);
CREATE INDEX runs_arm_idx ON runs (arm_id);
