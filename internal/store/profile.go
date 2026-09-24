package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProfileRow is one immutable profiles row.
type ProfileRow struct {
	ID              string
	ProfileHash     string
	OpenCodeVersion string
	OCBenchVersion  string
	CanonicalJSON   string
	CreatedAt       string
}

// ComponentRow is one profile_components row.
type ComponentRow struct {
	Kind          string
	Name          string
	Hash          string
	CanonicalJSON string
}

// ChangeRow is one profile_changes row. FromProfileID/ToProfileID are supplied
// separately to InsertProfileChanges.
type ChangeRow struct {
	ComponentKind string
	ComponentName string
	Change        string
	FromHash      string
	ToHash        string
	FromSummary   string
	ToSummary     string
	DetectedAt    string
}

// InsertProfile writes a profile and its components in a single transaction.
// The profile insert uses ON CONFLICT DO NOTHING so re-inserting the same id or
// profile_hash is a no-op; components are only written when the profile row
// exists afterwards (which protects the component foreign key when a different
// id already owns the hash).
func (s *Store) InsertProfile(ctx context.Context, p ProfileRow, comps []ComponentRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert profile: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profiles (id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		p.ID, p.ProfileHash, p.OpenCodeVersion, p.OCBenchVersion, p.CanonicalJSON, rfc3339UTC(p.CreatedAt)); err != nil {
		return fmt.Errorf("insert profile %s: %w", p.ID, err)
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE id = ?`, p.ID).Scan(&exists); err != nil {
		return fmt.Errorf("insert profile %s: %w", p.ID, err)
	}
	if exists > 0 {
		for _, c := range comps {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO profile_components (profile_id, kind, name, hash, canonical_json)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`,
				p.ID, c.Kind, c.Name, c.Hash, c.CanonicalJSON); err != nil {
				return fmt.Errorf("insert component %s/%s: %w", c.Kind, c.Name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert profile %s: %w", p.ID, err)
	}
	return nil
}

// GetProfileByHash returns the profile and its components. It returns a
// wrapped sql.ErrNoRows when no profile has the hash.
func (s *Store) GetProfileByHash(ctx context.Context, hash string) (*ProfileRow, []ComponentRow, error) {
	row := &ProfileRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at
		FROM profiles WHERE profile_hash = ?`, hash).
		Scan(&row.ID, &row.ProfileHash, &row.OpenCodeVersion, &row.OCBenchVersion, &row.CanonicalJSON, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("profile %s: %w", hash, sql.ErrNoRows)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get profile %s: %w", hash, err)
	}
	comps, err := s.componentsFor(ctx, row.ID)
	if err != nil {
		return nil, nil, err
	}
	return row, comps, nil
}

// GetProfileByID returns the profile and its components. It returns a wrapped
// sql.ErrNoRows when no profile has the id.
func (s *Store) GetProfileByID(ctx context.Context, id string) (*ProfileRow, []ComponentRow, error) {
	row := &ProfileRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at
		FROM profiles WHERE id = ?`, id).
		Scan(&row.ID, &row.ProfileHash, &row.OpenCodeVersion, &row.OCBenchVersion, &row.CanonicalJSON, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("profile %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get profile %s: %w", id, err)
	}
	comps, err := s.componentsFor(ctx, row.ID)
	if err != nil {
		return nil, nil, err
	}
	return row, comps, nil
}

// LatestProfile returns the most recently created profile, breaking ties by id.
// It returns a wrapped sql.ErrNoRows when the table is empty.
func (s *Store) LatestProfile(ctx context.Context) (*ProfileRow, []ComponentRow, error) {
	row := &ProfileRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at
		FROM profiles ORDER BY created_at DESC, id DESC LIMIT 1`).
		Scan(&row.ID, &row.ProfileHash, &row.OpenCodeVersion, &row.OCBenchVersion, &row.CanonicalJSON, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("latest profile: %w", sql.ErrNoRows)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("latest profile: %w", err)
	}
	comps, err := s.componentsFor(ctx, row.ID)
	if err != nil {
		return nil, nil, err
	}
	return row, comps, nil
}

// PreviousProfile returns the most recently created profile whose hash is not
// excludeHash, breaking ties by id. It is how a repeat snapshot finds the last
// genuinely different profile to diff against. It returns a wrapped
// sql.ErrNoRows when no other profile exists.
func (s *Store) PreviousProfile(ctx context.Context, excludeHash string) (*ProfileRow, []ComponentRow, error) {
	row := &ProfileRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at
		FROM profiles WHERE profile_hash != ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, excludeHash).
		Scan(&row.ID, &row.ProfileHash, &row.OpenCodeVersion, &row.OCBenchVersion, &row.CanonicalJSON, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("previous profile: %w", sql.ErrNoRows)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("previous profile: %w", err)
	}
	comps, err := s.componentsFor(ctx, row.ID)
	if err != nil {
		return nil, nil, err
	}
	return row, comps, nil
}

// InsertProfileChanges records the differences from fromID to toID in one
// transaction.
func (s *Store) InsertProfileChanges(ctx context.Context, fromID, toID string, changes []ChangeRow) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert profile changes: %w", err)
	}
	defer tx.Rollback()
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO profile_changes
				(from_profile_id, to_profile_id, component_kind, component_name, change,
				 from_hash, to_hash, from_summary, to_summary, detected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fromID, toID, c.ComponentKind, c.ComponentName, c.Change,
			c.FromHash, c.ToHash, c.FromSummary, c.ToSummary, rfc3339UTC(c.DetectedAt)); err != nil {
			return fmt.Errorf("insert profile change %s/%s: %w", c.ComponentKind, c.ComponentName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert profile changes: %w", err)
	}
	return nil
}

// ListProfileChanges returns the changes recorded against toID, ordered by
// component kind then name.
func (s *Store) ListProfileChanges(ctx context.Context, toID string) ([]ChangeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT component_kind, component_name, change,
		       COALESCE(from_hash, ''), COALESCE(to_hash, ''),
		       COALESCE(from_summary, ''), COALESCE(to_summary, ''),
		       detected_at
		FROM profile_changes
		WHERE to_profile_id = ?
		ORDER BY component_kind, component_name, id`, toID)
	if err != nil {
		return nil, fmt.Errorf("list profile changes: %w", err)
	}
	defer rows.Close()
	var out []ChangeRow
	for rows.Next() {
		var c ChangeRow
		if err := rows.Scan(&c.ComponentKind, &c.ComponentName, &c.Change,
			&c.FromHash, &c.ToHash, &c.FromSummary, &c.ToSummary, &c.DetectedAt); err != nil {
			return nil, fmt.Errorf("scan profile change: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list profile changes: %w", err)
	}
	return out, nil
}

func (s *Store) componentsFor(ctx context.Context, profileID string) ([]ComponentRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, name, hash, canonical_json
		FROM profile_components WHERE profile_id = ?
		ORDER BY kind, name`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list components for %s: %w", profileID, err)
	}
	defer rows.Close()
	var out []ComponentRow
	for rows.Next() {
		var c ComponentRow
		if err := rows.Scan(&c.Kind, &c.Name, &c.Hash, &c.CanonicalJSON); err != nil {
			return nil, fmt.Errorf("scan component: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list components for %s: %w", profileID, err)
	}
	return out, nil
}

// rfc3339UTC defaults an empty timestamp to the current UTC time.
func rfc3339UTC(ts string) string {
	if ts == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return ts
}

// ListProfiles returns every persisted profile, newest first. The dashboard
// lists all of them, including profiles that have no runs yet.
func (s *Store) ListProfiles(ctx context.Context) ([]ProfileRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile_hash, opencode_version, ocbench_version, canonical_json, created_at
		FROM profiles ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	var out []ProfileRow
	for rows.Next() {
		var r ProfileRow
		if err := rows.Scan(&r.ID, &r.ProfileHash, &r.OpenCodeVersion, &r.OCBenchVersion, &r.CanonicalJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	return out, nil
}
