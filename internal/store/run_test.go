package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

// derefRun copies a run row with its optional pointers dereferenced so two
// rows can be compared with reflect.DeepEqual.
func derefRun(r RunRow) RunRow {
	if r.ExitCode != nil {
		v := *r.ExitCode
		r.ExitCode = &v
	}
	if r.DurationMS != nil {
		v := *r.DurationMS
		r.DurationMS = &v
	}
	return r
}

// insertSampleRun inserts the suite, task and run a run-level test needs.
func insertSampleRun(t *testing.T, st *Store, run RunRow) {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatalf("insert run: %v", err)
	}
}

// insertSampleProfile inserts the profile a run row must reference and returns
// its id.
func insertSampleProfile(t *testing.T, st *Store, id, hash string) {
	t.Helper()
	row, comps := sampleProfile(id, hash)
	if err := st.InsertProfile(context.Background(), row, comps); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
}

func sampleSuite() SuiteRow {
	return SuiteRow{
		ID:           "suite-hash-1",
		Name:         "core",
		Version:      "1",
		Hash:         "suite-hash-1",
		Source:       "embedded",
		ManifestJSON: `{"name":"core"}`,
		CreatedAt:    "2026-01-01T00:00:00Z",
	}
}

func sampleTask() TaskRow {
	return TaskRow{
		SuiteID:        "suite-hash-1",
		TaskID:         "py-bugfix",
		Version:        "1",
		Name:           "Fix the divide operator",
		TagsJSON:       `["python"]`,
		TimeoutSeconds: 300,
		FixtureSHA:     "abc123",
		SpecJSON:       `{"id":"py-bugfix"}`,
	}
}

func sampleRun(id, taskID, started string) RunRow {
	exit := 0
	dur := int64(1234)
	return RunRow{
		ID:              id,
		ExperimentID:    "",
		RepeatIndex:     0,
		ProfileID:       "p1",
		ProfileHash:     "hash-1",
		SuiteID:         "suite-hash-1",
		SuiteName:       "core",
		SuiteVersion:    "1",
		SuiteHash:       "suite-hash-1",
		TaskID:          taskID,
		TaskVersion:     "1",
		FixtureSHA:      "abc123",
		OpenCodeVersion: "1.18.32",
		OCBenchVersion:  "dev",
		Model:           "p/m",
		Agent:           "build",
		Variant:         "high",
		Status:          "passed",
		DryRun:          false,
		ExitCode:        &exit,
		SessionID:       "ses_1",
		StartedAt:       started,
		FinishedAt:      started,
		DurationMS:      &dur,
		ArtifactsDir:    "/runs/" + id,
		Error:           "",
	}
}

func TestInsertSuiteTaskRunRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")

	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatalf("InsertSuite: %v", err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
	// Upserts are idempotent.
	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatalf("second InsertSuite: %v", err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatalf("second InsertTask: %v", err)
	}

	want := sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z")
	if err := st.InsertRun(ctx, want); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	got, err := st.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !reflect.DeepEqual(derefRun(*got), derefRun(want)) {
		t.Fatalf("run round trip:\n got %+v\nwant %+v", *got, want)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", got.ExitCode)
	}
	if got.DurationMS == nil || *got.DurationMS != 1234 {
		t.Fatalf("duration = %v, want 1234", got.DurationMS)
	}

	if _, err := st.GetRun(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetRun(missing) err = %v, want sql.ErrNoRows", err)
	}

	var suites, tasks, runs int
	for _, q := range []struct {
		sql string
		n   *int
	}{
		{"SELECT COUNT(*) FROM suites", &suites},
		{"SELECT COUNT(*) FROM tasks", &tasks},
		{"SELECT COUNT(*) FROM runs", &runs},
	} {
		if err := st.DB().QueryRowContext(ctx, q.sql).Scan(q.n); err != nil {
			t.Fatal(err)
		}
	}
	if suites != 1 || tasks != 1 || runs != 1 {
		t.Fatalf("suites=%d tasks=%d runs=%d, want 1/1/1", suites, tasks, runs)
	}
}

func TestInsertRunNullableFieldsRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatal(err)
	}

	run := sampleRun("run-null", "py-bugfix", "2026-03-01T10:00:00Z")
	run.ExitCode = nil
	run.DurationMS = nil
	run.FinishedAt = ""
	run.Model = ""
	run.Agent = ""
	run.Variant = ""
	run.SessionID = ""
	run.Error = ""
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	got, err := st.GetRun(ctx, "run-null")
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != nil || got.DurationMS != nil {
		t.Fatalf("nullable ints = %v/%v, want nil", got.ExitCode, got.DurationMS)
	}
	if got.FinishedAt != "" || got.Model != "" || got.SessionID != "" {
		t.Fatalf("nullable strings not empty: %+v", got)
	}
}

func TestInsertRunRequiresProfile(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	// The suite and task exist, so the only missing reference is the profile.
	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatal(err)
	}
	run := sampleRun("run-orphan", "py-bugfix", "2026-03-01T10:00:00Z")
	if err := st.InsertRun(ctx, run); err == nil {
		t.Fatal("InsertRun with unknown profile id succeeded, want FK error")
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("runs after failed insert = %d, want 0", n)
	}
}

func TestInsertRunMetricsUpsert(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	insertSampleRun(t, st, sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z"))

	if err := st.InsertRunMetrics(ctx, "run-1", map[string]float64{"steps": 3, "cost": 0.25}); err != nil {
		t.Fatalf("InsertRunMetrics: %v", err)
	}
	// Re-inserting updates the value and leaves other metrics untouched.
	if err := st.InsertRunMetrics(ctx, "run-1", map[string]float64{"steps": 7}); err != nil {
		t.Fatalf("second InsertRunMetrics: %v", err)
	}

	rows, err := st.DB().QueryContext(ctx, `SELECT name, value_num FROM run_metrics WHERE run_id = 'run-1' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]float64{}
	for rows.Next() {
		var name string
		var v float64
		if err := rows.Scan(&name, &v); err != nil {
			t.Fatal(err)
		}
		got[name] = v
	}
	if len(got) != 2 || got["steps"] != 7 || got["cost"] != 0.25 {
		t.Fatalf("metrics = %v, want steps=7 cost=0.25", got)
	}

	// An empty map is a no-op, not an error.
	if err := st.InsertRunMetrics(ctx, "run-1", nil); err != nil {
		t.Fatalf("empty InsertRunMetrics: %v", err)
	}
}

func TestInsertRunValidationsRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	insertSampleRun(t, st, sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z"))

	vals := []ValidationRow{
		{Seq: 1, Kind: "command", Name: "unit tests", Command: "python3 -m unittest", Status: "passed", ExitCode: 0, DurationMS: 42, OutputPath: "validation/1-unit_tests.log", OutputExcerpt: "ok"},
		{Seq: 2, Kind: "answer", Name: "root cause", Status: "failed", ExitCode: 1, DurationMS: 0, OutputPath: "validation/2-root_cause.log", OutputExcerpt: "unmatched"},
	}
	if err := st.InsertRunValidations(ctx, "run-1", vals); err != nil {
		t.Fatalf("InsertRunValidations: %v", err)
	}
	// Upsert keeps the row count at one per seq and updates the status.
	vals[0].Status = "failed"
	if err := st.InsertRunValidations(ctx, "run-1", vals); err != nil {
		t.Fatalf("second InsertRunValidations: %v", err)
	}

	rows, err := st.DB().QueryContext(ctx, `SELECT seq, kind, name, status, exit_code, output_path FROM run_validations WHERE run_id = 'run-1' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []ValidationRow
	for rows.Next() {
		var v ValidationRow
		if err := rows.Scan(&v.Seq, &v.Kind, &v.Name, &v.Status, &v.ExitCode, &v.OutputPath); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != 2 {
		t.Fatalf("validations = %d, want 2", len(got))
	}
	if got[0].Seq != 1 || got[0].Status != "failed" || got[0].OutputPath != "validation/1-unit_tests.log" {
		t.Fatalf("validation[0] = %+v", got[0])
	}
	if got[1].Kind != "answer" || got[1].Name != "root cause" {
		t.Fatalf("validation[1] = %+v", got[1])
	}

	// Empty input is a no-op.
	if err := st.InsertRunValidations(ctx, "run-1", nil); err != nil {
		t.Fatalf("empty InsertRunValidations: %v", err)
	}
}

func TestListRunsOrderingAndFilter(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	if err := st.InsertSuite(ctx, sampleSuite()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTask(ctx, sampleTask()); err != nil {
		t.Fatal(err)
	}

	runs := []RunRow{
		sampleRun("run-a", "py-bugfix", "2026-03-01T10:00:00Z"),
		sampleRun("run-b", "multi-file-feature", "2026-03-02T10:00:00Z"),
		sampleRun("run-c", "py-bugfix", "2026-03-03T10:00:00Z"),
	}
	for _, r := range runs {
		if err := st.InsertRun(ctx, r); err != nil {
			t.Fatalf("InsertRun %s: %v", r.ID, err)
		}
	}

	all, err := st.ListRuns(ctx, 0, "")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(all) != 3 || all[0].ID != "run-c" || all[2].ID != "run-a" {
		t.Fatalf("ordering = %v, want c,b,a", runIDs(all))
	}

	limited, err := st.ListRuns(ctx, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID != "run-c" || limited[1].ID != "run-b" {
		t.Fatalf("limit = %v, want c,b", runIDs(limited))
	}

	filtered, err := st.ListRuns(ctx, 0, "py-bugfix")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || filtered[0].ID != "run-c" || filtered[1].ID != "run-a" {
		t.Fatalf("filter = %v, want c,a", runIDs(filtered))
	}

	none, err := st.ListRuns(ctx, 0, "no-such-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("missing task = %v, want empty", runIDs(none))
	}
}

func runIDs(runs []RunRow) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}

func TestGetRunMetricsSorted(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	insertSampleRun(t, st, sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z"))

	if err := st.InsertRunMetrics(ctx, "run-1", map[string]float64{"steps": 3, "cost": 0.25, "accuracy": 0.9}); err != nil {
		t.Fatalf("InsertRunMetrics: %v", err)
	}
	// A text-only metric exercises the nullable value_num/value_text/unit columns.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO run_metrics (run_id, name, value_num, value_text, unit) VALUES ('run-1', 'notes', NULL, 'hello', 'count')`); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetRunMetrics(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRunMetrics: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("metrics = %d, want 4: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name}
	if want := []string{"accuracy", "cost", "notes", "steps"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("metric order = %v, want %v", names, want)
	}
	if got[0].ValueNum == nil || *got[0].ValueNum != 0.9 {
		t.Fatalf("accuracy value = %v, want 0.9", got[0].ValueNum)
	}
	notes := got[2]
	if notes.ValueNum != nil || notes.ValueText != "hello" || notes.Unit != "count" {
		t.Fatalf("notes metric = %+v", notes)
	}

	empty, err := st.GetRunMetrics(ctx, "missing")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing metrics = %+v, %v, want empty", empty, err)
	}
}

func TestListRunValidationsSorted(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	insertSampleRun(t, st, sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z"))

	vals := []ValidationRow{
		{Seq: 3, Kind: "answer", Name: "third", Status: "passed", ExitCode: 0, DurationMS: 5},
		{Seq: 1, Kind: "command", Name: "first", Command: "go test", Status: "failed", ExitCode: 1, DurationMS: 7, OutputPath: "validation/1.log", OutputExcerpt: "boom"},
		{Seq: 2, Kind: "answer", Name: "second", Status: "passed", ExitCode: 0, DurationMS: 0},
	}
	if err := st.InsertRunValidations(ctx, "run-1", vals); err != nil {
		t.Fatalf("InsertRunValidations: %v", err)
	}

	got, err := st.ListRunValidations(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListRunValidations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("validations = %d, want 3", len(got))
	}
	for i, wantSeq := range []int{1, 2, 3} {
		if got[i].Seq != wantSeq {
			t.Fatalf("validation[%d].Seq = %d, want %d", i, got[i].Seq, wantSeq)
		}
	}
	if got[0].Command != "go test" || got[0].OutputPath != "validation/1.log" || got[0].OutputExcerpt != "boom" {
		t.Fatalf("validation[0] = %+v", got[0])
	}
	if got[2].Command != "" || got[2].OutputPath != "" || got[2].OutputExcerpt != "" {
		t.Fatalf("nullable validation = %+v, want empty strings", got[2])
	}

	empty, err := st.ListRunValidations(ctx, "missing")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing validations = %+v, %v, want empty", empty, err)
	}
}

func TestLatestRunSelectsNewestNonDry(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")

	// The newest run overall is a dry run and must be excluded.
	dry := sampleRun("run-dry", "py-bugfix", "2026-03-04T10:00:00Z")
	dry.DryRun = true
	older := sampleRun("run-old", "py-bugfix", "2026-03-01T10:00:00Z")
	newer := sampleRun("run-new", "py-bugfix", "2026-03-03T10:00:00Z")
	// Exercise nullable columns on the returned row.
	newer.ExitCode = nil
	newer.DurationMS = nil
	newer.FinishedAt = ""
	newer.Model = ""
	newer.SessionID = ""
	newer.Error = ""
	for _, r := range []RunRow{older, dry, newer} {
		insertSampleRun(t, st, r)
	}

	got, err := st.LatestRun(ctx)
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got.ID != "run-new" {
		t.Fatalf("latest = %s, want run-new", got.ID)
	}
	if got.ExitCode != nil || got.DurationMS != nil {
		t.Fatalf("nullable ints = %v/%v, want nil", got.ExitCode, got.DurationMS)
	}
	if got.FinishedAt != "" || got.Model != "" || got.SessionID != "" || got.Error != "" {
		t.Fatalf("nullable strings not empty: %+v", got)
	}

	// A store holding only dry runs has no latest.
	dryOnly := profileStore(t)
	insertSampleProfile(t, dryOnly, "p1", "hash-1")
	insertSampleRun(t, dryOnly, dry)
	if _, err := dryOnly.LatestRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("dry-only latest err = %v, want sql.ErrNoRows", err)
	}

	// An empty store has no latest.
	if _, err := profileStore(t).LatestRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty latest err = %v, want sql.ErrNoRows", err)
	}
}

func TestLatestRunTieBreaksByID(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	insertSampleRun(t, st, sampleRun("run-a", "py-bugfix", "2026-03-01T10:00:00Z"))
	insertSampleRun(t, st, sampleRun("run-b", "py-bugfix", "2026-03-01T10:00:00Z"))

	got, err := st.LatestRun(ctx)
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got.ID != "run-b" {
		t.Fatalf("latest tie = %s, want run-b", got.ID)
	}
}

func TestPreviousCompatibleRun(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")

	anchor := sampleRun("anchor", "py-bugfix", "2026-03-05T10:00:00Z")
	prev := sampleRun("prev", "py-bugfix", "2026-03-03T10:00:00Z")
	older := sampleRun("older", "py-bugfix", "2026-03-01T10:00:00Z")
	newer := sampleRun("newer", "py-bugfix", "2026-03-06T10:00:00Z")
	dry := sampleRun("dry", "py-bugfix", "2026-03-04T10:00:00Z")
	dry.DryRun = true
	badTask := sampleRun("bad-task", "other-task", "2026-03-04T09:00:00Z")
	badFixture := sampleRun("bad-fixture", "py-bugfix", "2026-03-04T08:00:00Z")
	badFixture.FixtureSHA = "other"
	badSuite := sampleRun("bad-suite", "py-bugfix", "2026-03-04T07:00:00Z")
	badSuite.SuiteName = "other"

	for _, r := range []RunRow{anchor, prev, older, newer, dry, badTask, badFixture, badSuite} {
		insertSampleRun(t, st, r)
	}

	got, err := st.PreviousCompatibleRun(ctx, anchor)
	if err != nil {
		t.Fatalf("PreviousCompatibleRun: %v", err)
	}
	if got.ID != "prev" {
		t.Fatalf("previous = %s, want prev", got.ID)
	}

	// A first run has no compatible predecessor.
	first := sampleRun("first", "py-bugfix", "2026-03-01T00:00:00Z")
	noPrev := profileStore(t)
	insertSampleProfile(t, noPrev, "p1", "hash-1")
	insertSampleRun(t, noPrev, first)
	if _, err := noPrev.PreviousCompatibleRun(ctx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no predecessor err = %v, want sql.ErrNoRows", err)
	}
}

func TestPreviousCompatibleRunSkipsChangedSuiteHash(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")

	anchor := sampleRun("anchor", "py-bugfix", "2026-03-05T10:00:00Z")
	match := sampleRun("match", "py-bugfix", "2026-03-03T10:00:00Z")
	changed := sampleRun("changed", "py-bugfix", "2026-03-04T10:00:00Z")
	changed.SuiteHash = "suite-hash-2"

	for _, r := range []RunRow{anchor, match, changed} {
		insertSampleRun(t, st, r)
	}

	got, err := st.PreviousCompatibleRun(ctx, anchor)
	if err != nil {
		t.Fatalf("PreviousCompatibleRun: %v", err)
	}
	if got.ID != "match" {
		t.Fatalf("previous = %s, want match (changed suite hash must be skipped)", got.ID)
	}

	// When only a changed-suite-hash predecessor exists there is no controlled
	// comparison, so the lookup must report no predecessor.
	onlyAnchor := sampleRun("only-anchor", "py-bugfix", "2026-03-05T10:00:00Z")
	onlyChanged := sampleRun("only-changed", "py-bugfix", "2026-03-04T10:00:00Z")
	onlyChanged.SuiteHash = "suite-hash-2"
	noMatch := profileStore(t)
	insertSampleProfile(t, noMatch, "p1", "hash-1")
	insertSampleRun(t, noMatch, onlyAnchor)
	insertSampleRun(t, noMatch, onlyChanged)
	if _, err := noMatch.PreviousCompatibleRun(ctx, onlyAnchor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("changed-suite-only err = %v, want sql.ErrNoRows", err)
	}
}

func TestPreviousCompatibleRunTieBreaksByID(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")

	anchor := sampleRun("anchor", "py-bugfix", "2026-03-02T10:00:00Z")
	insertSampleRun(t, st, sampleRun("run-a", "py-bugfix", "2026-03-01T10:00:00Z"))
	insertSampleRun(t, st, sampleRun("run-b", "py-bugfix", "2026-03-01T10:00:00Z"))

	got, err := st.PreviousCompatibleRun(ctx, anchor)
	if err != nil {
		t.Fatalf("PreviousCompatibleRun: %v", err)
	}
	if got.ID != "run-b" {
		t.Fatalf("previous tie = %s, want run-b", got.ID)
	}
}

func TestInsertExperimentRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()

	want := ExperimentRow{
		ID:        "exp-1",
		Name:      "run core@1 2026-03-01T10:00:00Z",
		SpecJSON:  `{"repeat":2,"suite":"core"}`,
		CreatedAt: "2026-03-01T10:00:00Z",
	}
	if err := st.InsertExperiment(ctx, want); err != nil {
		t.Fatalf("InsertExperiment: %v", err)
	}
	// A second insert of the same id is idempotent and does not duplicate.
	if err := st.InsertExperiment(ctx, want); err != nil {
		t.Fatalf("second InsertExperiment: %v", err)
	}

	var (
		id, name, spec, created string
	)
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id, name, spec_json, created_at FROM experiments WHERE id = ?`, want.ID).
		Scan(&id, &name, &spec, &created); err != nil {
		t.Fatalf("query experiment: %v", err)
	}
	if id != want.ID || name != want.Name || spec != want.SpecJSON || created != want.CreatedAt {
		t.Fatalf("experiment = %q/%q/%q/%q, want %+v", id, name, spec, created, want)
	}

	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM experiments`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("experiments = %d, want 1", n)
	}
}

func TestGetExperiment(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()

	want := ExperimentRow{
		ID:        "exp-get",
		Name:      "experiment core@1 2026-03-01T10:00:00Z",
		SpecJSON:  `{"repeat":3}`,
		CreatedAt: "2026-03-01T10:00:00Z",
	}
	if err := st.InsertExperiment(ctx, want); err != nil {
		t.Fatalf("InsertExperiment: %v", err)
	}

	got, err := st.GetExperiment(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetExperiment: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("experiment round trip:\n got %+v\nwant %+v", *got, want)
	}

	if _, err := st.GetExperiment(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetExperiment(missing) err = %v, want sql.ErrNoRows", err)
	}
}

// sampleArm builds an experiment arm whose profile reference is p1.
func sampleArm(id, experimentID, label string) ExperimentArmRow {
	profileID := "p1"
	return ExperimentArmRow{
		ID:           id,
		ExperimentID: experimentID,
		Label:        label,
		ProfileID:    &profileID,
		ProfileHash:  "hash-1",
		OverlayKind:  "none",
		CreatedAt:    "2026-03-01T10:00:00Z",
	}
}

// insertSampleExperiment inserts the profile, experiment and arm a run-level
// arm test needs.
func insertSampleExperiment(t *testing.T, st *Store, exp ExperimentRow) {
	t.Helper()
	insertSampleProfile(t, st, "p1", "hash-1")
	if err := st.InsertExperiment(context.Background(), exp); err != nil {
		t.Fatalf("InsertExperiment: %v", err)
	}
}

func TestExperimentArmRoundTrip(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleExperiment(t, st, ExperimentRow{
		ID: "exp-1", Name: "run core@1", SpecJSON: `{"repeat":2}`, CreatedAt: "2026-03-01T10:00:00Z",
	})

	profileID := "p1"
	overlayPath := "overlays/a.patch"
	overlaySHA := "deadbeef"
	want := ExperimentArmRow{
		ID:            "arm-1",
		ExperimentID:  "exp-1",
		Label:         "baseline",
		ProfileID:     &profileID,
		ProfileHash:   "hash-1",
		OverlayKind:   "patch",
		OverlayPath:   &overlayPath,
		OverlaySHA256: &overlaySHA,
		CreatedAt:     "2026-03-01T10:00:00Z",
	}
	if err := st.InsertExperimentArm(ctx, want); err != nil {
		t.Fatalf("InsertExperimentArm: %v", err)
	}

	got, err := st.GetExperimentArm(ctx, "arm-1")
	if err != nil {
		t.Fatalf("GetExperimentArm: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("arm round trip:\n got %+v\nwant %+v", *got, want)
	}

	// Nullable columns read back as nil pointers.
	if err := st.InsertExperimentArm(ctx, ExperimentArmRow{
		ID: "arm-2", ExperimentID: "exp-1", Label: "no-overlay",
		ProfileID: nil, ProfileHash: "hash-1", OverlayKind: "none",
		CreatedAt: "2026-03-01T10:00:00Z",
	}); err != nil {
		t.Fatalf("InsertExperimentArm nullable: %v", err)
	}
	nullable, err := st.GetExperimentArm(ctx, "arm-2")
	if err != nil {
		t.Fatal(err)
	}
	if nullable.ProfileID != nil || nullable.OverlayPath != nil || nullable.OverlaySHA256 != nil {
		t.Fatalf("nullable arm pointers = %v/%v/%v, want nil", nullable.ProfileID, nullable.OverlayPath, nullable.OverlaySHA256)
	}

	if _, err := st.GetExperimentArm(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetExperimentArm(missing) err = %v, want sql.ErrNoRows", err)
	}
}

func TestListExperimentArmsLabelOrder(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleExperiment(t, st, ExperimentRow{
		ID: "exp-1", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z",
	})

	for _, a := range []ExperimentArmRow{
		sampleArm("arm-c", "exp-1", "candidate"),
		sampleArm("arm-a", "exp-1", "baseline"),
		sampleArm("arm-b", "exp-1", "boosted"),
	} {
		if err := st.InsertExperimentArm(ctx, a); err != nil {
			t.Fatalf("InsertExperimentArm %s: %v", a.ID, err)
		}
	}

	got, err := st.ListExperimentArms(ctx, "exp-1")
	if err != nil {
		t.Fatalf("ListExperimentArms: %v", err)
	}
	var labels []string
	for _, a := range got {
		labels = append(labels, a.Label)
	}
	if want := []string{"baseline", "boosted", "candidate"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("label order = %v, want %v", labels, want)
	}

	empty, err := st.ListExperimentArms(ctx, "no-such-exp")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing arms = %+v, %v, want empty", empty, err)
	}
}

func TestInsertExperimentArmDuplicateLabelFails(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleExperiment(t, st, ExperimentRow{
		ID: "exp-1", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z",
	})
	if err := st.InsertExperimentArm(ctx, sampleArm("arm-1", "exp-1", "baseline")); err != nil {
		t.Fatalf("InsertExperimentArm: %v", err)
	}
	// A different id with the same (experiment_id, label) violates UNIQUE.
	if err := st.InsertExperimentArm(ctx, sampleArm("arm-2", "exp-1", "baseline")); err == nil {
		t.Fatal("duplicate (experiment_id,label) insert succeeded, want error")
	}
	// The same label under a different experiment is allowed.
	if err := st.InsertExperiment(ctx, ExperimentRow{
		ID: "exp-2", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExperimentArm(ctx, sampleArm("arm-3", "exp-2", "baseline")); err != nil {
		t.Fatalf("same label in another experiment: %v", err)
	}
}

func TestDeleteExperimentCascadesArms(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleExperiment(t, st, ExperimentRow{
		ID: "exp-1", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z",
	})
	if err := st.InsertExperimentArm(ctx, sampleArm("arm-1", "exp-1", "baseline")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM experiments WHERE id = 'exp-1'`); err != nil {
		t.Fatalf("delete experiment: %v", err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM experiment_arms`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("experiment_arms after cascade = %d, want 0", n)
	}
}

func TestRunsForExperiment(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()
	insertSampleProfile(t, st, "p1", "hash-1")
	for _, exp := range []ExperimentRow{
		{ID: "exp-1", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z"},
		{ID: "exp-2", Name: "run core@1", SpecJSON: `{}`, CreatedAt: "2026-03-02T10:00:00Z"},
	} {
		if err := st.InsertExperiment(ctx, exp); err != nil {
			t.Fatal(err)
		}
	}
	armID := "arm-1"
	if err := st.InsertExperimentArm(ctx, sampleArm(armID, "exp-1", "baseline")); err != nil {
		t.Fatal(err)
	}

	mine := sampleRun("run-1", "py-bugfix", "2026-03-01T10:00:00Z")
	mine.ExperimentID = "exp-1"
	mine.ArmID = &armID
	later := sampleRun("run-2", "py-bugfix", "2026-03-01T12:00:00Z")
	later.ExperimentID = "exp-1"
	later.ArmID = &armID
	other := sampleRun("run-3", "py-bugfix", "2026-03-01T11:00:00Z")
	other.ExperimentID = "exp-2"
	for _, r := range []RunRow{later, other, mine} {
		insertSampleRun(t, st, r)
	}

	got, err := st.RunsForExperiment(ctx, "exp-1")
	if err != nil {
		t.Fatalf("RunsForExperiment: %v", err)
	}
	if want := []string{"run-1", "run-2"}; !reflect.DeepEqual(runIDs(got), want) {
		t.Fatalf("runs = %v, want %v", runIDs(got), want)
	}
	if got[0].ArmID == nil || *got[0].ArmID != armID {
		t.Fatalf("run-1 arm = %v, want %q", got[0].ArmID, armID)
	}

	empty, err := st.RunsForExperiment(ctx, "no-such-exp")
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing runs = %+v, %v, want empty", empty, err)
	}
}

func TestListExperimentsOrderingAndLimit(t *testing.T) {
	st := profileStore(t)
	ctx := context.Background()

	rows := []ExperimentRow{
		{ID: "exp-a", Name: "a", SpecJSON: `{}`, CreatedAt: "2026-03-01T10:00:00Z"},
		{ID: "exp-b", Name: "b", SpecJSON: `{}`, CreatedAt: "2026-03-02T10:00:00Z"},
		{ID: "exp-c", Name: "c", SpecJSON: `{}`, CreatedAt: "2026-03-03T10:00:00Z"},
	}
	for _, r := range rows {
		if err := st.InsertExperiment(ctx, r); err != nil {
			t.Fatalf("InsertExperiment %s: %v", r.ID, err)
		}
	}

	all, err := st.ListExperiments(ctx, 0)
	if err != nil {
		t.Fatalf("ListExperiments: %v", err)
	}
	if len(all) != 3 || all[0].ID != "exp-c" || all[2].ID != "exp-a" {
		t.Fatalf("ordering = %v, want c,b,a", experimentIDs(all))
	}

	limited, err := st.ListExperiments(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID != "exp-c" || limited[1].ID != "exp-b" {
		t.Fatalf("limit = %v, want c,b", experimentIDs(limited))
	}
}

func TestListExperimentsEmpty(t *testing.T) {
	st := profileStore(t)
	got, err := st.ListExperiments(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListExperiments: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty = %v, want no rows", experimentIDs(got))
	}
}

func experimentIDs(exps []ExperimentRow) []string {
	out := make([]string, len(exps))
	for i, e := range exps {
		out[i] = e.ID
	}
	return out
}
