-- Schema v3 records the execution environment without a hostname or machine
-- identifier. Older runs remain explicitly unknown rather than being guessed.
ALTER TABLE runs ADD COLUMN runner_env TEXT NOT NULL DEFAULT '';
