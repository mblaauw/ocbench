package profile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/id"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/version"
)

// Persist writes the profile and its components to the store and the redacted
// raw captures under paths.Profiles/<hash>/. It is idempotent: when a profile
// with the same hash already exists it writes nothing new to the database and
// returns created=false.
func Persist(ctx context.Context, st *store.Store, paths config.Paths, p *Profile) (bool, error) {
	if st == nil {
		return false, errors.New("persist: nil store")
	}
	if p == nil {
		return false, errors.New("persist: nil profile")
	}

	created := true
	if existing, _, err := st.GetProfileByHash(ctx, p.Hash); err == nil {
		created = false
		// Persist fills in the stored id on the caller's profile, so a
		// snapshot of an already-known hash still returns a usable identity.
		if p.ID == "" {
			p.ID = existing.ID
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if created {
		profileID := p.ID
		if profileID == "" {
			generated, err := id.NewUUID()
			if err != nil {
				return false, fmt.Errorf("generate profile id: %w", err)
			}
			profileID = generated
			p.ID = profileID
		}
		row := store.ProfileRow{
			ID:              profileID,
			ProfileHash:     p.Hash,
			OpenCodeVersion: p.OpenCodeVersion,
			OCBenchVersion:  version.Info().Version,
			CanonicalJSON:   string(p.CanonicalJSON),
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		}
		comps := make([]store.ComponentRow, 0, len(p.Components))
		for _, c := range p.Components {
			comps = append(comps, store.ComponentRow{
				Kind:          c.Kind,
				Name:          c.Name,
				Hash:          c.Hash,
				CanonicalJSON: string(c.CanonicalJSON),
			})
		}
		if err := st.InsertProfile(ctx, row, comps); err != nil {
			return false, err
		}
	}

	if err := writeCaptures(paths, p); err != nil {
		return false, err
	}
	return created, nil
}

// latest returns the most recent persisted profile.
func latest(ctx context.Context, st *store.Store) (*Profile, error) {
	if st == nil {
		return nil, errors.New("latest: nil store")
	}
	row, comps, err := st.LatestProfile(ctx)
	if err != nil {
		return nil, err
	}
	return FromRows(row, comps)
}

// FromRows rebuilds a Profile from persisted rows for comparison and display.
// Snapshot is decoded from the stored canonical JSON; Captures are empty
// because captures are content-addressed files, not database rows.
func FromRows(row *store.ProfileRow, comps []store.ComponentRow) (*Profile, error) {
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(row.CanonicalJSON), &snapshot); err != nil {
		return nil, fmt.Errorf("decode profile %s: %w", row.ID, err)
	}
	p := &Profile{
		ID:              row.ID,
		Hash:            row.ProfileHash,
		OpenCodeVersion: row.OpenCodeVersion,
		CanonicalJSON:   []byte(row.CanonicalJSON),
		Snapshot:        snapshot,
	}
	for _, c := range comps {
		p.Components = append(p.Components, Component{
			Kind:          c.Kind,
			Name:          c.Name,
			Hash:          c.Hash,
			CanonicalJSON: []byte(c.CanonicalJSON),
		})
	}
	return p, nil
}

// writeCaptures writes the content-addressed capture files. All payloads were
// produced by Fingerprint (redacted and path-normalised where applicable).
// A profile loaded from the database (Latest/GetProfileByHash) has no captures;
// in that case existing capture files are left untouched rather than
// overwritten with empty placeholders.
func writeCaptures(paths config.Paths, p *Profile) error {
	if paths.Profiles == "" {
		return errors.New("persist: empty profiles path")
	}
	type captureFile struct {
		name string
		data []byte
	}
	var files []captureFile
	if len(p.Captures.ResolvedConfig) > 0 {
		files = append(files, captureFile{"resolved-config.json", p.Captures.ResolvedConfig})
	}
	if len(p.Captures.Skills) > 0 {
		files = append(files, captureFile{"skills.json", p.Captures.Skills})
	}
	if len(p.Captures.Agents) > 0 {
		files = append(files, captureFile{"agents.json", p.Captures.Agents})
	}
	if len(p.Captures.Instructions) > 0 {
		files = append(files, captureFile{"instructions.json", p.Captures.Instructions})
	}
	if len(p.CanonicalJSON) > 0 {
		files = append(files, captureFile{"snapshot.json", p.CanonicalJSON})
	}
	if len(files) == 0 {
		return nil
	}
	dir := filepath.Join(paths.Profiles, p.Hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profile dir %s: %w", dir, err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}
