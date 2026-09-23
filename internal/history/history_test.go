package history_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/store"
)

const (
	suiteName    = "core"
	suiteVersion = "1"
	suiteHash    = "suite-hash-1"
	taskID       = "py-bugfix"
	taskVersion  = "1"
	fixtureSHA   = "abc123"
)

// testStore opens a migrated temp SQLite store.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func seedSuiteTask(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertSuite(ctx, store.SuiteRow{
		ID: suiteHash, Name: suiteName, Version: suiteVersion, Hash: suiteHash,
		Source: "embedded", ManifestJSON: `{"name":"core"}`, CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	if err := st.InsertTask(ctx, store.TaskRow{
		SuiteID: suiteHash, TaskID: taskID, Version: taskVersion, Name: "Fix",
		TagsJSON: `["python"]`, TimeoutSeconds: 300, FixtureSHA: fixtureSHA, SpecJSON: `{}`,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func seedProfile(t *testing.T, st *store.Store, id, hash string, comps []store.ComponentRow) {
	t.Helper()
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: id, ProfileHash: hash, OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, comps); err != nil {
		t.Fatalf("insert profile %s: %v", id, err)
	}
}

// components returns the standard component set, varying only the agent/build
// hash so a diff can be made to have exactly one changed component.
func components(buildHash string) []store.ComponentRow {
	return []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: buildHash, CanonicalJSON: `{"mode":"primary"}`},
	}
}

func seedRun(t *testing.T, st *store.Store, r store.RunRow) {
	t.Helper()
	if err := st.InsertRun(context.Background(), r); err != nil {
		t.Fatalf("insert run %s: %v", r.ID, err)
	}
}

func baseRun(id, started, profileID, profileHash string) store.RunRow {
	exit := 0
	dur := int64(1000)
	return store.RunRow{
		ID: id, ProfileID: profileID, ProfileHash: profileHash,
		SuiteID: suiteHash, SuiteName: suiteName, SuiteVersion: suiteVersion, SuiteHash: suiteHash,
		TaskID: taskID, TaskVersion: taskVersion, FixtureSHA: fixtureSHA,
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "p/m", Agent: "build",
		Status: "passed", DryRun: false, ExitCode: &exit,
		StartedAt: started, FinishedAt: started, DurationMS: &dur,
		ArtifactsDir: "/runs/" + id,
	}
}

func deltaByName(t *testing.T, deltas []history.MetricDelta, name string) history.MetricDelta {
	t.Helper()
	for _, d := range deltas {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("metric %q not found in %+v", name, deltas)
	return history.MetricDelta{}
}

func TestListEmptyStoreReturnsEmptySlice(t *testing.T) {
	st := testStore(t)
	got, err := history.List(context.Background(), st, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List returned a nil slice, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestListOrdersNewestFirstAndFilters(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedRun(t, st, baseRun("run-old", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-new", "2026-01-02T00:00:00Z", "p1", "hash-1"))

	got, err := history.List(context.Background(), st, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Run.ID != "run-new" || got[1].Run.ID != "run-old" {
		t.Fatalf("order = %+v, want [run-new run-old]", got)
	}

	limited, err := history.List(context.Background(), st, "", 1)
	if err != nil {
		t.Fatalf("List limit: %v", err)
	}
	if len(limited) != 1 || limited[0].Run.ID != "run-new" {
		t.Fatalf("limited = %+v, want [run-new]", limited)
	}

	filtered, err := history.List(context.Background(), st, "other-task", 0)
	if err != nil {
		t.Fatalf("List filter: %v", err)
	}
	if filtered == nil || len(filtered) != 0 {
		t.Fatalf("filtered = %+v, want empty non-nil", filtered)
	}
}

func TestListNormalizesEmptyMetricsAndValidations(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", nil)
	seedRun(t, st, baseRun("run-1", "2026-01-01T00:00:00Z", "p1", "hash-1"))

	got, err := history.List(context.Background(), st, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	d := got[0]
	if d.Metrics == nil || len(d.Metrics) != 0 {
		t.Fatalf("Metrics = %#v, want empty non-nil", d.Metrics)
	}
	if d.Validations == nil || len(d.Validations) != 0 {
		t.Fatalf("Validations = %#v, want empty non-nil", d.Validations)
	}
	if d.Profile == nil {
		t.Fatal("Profile = nil, want hydrated profile")
	}
}

func TestListHydratesMetricsAndValidations(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedRun(t, st, baseRun("run-1", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	if err := st.InsertRunMetrics(ctx, "run-1", map[string]float64{"steps": 7}); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
	if err := st.InsertRunValidations(ctx, "run-1", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "passed", ExitCode: 0, DurationMS: 5},
	}); err != nil {
		t.Fatalf("insert validations: %v", err)
	}

	got, err := history.List(ctx, st, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Metrics["steps"] != 7 {
		t.Fatalf("Metrics = %#v, want steps=7", got[0].Metrics)
	}
	if len(got[0].Validations) != 1 || got[0].Validations[0].Name != "unit" {
		t.Fatalf("Validations = %+v", got[0].Validations)
	}
}

func TestCompareLatestPreviousResolvesRelativeAndSkipsDryRuns(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedProfile(t, st, "p2", "hash-2", components("h-build-2"))
	seedRun(t, st, baseRun("run-old", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-new", "2026-01-02T00:00:00Z", "p2", "hash-2"))
	dry := baseRun("run-dry", "2026-01-03T00:00:00Z", "p1", "hash-1")
	dry.DryRun = true
	seedRun(t, st, dry)

	// "previous" is always Before and the run it is relative to is always
	// After, so the argument order does not change the resolved pair and the
	// metric deltas are always latest-minus-previous.
	cmp, err := history.Compare(context.Background(), st, "latest", "previous")
	if err != nil {
		t.Fatalf("Compare(latest, previous): %v", err)
	}
	if cmp.Before.Run.ID != "run-old" || cmp.After.Run.ID != "run-new" {
		t.Fatalf("latest/previous = before %q after %q, want run-old/run-new", cmp.Before.Run.ID, cmp.After.Run.ID)
	}

	cmp, err = history.Compare(context.Background(), st, "previous", "latest")
	if err != nil {
		t.Fatalf("Compare(previous, latest): %v", err)
	}
	if cmp.Before.Run.ID != "run-old" || cmp.After.Run.ID != "run-new" {
		t.Fatalf("previous/latest = before %q after %q, want run-old/run-new", cmp.Before.Run.ID, cmp.After.Run.ID)
	}
}

func TestCompareExplicitDryRunIsUsageError(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedProfile(t, st, "p2", "hash-2", components("h-build-2"))
	seedRun(t, st, baseRun("run-before", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-after", "2026-01-02T00:00:00Z", "p2", "hash-2"))
	dry := baseRun("run-dry", "2026-01-03T00:00:00Z", "p1", "hash-1")
	dry.DryRun = true
	seedRun(t, st, dry)

	cases := []struct {
		name        string
		left, right string
	}{
		{"dry explicit with explicit", "run-dry", "run-after"},
		{"explicit with dry explicit", "run-after", "run-dry"},
		{"dry explicit with previous", "run-dry", "previous"},
		{"previous with dry explicit", "previous", "run-dry"},
		{"dry explicit with latest", "run-dry", "latest"},
		{"latest with dry explicit", "latest", "run-dry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := history.Compare(ctx, st, tc.left, tc.right); !errors.Is(err, history.ErrSelector) {
				t.Fatalf("Compare(%q, %q) err = %v, want ErrSelector", tc.left, tc.right, err)
			}
		})
	}
}

func TestCompareBothPreviousIsUsageError(t *testing.T) {
	st := testStore(t)
	_, err := history.Compare(context.Background(), st, "previous", "previous")
	if !errors.Is(err, history.ErrSelector) {
		t.Fatalf("err = %v, want ErrSelector", err)
	}
}

func TestCompareMissingSelectorsWrapNoRows(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if _, err := history.Compare(ctx, st, "latest", "previous"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty store err = %v, want sql.ErrNoRows", err)
	}

	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedRun(t, st, baseRun("run-1", "2026-01-01T00:00:00Z", "p1", "hash-1"))

	if _, err := history.Compare(ctx, st, "no-such-id", "latest"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown id err = %v, want sql.ErrNoRows", err)
	}
	if _, err := history.Compare(ctx, st, "latest", "no-such-id"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown right id err = %v, want sql.ErrNoRows", err)
	}
	// A valid "latest" with no compatible predecessor is missing, not a usage
	// error.
	if _, err := history.Compare(ctx, st, "latest", "previous"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no predecessor err = %v, want sql.ErrNoRows", err)
	}
}

func TestCompareIncompatibleExplicitRunsIsUsageError(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))

	left := baseRun("run-a", "2026-01-01T00:00:00Z", "p1", "hash-1")
	right := baseRun("run-b", "2026-01-02T00:00:00Z", "p1", "hash-1")
	right.FixtureSHA = "different-fixture"
	seedRun(t, st, left)
	seedRun(t, st, right)

	if _, err := history.Compare(ctx, st, "run-a", "run-b"); !errors.Is(err, history.ErrSelector) {
		t.Fatalf("err = %v, want ErrSelector", err)
	}
}

func TestCompareMetricUnionAndZeroBaselinePercent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedRun(t, st, baseRun("run-before", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-after", "2026-01-02T00:00:00Z", "p1", "hash-1"))
	if err := st.InsertRunMetrics(ctx, "run-before", map[string]float64{
		"a": 4, "b": 0, "only_before": 2,
	}); err != nil {
		t.Fatalf("insert before metrics: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, "run-after", map[string]float64{
		"a": 6, "b": 3, "only_after": 5,
	}); err != nil {
		t.Fatalf("insert after metrics: %v", err)
	}

	cmp, err := history.Compare(ctx, st, "run-before", "run-after")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}

	names := make([]string, len(cmp.Metrics))
	for i, d := range cmp.Metrics {
		names[i] = d.Name
	}
	wantNames := []string{"a", "b", "only_after", "only_before"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("metric order = %v, want %v", names, wantNames)
	}

	a := deltaByName(t, cmp.Metrics, "a")
	if a.Before != 4 || a.After != 6 || a.Delta != 2 {
		t.Fatalf("a = %+v, want before=4 after=6 delta=2", a)
	}
	if a.Percent == nil || *a.Percent != 50 {
		t.Fatalf("a.Percent = %v, want 50", a.Percent)
	}

	b := deltaByName(t, cmp.Metrics, "b")
	if b.Before != 0 || b.After != 3 || b.Delta != 3 {
		t.Fatalf("b = %+v, want before=0 after=3 delta=3", b)
	}
	if b.Percent != nil {
		t.Fatalf("b.Percent = %v, want nil for zero baseline", *b.Percent)
	}

	onlyAfter := deltaByName(t, cmp.Metrics, "only_after")
	if onlyAfter.Before != 0 || onlyAfter.After != 5 || onlyAfter.Delta != 5 || onlyAfter.Percent != nil {
		t.Fatalf("only_after = %+v, want before=0 after=5 delta=5 percent=nil", onlyAfter)
	}

	onlyBefore := deltaByName(t, cmp.Metrics, "only_before")
	if onlyBefore.Before != 2 || onlyBefore.After != 0 || onlyBefore.Delta != -2 {
		t.Fatalf("only_before = %+v, want before=2 after=0 delta=-2", onlyBefore)
	}
	if onlyBefore.Percent == nil || *onlyBefore.Percent != -100 {
		t.Fatalf("only_before.Percent = %v, want -100", onlyBefore.Percent)
	}
}

func TestCompareSingleProfileChangeHasNoWarning(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedProfile(t, st, "p2", "hash-2", components("h-build-2"))
	seedRun(t, st, baseRun("run-before", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-after", "2026-01-02T00:00:00Z", "p2", "hash-2"))

	cmp, err := history.Compare(ctx, st, "run-before", "run-after")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(cmp.ProfileChanges) != 1 {
		t.Fatalf("ProfileChanges = %+v, want exactly 1", cmp.ProfileChanges)
	}
	if cmp.ControlledRunWarning != "" {
		t.Fatalf("warning = %q, want empty for a single change", cmp.ControlledRunWarning)
	}
}

func TestCompareMultipleProfileChangesWarnsWithCount(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedProfile(t, st, "p3", "hash-3", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary-2", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-2", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedRun(t, st, baseRun("run-before", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-after", "2026-01-02T00:00:00Z", "p3", "hash-3"))

	cmp, err := history.Compare(ctx, st, "run-before", "run-after")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(cmp.ProfileChanges) != 2 {
		t.Fatalf("ProfileChanges = %+v, want exactly 2", cmp.ProfileChanges)
	}
	if !strings.Contains(cmp.ControlledRunWarning, "2") {
		t.Fatalf("warning = %q, want it to state the count 2", cmp.ControlledRunWarning)
	}
	lower := strings.ToLower(cmp.ControlledRunWarning)
	for _, bad := range []string{"because", "caused", "due to"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("warning = %q, must not claim causation (%q)", cmp.ControlledRunWarning, bad)
		}
	}
}

func TestCompareEmptyResultsAreNonNil(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-1", components("h-build-1"))
	seedRun(t, st, baseRun("run-before", "2026-01-01T00:00:00Z", "p1", "hash-1"))
	seedRun(t, st, baseRun("run-after", "2026-01-02T00:00:00Z", "p1", "hash-1"))

	cmp, err := history.Compare(ctx, st, "run-before", "run-after")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if cmp.Metrics == nil || len(cmp.Metrics) != 0 {
		t.Fatalf("Metrics = %#v, want empty non-nil", cmp.Metrics)
	}
	if cmp.ProfileChanges == nil || len(cmp.ProfileChanges) != 0 {
		t.Fatalf("ProfileChanges = %#v, want empty non-nil", cmp.ProfileChanges)
	}
	if cmp.ControlledRunWarning != "" {
		t.Fatalf("warning = %q, want empty", cmp.ControlledRunWarning)
	}
}
