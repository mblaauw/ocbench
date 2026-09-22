package profile

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mbl/ocbench/internal/config"
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
		if p.ID == "" {
			p.ID = existing.ID
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if created {
		id := p.ID
		if id == "" {
			var err error
			id, err = newUUID()
			if err != nil {
				return false, err
			}
			p.ID = id
		}
		row := store.ProfileRow{
			ID:              id,
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

// Latest returns the most recent persisted profile.
func Latest(ctx context.Context, st *store.Store) (*Profile, error) {
	if st == nil {
		return nil, errors.New("latest: nil store")
	}
	row, comps, err := st.LatestProfile(ctx)
	if err != nil {
		return nil, err
	}
	return profileFromRows(row, comps)
}

func profileFromRows(row *store.ProfileRow, comps []store.ComponentRow) (*Profile, error) {
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

// writeCaptures writes the four content-addressed capture files. All payloads
// were redacted and path-normalised by Fingerprint before reaching here.
func writeCaptures(paths config.Paths, p *Profile) error {
	if paths.Profiles == "" {
		return errors.New("persist: empty profiles path")
	}
	dir := filepath.Join(paths.Profiles, p.Hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profile dir %s: %w", dir, err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{"resolved-config.json", defaultBytes(p.Captures.ResolvedConfig, []byte("{}"))},
		{"skills.json", defaultBytes(p.Captures.Skills, []byte("[]"))},
		{"agents.json", defaultBytes(p.Captures.Agents, []byte("[]"))},
		{"snapshot.json", defaultBytes(p.CanonicalJSON, []byte("{}"))},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

func defaultBytes(b, fallback []byte) []byte {
	if len(b) == 0 {
		return fallback
	}
	return b
}

// newUUID returns a random RFC 4122 version 4 shaped identifier formatted as
// 8-4-4-4-12 hex. It needs no external dependency.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate profile id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
