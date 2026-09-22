package profile

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/store"
)

func openProfileStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ocbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestPersistDedupesAndWritesRedactedCaptures(t *testing.T) {
	ctx := context.Background()
	st := openProfileStore(t)
	paths := config.Paths{Profiles: filepath.Join(t.TempDir(), "profiles")}

	p, err := Fingerprint(loadTestSources(t), Options{Auto: true, EnvNames: []string{"PATH", "HOME"}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := Persist(ctx, st, paths, p)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first persist: created = false, want true")
	}
	if !uuidRE.MatchString(p.ID) {
		t.Fatalf("generated id %q is not a uuid", p.ID)
	}

	created, err = Persist(ctx, st, paths, p)
	if err != nil {
		t.Fatalf("second persist: %v", err)
	}
	if created {
		t.Fatal("second persist: created = true, want false")
	}

	var profiles, components int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles`).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_components`).Scan(&components); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 {
		t.Fatalf("profiles = %d, want 1", profiles)
	}
	if components != len(p.Components) {
		t.Fatalf("components = %d, want %d", components, len(p.Components))
	}

	dir := filepath.Join(paths.Profiles, p.Hash)
	for _, name := range []string{"resolved-config.json", "skills.json", "agents.json", "snapshot.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("capture %s missing: %v", name, err)
		}
	}

	// No secret value and no absolute home path may survive into any capture.
	scanDirForbidden(t, dir, []string{
		"glpat-EXAMPLE-not-real",
		"Bearer gitlab-pat-EXAMPLE-not-real",
		"/home/u",
	})
	// Raw skill content is never persisted; only metadata and hashes are.
	scanDirForbidden(t, dir, []string{"SYNTHETIC-SKILL-BODY-MARKER", "run ruff before committing"})

	// The generated id is unique for a different profile.
	other, err := Fingerprint(loadTestSources(t), Options{Auto: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Persist(ctx, st, paths, other); err != nil {
		t.Fatal(err)
	}
	if other.ID == p.ID {
		t.Fatal("profile ids are not unique")
	}
}

func TestPersistLatestRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openProfileStore(t)
	paths := config.Paths{Profiles: filepath.Join(t.TempDir(), "profiles")}
	p, err := Fingerprint(loadTestSources(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Persist(ctx, st, paths, p); err != nil {
		t.Fatal(err)
	}
	got, err := Latest(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Hash != p.Hash || got.OpenCodeVersion != p.OpenCodeVersion {
		t.Fatalf("latest = %+v, want id %s hash %s", got, p.ID, p.Hash)
	}
	if len(got.Components) != len(p.Components) {
		t.Fatalf("latest components = %d, want %d", len(got.Components), len(p.Components))
	}
	if string(got.CanonicalJSON) != string(p.CanonicalJSON) {
		t.Fatal("latest canonical JSON differs")
	}
}

// scanDirForbidden fails when any file under dir contains any of the forbidden
// substrings.
func scanDirForbidden(t *testing.T, dir string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, f := range forbidden {
			if strings.Contains(string(b), f) {
				t.Fatalf("forbidden value %q found in %s", f, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
