package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func profileStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func sampleProfile(id, hash string) (ProfileRow, []ComponentRow) {
	row := ProfileRow{
		ID:              id,
		ProfileHash:     hash,
		OpenCodeVersion: "1.18.32",
		OCBenchVersion:  "dev",
		CanonicalJSON:   `{"schema":1}`,
		CreatedAt:       "2026-01-01T00:00:00Z",
	}
	return row, []ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build", CanonicalJSON: `{"mode":"primary"}`},
	}
}

func TestInsertProfileIsIdempotent(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	row, comps := sampleProfile("p1", "hash-1")
	if err := st.InsertProfile(ctx, row, comps); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := st.InsertProfile(ctx, row, comps); err != nil {
		t.Fatalf("second insert: %v", err)
	}
	var profiles, components int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles`).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_components`).Scan(&components); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || components != 2 {
		t.Fatalf("profiles=%d components=%d, want 1 and 2", profiles, components)
	}
}

func TestInsertProfileDifferentIDSameHash(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	first, comps := sampleProfile("p1", "hash-1")
	if err := st.InsertProfile(ctx, first, comps); err != nil {
		t.Fatal(err)
	}
	second, secondComps := sampleProfile("p2", "hash-1")
	if err := st.InsertProfile(ctx, second, secondComps); err != nil {
		t.Fatalf("second id same hash: %v", err)
	}
	var profiles int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles`).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 {
		t.Fatalf("profiles = %d, want 1", profiles)
	}
	var p2Components int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_components WHERE profile_id = 'p2'`).Scan(&p2Components); err != nil {
		t.Fatal(err)
	}
	if p2Components != 0 {
		t.Fatalf("components for phantom id p2 = %d, want 0", p2Components)
	}
}

func TestGetProfileByHashRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	row, comps := sampleProfile("p1", "hash-1")
	if err := st.InsertProfile(ctx, row, comps); err != nil {
		t.Fatal(err)
	}
	got, gotComps, err := st.GetProfileByHash(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "p1" || got.ProfileHash != "hash-1" || got.CanonicalJSON != `{"schema":1}` {
		t.Fatalf("profile = %+v", got)
	}
	if len(gotComps) != 2 || gotComps[0].Kind != "agent" || gotComps[0].Name != "build" {
		t.Fatalf("components = %+v", gotComps)
	}
	if _, _, err := st.GetProfileByHash(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing err = %v, want sql.ErrNoRows", err)
	}
}

func TestLatestProfileOrdering(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	older, comps := sampleProfile("a", "hash-a")
	older.CreatedAt = "2026-01-01T00:00:00Z"
	if err := st.InsertProfile(ctx, older, comps); err != nil {
		t.Fatal(err)
	}
	newer, comps := sampleProfile("b", "hash-b")
	newer.CreatedAt = "2026-02-01T00:00:00Z"
	if err := st.InsertProfile(ctx, newer, comps); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.LatestProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "b" {
		t.Fatalf("latest = %s, want b", got.ID)
	}
	if _, _, err := profileStore(t).LatestProfile(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty latest err = %v, want sql.ErrNoRows", err)
	}
}

func TestProfileComponentsCascadeOnDelete(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	row, comps := sampleProfile("p1", "hash-1")
	if err := st.InsertProfile(ctx, row, comps); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM profiles WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_components`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("components after cascade = %d, want 0", n)
	}
}

func TestInsertProfileChangesRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	from, comps := sampleProfile("p1", "hash-1")
	if err := st.InsertProfile(ctx, from, comps); err != nil {
		t.Fatal(err)
	}
	to, comps := sampleProfile("p2", "hash-2")
	if err := st.InsertProfile(ctx, to, comps); err != nil {
		t.Fatal(err)
	}
	changes := []ChangeRow{
		{ComponentKind: "skill", ComponentName: "ruff", Change: "changed", FromHash: "x", ToHash: "y", DetectedAt: "2026-03-01T00:00:00Z"},
		{ComponentKind: "agent", ComponentName: "build", Change: "changed", FromHash: "a", ToHash: "b"},
	}
	if err := st.InsertProfileChanges(ctx, "p1", "p2", changes); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListProfileChanges(ctx, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("changes = %+v", got)
	}
	if got[0].ComponentKind != "agent" || got[0].ComponentName != "build" {
		t.Fatalf("ordering: %+v", got)
	}
	if got[1].ComponentKind != "skill" || got[1].FromHash != "x" || got[1].ToHash != "y" {
		t.Fatalf("round trip: %+v", got[1])
	}
	if got[1].DetectedAt != "2026-03-01T00:00:00Z" {
		t.Fatalf("detected_at = %q", got[1].DetectedAt)
	}
	if empty, err := st.ListProfileChanges(ctx, "missing"); err != nil || len(empty) != 0 {
		t.Fatalf("missing changes = %+v, %v", empty, err)
	}
}
