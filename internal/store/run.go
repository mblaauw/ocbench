package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SuiteRow is one immutable suites row. ID is content-addressed (the suite
// hash), so re-loading the same suite content is idempotent.
type SuiteRow struct {
	ID           string
	Name         string
	Version      string
	Hash         string
	Source       string
	ManifestJSON string
	CreatedAt    string
}

// TaskRow is one tasks row, keyed by (suite_id, task_id, version).
type TaskRow struct {
	SuiteID        string
	TaskID         string
	Version        string
	Name           string
	TagsJSON       string
	TimeoutSeconds int
	FixtureSHA     string
	SpecJSON       string
}

// RunRow is one runs row. Empty optional strings are stored as SQL NULL and
// read back as ""; ExitCode and DurationMS are pointers so "unset" is distinct
// from zero.
type RunRow struct {
	ID              string
	ExperimentID    string
	RepeatIndex     int
	ProfileID       string
	ProfileHash     string
	SuiteID         string
	SuiteName       string
	SuiteVersion    string
	SuiteHash       string
	TaskID          string
	TaskVersion     string
	FixtureSHA      string
	OpenCodeVersion string
	OCBenchVersion  string
	Model           string
	Agent           string
	Variant         string
	Status          string
	DryRun          bool
	ExitCode        *int
	SessionID       string
	StartedAt       string
	FinishedAt      string
	DurationMS      *int64
	ArtifactsDir    string
	Error           string
}

// ValidationRow is one run_validations row, keyed by (run_id, seq).
type ValidationRow struct {
	Seq           int
	Kind          string
	Name          string
	Command       string
	Status        string
	ExitCode      int
	DurationMS    int64
	OutputPath    string
	OutputExcerpt string
}

// ExperimentRow is one experiments row. The CLI creates exactly one per
// `ocbench run` invocation; ID is a fresh UUID.
type ExperimentRow struct {
	ID        string
	Name      string
	SpecJSON  string
	CreatedAt string
}

// InsertExperiment writes an experiments row in one transaction. An existing
// row with the same id is left untouched, so a retried insert is idempotent.
func (s *Store) InsertExperiment(ctx context.Context, row ExperimentRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert experiment: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO experiments (id, name, spec_json, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		row.ID, row.Name, row.SpecJSON, rfc3339UTC(row.CreatedAt)); err != nil {
		return fmt.Errorf("insert experiment %s: %w", row.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert experiment %s: %w", row.ID, err)
	}
	return nil
}

// InsertSuite upserts a suite in one transaction. An existing row for the same
// id or (name, version, hash) is left untouched.
func (s *Store) InsertSuite(ctx context.Context, row SuiteRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert suite: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO suites (id, name, version, hash, source, manifest_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		row.ID, row.Name, row.Version, row.Hash, row.Source, row.ManifestJSON, rfc3339UTC(row.CreatedAt)); err != nil {
		return fmt.Errorf("insert suite %s: %w", row.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert suite %s: %w", row.ID, err)
	}
	return nil
}

// InsertTask upserts a task in one transaction. It requires the suite row to
// exist (foreign key).
func (s *Store) InsertTask(ctx context.Context, row TaskRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (suite_id, task_id, version, name, tags_json, timeout_seconds, fixture_sha, spec_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		row.SuiteID, row.TaskID, row.Version, row.Name, row.TagsJSON, row.TimeoutSeconds, row.FixtureSHA, row.SpecJSON); err != nil {
		return fmt.Errorf("insert task %s/%s: %w", row.SuiteID, row.TaskID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert task %s/%s: %w", row.SuiteID, row.TaskID, err)
	}
	return nil
}

// InsertRun writes a run row in one transaction.
func (s *Store) InsertRun(ctx context.Context, row RunRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runs (
			id, experiment_id, repeat_index, profile_id, profile_hash,
			suite_id, suite_name, suite_version, suite_hash,
			task_id, task_version, fixture_sha, opencode_version, ocbench_version,
			model, agent, variant, status, dry_run, exit_code, session_id,
			started_at, finished_at, duration_ms, artifacts_dir, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, nullString(row.ExperimentID), row.RepeatIndex, row.ProfileID, row.ProfileHash,
		nullString(row.SuiteID), row.SuiteName, row.SuiteVersion, row.SuiteHash,
		row.TaskID, row.TaskVersion, row.FixtureSHA, row.OpenCodeVersion, row.OCBenchVersion,
		nullString(row.Model), nullString(row.Agent), nullString(row.Variant), row.Status, boolInt(row.DryRun),
		nullInt(row.ExitCode), nullString(row.SessionID), rfc3339UTC(row.StartedAt), nullString(row.FinishedAt),
		nullInt64(row.DurationMS), row.ArtifactsDir, nullString(row.Error)); err != nil {
		return fmt.Errorf("insert run %s: %w", row.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert run %s: %w", row.ID, err)
	}
	return nil
}

// InsertRunMetrics upserts every metric of a run in one transaction. An
// existing (run_id, name) is updated in place, so a re-normalised run replaces
// stale values.
func (s *Store) InsertRunMetrics(ctx context.Context, runID string, metrics map[string]float64) error {
	if len(metrics) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert run metrics: %w", err)
	}
	defer tx.Rollback()

	for name, value := range metrics {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_metrics (run_id, name, value_num, value_text, unit)
			VALUES (?, ?, ?, NULL, NULL)
			ON CONFLICT(run_id, name) DO UPDATE SET value_num = excluded.value_num`,
			runID, name, value); err != nil {
			return fmt.Errorf("insert run metric %s/%s: %w", runID, name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert run metrics: %w", err)
	}
	return nil
}

// InsertRunValidations upserts every validation of a run in one transaction,
// keyed by seq so a re-run replaces stale rows.
func (s *Store) InsertRunValidations(ctx context.Context, runID string, vals []ValidationRow) error {
	if len(vals) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert run validations: %w", err)
	}
	defer tx.Rollback()

	for _, v := range vals {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_validations
				(run_id, seq, kind, name, command, status, exit_code, duration_ms, output_path, output_excerpt)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(run_id, seq) DO UPDATE SET
				kind = excluded.kind, name = excluded.name, command = excluded.command,
				status = excluded.status, exit_code = excluded.exit_code,
				duration_ms = excluded.duration_ms, output_path = excluded.output_path,
				output_excerpt = excluded.output_excerpt`,
			runID, v.Seq, v.Kind, v.Name, nullString(v.Command), v.Status, v.ExitCode, v.DurationMS,
			nullString(v.OutputPath), nullString(v.OutputExcerpt)); err != nil {
			return fmt.Errorf("insert run validation %s/%d: %w", runID, v.Seq, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert run validations: %w", err)
	}
	return nil
}

// GetRun returns a run by id, or a wrapped sql.ErrNoRows when absent.
func (s *Store) GetRun(ctx context.Context, id string) (*RunRow, error) {
	row := &RunRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(experiment_id, ''), repeat_index, profile_id, profile_hash,
		       COALESCE(suite_id, ''), suite_name, suite_version, suite_hash,
		       task_id, task_version, fixture_sha, opencode_version, ocbench_version,
		       COALESCE(model, ''), COALESCE(agent, ''), COALESCE(variant, ''),
		       status, dry_run, exit_code, COALESCE(session_id, ''),
		       started_at, COALESCE(finished_at, ''), duration_ms,
		       artifacts_dir, COALESCE(error, '')
		FROM runs WHERE id = ?`, id).
		Scan(&row.ID, &row.ExperimentID, &row.RepeatIndex, &row.ProfileID, &row.ProfileHash,
			&row.SuiteID, &row.SuiteName, &row.SuiteVersion, &row.SuiteHash,
			&row.TaskID, &row.TaskVersion, &row.FixtureSHA, &row.OpenCodeVersion, &row.OCBenchVersion,
			&row.Model, &row.Agent, &row.Variant, &row.Status, &row.DryRun, &row.ExitCode, &row.SessionID,
			&row.StartedAt, &row.FinishedAt, &row.DurationMS, &row.ArtifactsDir, &row.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("get run %s: %w", id, err)
	}
	return row, nil
}

// ListRuns returns runs ordered newest first (started_at, then id). taskID
// filters by task when non-empty; a limit <= 0 returns every match.
func (s *Store) ListRuns(ctx context.Context, limit int, taskID string) ([]RunRow, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(experiment_id, ''), repeat_index, profile_id, profile_hash,
		       COALESCE(suite_id, ''), suite_name, suite_version, suite_hash,
		       task_id, task_version, fixture_sha, opencode_version, ocbench_version,
		       COALESCE(model, ''), COALESCE(agent, ''), COALESCE(variant, ''),
		       status, dry_run, exit_code, COALESCE(session_id, ''),
		       started_at, COALESCE(finished_at, ''), duration_ms,
		       artifacts_dir, COALESCE(error, '')
		FROM runs
		WHERE (? = '' OR task_id = ?)
		ORDER BY started_at DESC, id DESC
		LIMIT ?`, taskID, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var out []RunRow
	for rows.Next() {
		var row RunRow
		if err := rows.Scan(&row.ID, &row.ExperimentID, &row.RepeatIndex, &row.ProfileID, &row.ProfileHash,
			&row.SuiteID, &row.SuiteName, &row.SuiteVersion, &row.SuiteHash,
			&row.TaskID, &row.TaskVersion, &row.FixtureSHA, &row.OpenCodeVersion, &row.OCBenchVersion,
			&row.Model, &row.Agent, &row.Variant, &row.Status, &row.DryRun, &row.ExitCode, &row.SessionID,
			&row.StartedAt, &row.FinishedAt, &row.DurationMS, &row.ArtifactsDir, &row.Error); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return out, nil
}

// nullString maps "" to SQL NULL and any other value to itself.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullInt maps a nil pointer to SQL NULL and a value to itself.
func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullInt64 maps a nil pointer to SQL NULL and a value to itself.
func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// boolInt renders a bool as the 0/1 SQLite stores.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
