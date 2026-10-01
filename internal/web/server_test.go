package web_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/web"
)

// testStore opens a migrated temp SQLite store. No real OpenCode or network is
// involved.
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

// seedRun inserts a profile and one passed run so the listing has content.
func seedRun(t *testing.T, st *store.Store, id, taskID string) {
	t.Helper()
	ctx := context.Background()
	profileID := "profile-" + id
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: profileID, ProfileHash: "hash-" + id, OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{"schema":1}`,
		CreatedAt: "2026-01-01T00:00:00Z",
	}, []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	exit := 0
	dur := int64(1500)
	if err := st.InsertRun(ctx, store.RunRow{
		ID: id, ProfileID: profileID, ProfileHash: "hash-" + id,
		SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-hash",
		TaskID: taskID, TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "p/m", Agent: "build",
		Status: "passed", ExitCode: &exit, StartedAt: "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:00:01Z", DurationMS: &dur, ArtifactsDir: "/runs/" + id,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRootReturnsHTML(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "run-1", "py-bugfix")

	// `/` is the controlled-cohort landing page; the run listing lives at /runs.
	rec := get(t, web.NewHandler(st), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("body does not look like an HTML document:\n%s", body)
	}

	rec = get(t, web.NewHandler(st), "/runs")
	if rec.Code != http.StatusOK {
		t.Fatalf("/runs status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "run-1") {
		t.Errorf("/runs body does not include the seeded run id:\n%s", body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	st := testStore(t)

	rec := get(t, web.NewHandler(st), "/")

	for header, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy is empty")
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q is not restrictive (want default-src 'none')", csp)
	}
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-") {
		t.Errorf("CSP %q must permit only same-origin scripts", csp)
	}
}

func TestEscapesDatabaseContent(t *testing.T) {
	st := testStore(t)
	const payload = `<script>alert(1)</script>`
	seedRun(t, st, "run-x", payload)

	rec := get(t, web.NewHandler(st), "/runs")

	body := rec.Body.String()
	if strings.Contains(body, payload) {
		t.Errorf("unescaped payload present in body:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped payload missing from body:\n%s", body)
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	st := testStore(t)

	for _, target := range []string{"/nope", "/events.jsonl", "/artifacts/run-1/events.jsonl"} {
		rec := get(t, web.NewHandler(st), target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rec.Code)
		}
	}
}

func TestServesEmbeddedCSS(t *testing.T) {
	st := testStore(t)

	rec := get(t, web.NewHandler(st), "/static/site.css")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("embedded CSS is empty")
	}
}

// The dashboard's small progressive-enhancement layer is first-party. It must
// be served from the embedded asset tree rather than from a CDN.
func TestServesEmbeddedDashboardScript(t *testing.T) {
	st := testStore(t)
	rec := get(t, web.NewHandler(st), "/static/dashboard.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type = %q, want JavaScript", ct)
	}
	if !strings.Contains(rec.Body.String(), "data-theme") {
		t.Error("dashboard script does not contain theme enhancement")
	}
}

func TestSuitesPageEmptyState(t *testing.T) {
	st := testStore(t)
	rec := get(t, web.NewHandler(st), "/suites")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No suite definitions have been persisted yet") {
		t.Errorf("expected an honest empty catalogue, got:\n%s", rec.Body.String())
	}
}

func TestCohortsShowOnlyEvidenceGatedEfficiencyStandings(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	for _, profile := range []store.ProfileRow{
		{ID: "profile-base", ProfileHash: "hash-base", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "profile-lean", ProfileHash: "hash-lean", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"},
	} {
		if err := st.InsertProfile(ctx, profile, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{ID: "exp-cohort", Name: "core efficiency", SpecJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, arm := range []store.ExperimentArmRow{
		{ID: "arm-base", ExperimentID: "exp-cohort", Label: "base", ProfileID: stringPtr("profile-base"), ProfileHash: "hash-base", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "arm-lean", ExperimentID: "exp-cohort", Label: "lean", ProfileID: stringPtr("profile-lean"), ProfileHash: "hash-lean", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, arm); err != nil {
			t.Fatal(err)
		}
	}
	for _, arm := range []struct {
		id, profileID, hash string
		cost, tokens        float64
	}{
		{"arm-base", "profile-base", "hash-base", 2, 200},
		{"arm-lean", "profile-lean", "hash-lean", 1, 100},
	} {
		for repeat := 0; repeat < 3; repeat++ {
			armID := arm.id
			runID := fmt.Sprintf("%s-%d", arm.id, repeat)
			if err := st.InsertRun(ctx, store.RunRow{
				ID: runID, ExperimentID: "exp-cohort", ArmID: &armID, RepeatIndex: repeat,
				ProfileID: arm.profileID, ProfileHash: arm.hash,
				SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-h", TaskID: "task", TaskVersion: "1", FixtureSHA: "fixture-h",
				OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", RunnerEnv: "linux/amd64 · 4 CPU",
				Status: "passed", StartedAt: fmt.Sprintf("2026-01-01T00:00:0%dZ", repeat), ArtifactsDir: "/runs/" + runID,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertRunMetrics(ctx, runID, map[string]float64{"success": 1, "cost": arm.cost, "tokens_total": arm.tokens}); err != nil {
				t.Fatal(err)
			}
		}
	}

	h := web.NewHandler(st)
	list := get(t, h, "/cohorts")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "core efficiency") || !strings.Contains(list.Body.String(), "eligible") {
		t.Fatalf("cohort list = %d:\n%s", list.Code, list.Body.String())
	}
	detail := get(t, h, "/cohorts/exp-cohort")
	for _, want := range []string{"Efficiency standing", "lean", "$1.000000", "linux/amd64 · 4 CPU"} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("cohort detail missing %q (%d):\n%s", want, detail.Code, detail.Body.String())
		}
	}
	if rec := get(t, h, "/cohorts/missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing cohort status = %d, want 404", rec.Code)
	}
}

func stringPtr(value string) *string { return &value }

// theme.js is loaded synchronously from the head so a stored light mode applies
// before the first paint.
func TestServesEmbeddedThemeScript(t *testing.T) {
	st := testStore(t)
	rec := get(t, web.NewHandler(st), "/static/theme.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ocbench-theme") {
		t.Error("theme script does not read the stored colour mode")
	}
	body := get(t, web.NewHandler(st), "/").Body.String()
	if !strings.Contains(body, `src="/static/theme.js"`) {
		t.Error("base template does not load the theme bootstrap in the head")
	}
}

func TestSuitesPageListsPersistedTasks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertSuite(ctx, store.SuiteRow{
		ID: "suite-id", Name: "core", Version: "1", Hash: "suite-hash", Source: "embedded",
		ManifestJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	if err := st.InsertTask(ctx, store.TaskRow{
		SuiteID: "suite-id", TaskID: "task-one", Version: "1", Name: "One task",
		TagsJSON: `["debugging"]`, TimeoutSeconds: 300, FixtureSHA: "fixture", SpecJSON: `{}`,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	rec := get(t, web.NewHandler(st), "/suites")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, want := range []string{"Catalogue", "core", "task-one", "debugging"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("suites body missing %q", want)
		}
	}
}

// seedProfile inserts a profile with the given components.
func seedProfile(t *testing.T, st *store.Store, id, hash string, comps []store.ComponentRow) {
	t.Helper()
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: id, ProfileHash: hash, OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, comps); err != nil {
		t.Fatalf("insert profile %s: %v", id, err)
	}
}

// runRow builds a compatible run row for the dashboard fixtures.
func runRow(id, taskID, profileID, profileHash, started string) store.RunRow {
	exit := 0
	dur := int64(1000)
	return store.RunRow{
		ID: id, ProfileID: profileID, ProfileHash: profileHash,
		SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-hash",
		TaskID: taskID, TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "p/m", Agent: "build",
		Status: "passed", ExitCode: &exit, StartedAt: started, FinishedAt: started,
		DurationMS: &dur, ArtifactsDir: "/runs/" + id,
	}
}

func seedRunRow(t *testing.T, st *store.Store, r store.RunRow) {
	t.Helper()
	if err := st.InsertRun(context.Background(), r); err != nil {
		t.Fatalf("insert run %s: %v", r.ID, err)
	}
}

func TestRunPageShowsSafeSummaryAndEscapesExcerpt(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "run-1", "py-bugfix")
	if err := st.InsertRunMetrics(context.Background(), "run-1",
		map[string]float64{"tokens_total": 120, "tool_calls_total": 4}); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
	const payload = `<script>alert(1)</script>`
	if err := st.InsertRunValidations(context.Background(), "run-1", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "passed", ExitCode: 0,
			DurationMS: 12, OutputPath: "/runs/run-1/events.jsonl", OutputExcerpt: payload},
	}); err != nil {
		t.Fatalf("insert validations: %v", err)
	}

	rec := get(t, web.NewHandler(st), "/runs/run-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"run-1", "Tokens", "120", "unit"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, payload) {
		t.Errorf("unescaped validation excerpt present:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped excerpt missing:\n%s", body)
	}
	for _, forbidden := range []string{"events.jsonl", "session.json", `{"schema":1}`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body leaks %q:\n%s", forbidden, body)
		}
	}
}

func TestRunPageMissingReturns404(t *testing.T) {
	st := testStore(t)

	for _, target := range []string{"/runs/nope", "/runs/..", "/runs/%2e%2e"} {
		if rec := get(t, web.NewHandler(st), target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

func TestComparePageRendersDeltasAndProfileChanges(t *testing.T) {
	st := testStore(t)
	seedProfile(t, st, "p1", "hash-1", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-1", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedProfile(t, st, "p2", "hash-2", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-2", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedRunRow(t, st, runRow("run-a", "py-bugfix", "p1", "hash-1", "2026-01-01T00:00:00Z"))
	seedRunRow(t, st, runRow("run-b", "py-bugfix", "p2", "hash-2", "2026-01-02T00:00:00Z"))
	ctx := context.Background()
	if err := st.InsertRunMetrics(ctx, "run-a", map[string]float64{"tokens_total": 100}); err != nil {
		t.Fatalf("insert run-a metrics: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, "run-b", map[string]float64{"tokens_total": 150}); err != nil {
		t.Fatalf("insert run-b metrics: %v", err)
	}

	rec := get(t, web.NewHandler(st), "/compare?a=run-a&b=run-b")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"run-a", "run-b", "tokens_total", "150", "50", "h-build-1", "h-build-2"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `{"mode":"primary"}`) {
		t.Errorf("body leaks canonical component JSON:\n%s", body)
	}
}

func TestCompareBadRequests(t *testing.T) {
	st := testStore(t)
	seedProfile(t, st, "p-a", "hash-a", nil)
	seedProfile(t, st, "p-b", "hash-b", nil)
	seedRunRow(t, st, runRow("run-a", "py-bugfix", "p-a", "hash-a", "2026-01-01T00:00:00Z"))
	seedRunRow(t, st, runRow("run-b", "other-task", "p-b", "hash-b", "2026-01-02T00:00:00Z"))

	cases := []struct {
		target string
		want   int
	}{
		{"/compare", http.StatusBadRequest},
		{"/compare?a=run-a", http.StatusBadRequest},
		{"/compare?b=run-b", http.StatusBadRequest},
		{"/compare?a=previous&b=previous", http.StatusBadRequest},
		{"/compare?a=run-a&b=run-b", http.StatusBadRequest}, // incompatible task
		{"/compare?a=run-a&b=nope", http.StatusNotFound},
		{"/compare?a=nope&b=run-b", http.StatusNotFound},
	}
	for _, tc := range cases {
		if rec := get(t, web.NewHandler(st), tc.target); rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d; body=%s", tc.target, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestProfilePageRedactsComponents(t *testing.T) {
	st := testStore(t)
	seedProfile(t, st, "p1", "hash-1", []store.ComponentRow{
		{Kind: "agent", Name: "build", Hash: "h-build", CanonicalJSON: `{"mode":"primary","secret":"x"}`},
	})

	rec := get(t, web.NewHandler(st), "/arch/hash-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"hash-1", "agent", "build", "h-build"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{`"secret"`, `{"mode":"primary","secret":"x"}`, `{"schema":1}`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body leaks %q:\n%s", forbidden, body)
		}
	}
}

func TestProfilePageMissingReturns404(t *testing.T) {
	st := testStore(t)

	if rec := get(t, web.NewHandler(st), "/profiles/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestForbiddenPathsReturn404(t *testing.T) {
	st := testStore(t)

	for _, target := range []string{
		"/artifacts/run-1/events.jsonl",
		"/events.jsonl",
		"/../ocbench.db",
		"/runs/../ocbench.db",
		"/profiles/../../ocbench.db",
		"/runs/run-1/events.jsonl",
	} {
		if rec := get(t, web.NewHandler(st), target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

func TestStoreBusyReturns503(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seedRun(t, st, "run-1", "py-bugfix")

	// Force a rollback journal and no wait so the lock is reported immediately
	// instead of blocking for the 5s busy timeout.
	if _, err := st.DB().Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if _, err := st.DB().Exec("PRAGMA busy_timeout=0"); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}

	locker, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open locker: %v", err)
	}
	defer locker.Close()
	if _, err := locker.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("begin exclusive: %v", err)
	}
	defer locker.Exec("ROLLBACK")

	rec := get(t, web.NewHandler(st), "/runs/run-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRunPageShowsSubagentRollUp covers the per-agent panel for a run that
// actually delegated: the live database has only single-agent runs, so the
// share bars would otherwise never be exercised.
func TestRunPageShowsSubagentRollUp(t *testing.T) {
	st := testStore(t)
	seedProfile(t, st, "profile-sub", "hash-sub", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h1", CanonicalJSON: `{"default_agent":"build"}`},
		{Kind: "agent", Name: "build", Hash: "h2",
			CanonicalJSON: `{"model":"opencode-go/deepseek-v4.1-flash","variant":"high"}`},
		{Kind: "agent", Name: "explore", Hash: "h3",
			CanonicalJSON: `{"model":"opencode-go/deepseek-v4.1-flash","variant":"low"}`},
		{Kind: "agent", Name: "general", Hash: "h4",
			CanonicalJSON: `{"model":"opencode-go/deepseek-v4.1-flash","variant":"low"}`},
	})
	exit := 0
	dur := int64(42000)
	seedRunRow(t, st, store.RunRow{
		ID: "run-sub", ProfileID: "profile-sub", ProfileHash: "hash-sub",
		SuiteName: "agentic", SuiteVersion: "1", SuiteHash: "suite-hash",
		TaskID: "repo-investigation", TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "m", Agent: "build",
		Status: "passed", ExitCode: &exit, StartedAt: "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:00:42Z", DurationMS: &dur, ArtifactsDir: "/runs/run-sub",
	})
	if err := st.InsertRunMetrics(context.Background(), "run-sub", map[string]float64{
		"score": 1, "success": 1, "tokens_total": 100000,
		"agent.build.messages": 6, "agent.build.tool_calls": 5, "agent.build.tokens_total": 40000,
		"agent.explore.messages": 4, "agent.explore.tool_calls": 2, "agent.explore.tokens_total": 45000,
		"agent.general.messages": 1, "agent.general.tool_calls": 0, "agent.general.tokens_total": 15000,
		"subagent_tokens_total": 60000,
	}); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}

	body := get(t, web.NewHandler(st), "/runs/run-sub").Body.String()

	for _, want := range []string{
		"Subagents via task",
		"explore", "75%", // 45000 of 60000 subagent tokens
		"general", "25%",
		"deepseek-v4.1-flash",
		"100k", // run token total
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The share bar is SVG geometry, because inline style attributes are
	// blocked by the page's content security policy.
	if !strings.Contains(body, `width="75"`) || !strings.Contains(body, `width="25"`) {
		t.Errorf("share bars not rendered as SVG geometry")
	}
	if strings.Contains(body, "No subagents") {
		t.Errorf("single-agent empty state shown for a delegating run")
	}
}

// TestRunPageUnknownIDIs404 pins the behaviour that an explicitly requested run
// that does not exist is an error, not a silently different run.
func TestRunPageUnknownIDIs404(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "run-1", "py-bugfix")

	for _, target := range []string{"/runs/nope", "/runs?run=nope"} {
		if rec := get(t, web.NewHandler(st), target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
	// A run that does exist still renders.
	if rec := get(t, web.NewHandler(st), "/runs?run=run-1"); rec.Code != http.StatusOK {
		t.Errorf("GET /runs?run=run-1 = %d, want 200", rec.Code)
	}
}

// seedArchProfile writes a profile with a primary agent, two subagents, an
// instruction file and a skill, which is what the architecture page renders.
func seedArchProfile(t *testing.T, st *store.Store, hash string) {
	t.Helper()
	seedProfile(t, st, "profile-"+hash, hash, []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h0", CanonicalJSON: `{"default_agent":"build"}`},
		{Kind: "agent", Name: "build", Hash: "h1", CanonicalJSON: `{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash",
			"variant":"high","steps":60,"native":true,"temperature":0.1,"options":{"reasoning":"high"},
			"tools":{"bash":true,"edit":true,"task":true}}`},
		{Kind: "permissions", Name: "permissions", Hash: "h8", CanonicalJSON: `{"by_agent":{"build":[
			{"permission":"task","pattern":"explore","action":"allow"},
			{"permission":"task","pattern":"*","action":"ask"}]}}`},
		{Kind: "agent", Name: "explore", Hash: "h2", CanonicalJSON: `{"mode":"subagent","model":"opencode-go/deepseek-v4.1-flash","tools":{"read":true}}`},
		{Kind: "agent", Name: "architect", Hash: "h3", CanonicalJSON: `{"mode":"subagent","model":"openai/gpt-5.6-sol","variant":"high"}`},
		{Kind: "instructions", Name: "global:AGENTS.md", Hash: "h4", CanonicalJSON: `{"path":"~/.config/opencode/AGENTS.md","sha256":"abc123"}`},
		{Kind: "skill", Name: "pentest", Hash: "h5", CanonicalJSON: `{"description":"Run a non-destructive pentest"}`},
		{Kind: "mcp", Name: "playwright", Hash: "h6", CanonicalJSON: `{"command":"npx","args":["-y","@playwright/mcp"]}`},
		{Kind: "plugin", Name: "superpowers", Hash: "h7", CanonicalJSON: `{}`},
	})
}

// seedCaptures writes capture files for hash under a temp profiles root and
// returns the root, so the handler can be built with WithPaths.
func seedCaptures(t *testing.T, hash string, files map[string]string) config.Paths {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return config.Paths{Profiles: root}
}

func TestArchitecturePageRendersAgentsTreeAndCapturedText(t *testing.T) {
	st := testStore(t)
	seedArchProfile(t, st, "hash-arch")
	paths := seedCaptures(t, "hash-arch", map[string]string{
		"agents.json": `[{"name":"build","mode":"primary","model":{"providerID":"opencode-go","modelID":"deepseek-v4.1-flash"},
			"prompt":"You are the build agent.","permission":[{"permission":"task","pattern":"*","action":"ask"}]}]`,
		"instructions.json": `{"global:AGENTS.md":"Never touch the user's data directory."}`,
		"skills.json":       `[{"name":"pentest","description":"Run a non-destructive pentest","location":"~/.config/opencode/skills/pentest","content":"# Pentest","content_sha256":"deadbeef"}]`,
	})

	rec := get(t, web.NewHandler(st, web.WithPaths(paths)), "/arch/hash-arch")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Primary agent", "Subagents via task", "Diff against", "Skills", "MCP servers", "Fingerprint",
		"Subagent tree", "Agents", "Instructions",
		"build", "explore", "architect", // the agents
		"You are the build agent.", // captured agent prompt
		// The apostrophe is escaped by html/template, so assert on a
		// fragment that survives escaping.
		"Never touch the user",          // captured instruction text
		"Run a non-destructive pentest", // skill description
		"deadbeef",                      // skill content hash
		"reasoning", "high",             // model options
		"opencode-go/deepseek-v4.1-flash", // model id
		"playwright", "superpowers",       // mcp + plugin
		"0.1", // temperature
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The subagent tree draws one trunk per primary agent with a branch per
	// subagent it may call, which is the shape the prototype uses.
	if !strings.Contains(body, `class="trunk"`) || !strings.Contains(body, `class="branch"`) {
		t.Errorf("subagent tree not rendered as trunks and branches")
	}
	if !strings.Contains(body, "may call") {
		t.Errorf("subagent tree does not say how many a primary may call")
	}
}

// A profile that exists only in the database has no capture files. The page must
// still render, saying so, rather than failing or implying empty text.
func TestArchitecturePageWithoutCapturesSaysSo(t *testing.T) {
	st := testStore(t)
	seedArchProfile(t, st, "hash-nocap")

	rec := get(t, web.NewHandler(st), "/arch/hash-nocap")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No capture files") {
		t.Errorf("missing capture disclosure")
	}
	if !strings.Contains(body, "abc123") {
		t.Errorf("instruction hash not shown when text is unavailable")
	}
}

func TestArchitecturePageComparesTwoProfiles(t *testing.T) {
	st := testStore(t)
	seedArchProfile(t, st, "hash-a")
	seedProfile(t, st, "profile-hash-b", "hash-b", []store.ComponentRow{
		{Kind: "agent", Name: "build", Hash: "h1", CanonicalJSON: `{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash"}`},
		{Kind: "skill", Name: "extra", Hash: "h9", CanonicalJSON: `{"description":"A skill only b has"}`},
	})

	body := get(t, web.NewHandler(st), "/arch/hash-b?against=hash-a").Body.String()
	if !strings.Contains(body, "Changes vs") {
		t.Fatalf("comparison section missing")
	}
	// b has a skill a does not, and a has agents b does not.
	for _, want := range []string{"extra", "architect"} {
		if !strings.Contains(body, want) {
			t.Errorf("comparison missing %q", want)
		}
	}
	if !strings.Contains(body, "&#43; changed") {
		t.Errorf("comparison did not annotate changed architecture cards")
	}
	if !strings.Contains(body, "Profile") || !strings.Contains(body, "/arch/hash-a") {
		t.Errorf("architecture page did not provide profile-switch links")
	}
	// Without ?against there is no comparison section.
	plain := get(t, web.NewHandler(st), "/arch/hash-b").Body.String()
	if strings.Contains(plain, "Changes vs") {
		t.Errorf("comparison rendered without ?against")
	}
}

// The architecture summary reports measured per-agent usage, and matches a
// configured agent name against the sanitised metric key the runner writes
// ("code-reviewer" → agent.code_reviewer.*).
func TestArchitectureSummaryShowsMeasuredAgentUsage(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertExperiment(ctx, store.ExperimentRow{ID: "exp-usage", Name: "usage cohort", SpecJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	seedProfile(t, st, "profile-usage", "hash-usage", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h0", CanonicalJSON: `{"default_agent":"build"}`},
		{Kind: "agent", Name: "build", Hash: "h1", CanonicalJSON: `{"mode":"primary","model":"m","tools":{"task":true}}`},
		{Kind: "agent", Name: "code-reviewer", Hash: "h2", CanonicalJSON: `{"mode":"subagent","model":"m"}`},
	})
	for _, arm := range []store.ExperimentArmRow{
		{ID: "usage-a", ExperimentID: "exp-usage", Label: "measured", ProfileID: stringPtr("profile-usage"), ProfileHash: "hash-usage", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "usage-b", ExperimentID: "exp-usage", Label: "other", ProfileHash: "hash-other", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, arm); err != nil {
			t.Fatal(err)
		}
	}
	exit := 0
	dur := int64(1000)
	armID := "usage-a"
	if err := st.InsertRun(ctx, store.RunRow{
		ID: "run-usage", ExperimentID: "exp-usage", ArmID: &armID, ProfileID: "profile-usage", ProfileHash: "hash-usage",
		SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-hash",
		TaskID: "task-one", TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "m", Agent: "build",
		Status: "passed", ExitCode: &exit, StartedAt: "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:00:01Z", DurationMS: &dur, ArtifactsDir: "/runs/run-usage",
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, "run-usage", map[string]float64{
		"agent.build.messages": 10, "agent.build.tokens_total": 800,
		"agent.code_reviewer.messages": 4, "agent.code_reviewer.tokens_total": 200,
	}); err != nil {
		t.Fatalf("metrics: %v", err)
	}

	body := get(t, web.NewHandler(st), "/arch/hash-usage?cohort=exp-usage").Body.String()
	for _, want := range []string{
		"Primary agent", "Subagents via task",
		"code-reviewer",          // the configured name
		"4.0× / run",             // sanitised subagent matched its metrics
		"80% of measured tokens", // primary token share
		"20%",                    // subagent token share
		"Agent messages / run",   // cohort-scoped workflow metric
		"Subagent token share",   // cohort-scoped workflow metric
		"Observed only in the selected controlled cohort.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("architecture summary missing %q", want)
		}
	}
	if plain := get(t, web.NewHandler(st), "/arch/hash-usage").Body.String(); !strings.Contains(plain, "Choose a controlled cohort") {
		t.Errorf("architecture without cohort did not disclose unscoped metrics")
	}
	// A profile with no measured runs must not invent a usage figure.
	seedArchProfile(t, st, "hash-nomeasured")
	if body := get(t, web.NewHandler(st), "/arch/hash-nomeasured").Body.String(); strings.Contains(body, "× / run") {
		t.Errorf("unmeasured profile rendered a per-run usage figure")
	}
}

// A cohort whose runs never recorded cost must render the cost as absent, not
// as a free configuration.
func TestArchitectureCohortCostAbsentWithoutCostMetrics(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertExperiment(ctx, store.ExperimentRow{ID: "exp-nocost", Name: "no cost", SpecJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	seedProfile(t, st, "profile-nocost", "hash-nocost", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h0", CanonicalJSON: `{"default_agent":"build"}`},
		{Kind: "agent", Name: "build", Hash: "h1", CanonicalJSON: `{"mode":"primary","model":"m"}`},
	})
	for _, arm := range []store.ExperimentArmRow{
		{ID: "nocost-a", ExperimentID: "exp-nocost", Label: "a", ProfileID: stringPtr("profile-nocost"), ProfileHash: "hash-nocost", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "nocost-b", ExperimentID: "exp-nocost", Label: "b", ProfileHash: "hash-other", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, arm); err != nil {
			t.Fatal(err)
		}
	}
	armID := "nocost-a"
	if err := st.InsertRun(ctx, store.RunRow{
		ID: "run-nocost", ExperimentID: "exp-nocost", ArmID: &armID,
		ProfileID: "profile-nocost", ProfileHash: "hash-nocost",
		SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-hash",
		TaskID: "task-one", TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "m", Agent: "build",
		Status: "passed", StartedAt: "2026-01-01T00:00:00Z", ArtifactsDir: "/runs/run-nocost",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertRunMetrics(ctx, "run-nocost", map[string]float64{"success": 1, "tokens_total": 100}); err != nil {
		t.Fatal(err)
	}

	body := get(t, web.NewHandler(st), "/arch/hash-nocost?cohort=exp-nocost").Body.String()
	if !strings.Contains(body, "Cost / solved") {
		t.Fatalf("cohort cost stat missing:\n%s", body)
	}
	if strings.Contains(body, "$0.000") {
		t.Errorf("a cost-less cohort rendered as free")
	}
}

func TestArchitectureIndexListsProfiles(t *testing.T) {
	st := testStore(t)
	seedArchProfile(t, st, "hash-idx")
	seedRun(t, st, "run-idx", "py-bugfix")

	rec := get(t, web.NewHandler(st), "/arch")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"hash-idx", "/arch/hash-idx", "Architecture"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	// The old /profiles route is gone rather than left as a dead alias.
	if rec := get(t, web.NewHandler(st), "/profiles"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /profiles = %d, want 404", rec.Code)
	}
}

func TestOverviewRemainsReachableAsExploratoryHistory(t *testing.T) {
	st := testStore(t)
	seedArchProfile(t, st, "hash-history")
	seedRun(t, st, "run-history", "py-bugfix")

	body := get(t, web.NewHandler(st), "/overview").Body.String()
	for _, want := range []string{"Exploratory profile history", "Historic observations are not repetitions", "not an efficiency ranking"} {
		if !strings.Contains(body, want) {
			t.Errorf("exploratory history missing %q", want)
		}
	}
	if strings.Contains(body, "Which setup scores best?") || strings.Contains(body, "Profile leaderboard") {
		t.Errorf("exploratory history still claims a global winner")
	}
}

// The run aside reports the cache hit rate and states plainly that cache writes
// are not reported, rather than showing a zero the provider never measured.
func TestRunPageShowsCacheHitRateAndDisclaimsCacheWrites(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "run-cache", "py-bugfix")
	if err := st.InsertRunMetrics(context.Background(), "run-cache", map[string]float64{
		"score": 1, "success": 1, "tokens_total": 1000,
		"tokens_cache_read": 750, "tokens_input": 250,
	}); err != nil {
		t.Fatalf("metrics: %v", err)
	}

	body := get(t, web.NewHandler(st), "/runs/run-cache").Body.String()
	if !strings.Contains(body, "Cache hit") || !strings.Contains(body, "75%") {
		t.Errorf("cache hit rate not rendered:\n%s", body)
	}
	if !strings.Contains(body, "Cache writes are not reported by OpenCode") {
		t.Errorf("cache-write disclaimer missing")
	}
}

// A run with no prompt tokens has no hit rate; the cell must be a dash rather
// than a zero that reads as a total miss.
func TestRunPageCacheHitIsAbsentWithoutPromptTokens(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "run-nocache", "py-bugfix")
	if err := st.InsertRunMetrics(context.Background(), "run-nocache", map[string]float64{
		"score": 1, "success": 1, "tokens_output": 100,
	}); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	body := get(t, web.NewHandler(st), "/runs/run-nocache").Body.String()
	if !strings.Contains(body, "Cache hit") {
		t.Fatal("cache hit cell missing")
	}
	if strings.Contains(body, "<span class=\"v\">0%</span>") {
		t.Errorf("a missing hit rate rendered as 0%%")
	}
}

// A single-arm session is not a controlled cohort, so its detail page must say
// so instead of rendering a standing that looks measured. It stays reachable:
// links from runs and the CLI's `experiment show` land here.
func TestSingleArmExperimentDetailExplainsItIsNotACohort(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: "profile-solo", ProfileHash: "hash-solo", OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{ID: "exp-solo", Name: "core@1.1.0", SpecJSON: `{}`, CreatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExperimentArm(ctx, store.ExperimentArmRow{
		ID: "arm-solo", ExperimentID: "exp-solo", Label: "baseline",
		ProfileID: stringPtr("profile-solo"), ProfileHash: "hash-solo", OverlayKind: "none",
		CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 3; repeat++ {
		runID := fmt.Sprintf("run-solo-%d", repeat)
		armID := "arm-solo"
		if err := st.InsertRun(ctx, store.RunRow{
			ID: runID, ExperimentID: "exp-solo", ArmID: &armID, RepeatIndex: repeat,
			ProfileID: "profile-solo", ProfileHash: "hash-solo",
			SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-h", TaskID: "task", TaskVersion: "1", FixtureSHA: "fixture-h",
			OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", RunnerEnv: "darwin/arm64 · 12 CPU",
			Status: "passed", StartedAt: fmt.Sprintf("2026-01-01T00:00:0%dZ", repeat), ArtifactsDir: "/runs/" + runID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertRunMetrics(ctx, runID, map[string]float64{"success": 1, "cost": 1, "tokens_total": 100}); err != nil {
			t.Fatal(err)
		}
	}

	h := web.NewHandler(st)
	list := get(t, h, "/cohorts")
	if !strings.Contains(list.Body.String(), "1 unarmed historic session(s) excluded") {
		t.Fatalf("cohort list must exclude the single-arm session:\n%s", list.Body.String())
	}
	if strings.Contains(list.Body.String(), "/cohorts/exp-solo") {
		t.Fatalf("single-arm session must not be listed as a cohort:\n%s", list.Body.String())
	}

	detail := get(t, h, "/cohorts/exp-solo")
	if detail.Code != http.StatusOK {
		t.Fatalf("single-arm detail status = %d, want 200 (links must not dead-end)", detail.Code)
	}
	if !strings.Contains(detail.Body.String(), "not a controlled cohort") {
		t.Fatalf("single-arm detail must disclose that it is not a cohort:\n%s", detail.Body.String())
	}
}

// A profile whose stored configuration cannot be decoded must say so in the
// list. An empty architecture cell would read as "this profile has no
// architecture", which is a different claim.
func TestProfilesListMarksUndecodableProfiles(t *testing.T) {
	st := testStore(t)
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: "profile-broken", ProfileHash: "hash-broken", OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{"schema":1`, CreatedAt: "2026-01-01T00:00:00Z",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: "profile-fine", ProfileHash: "hash-fine", OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, []store.ComponentRow{{Kind: "agent", Name: "build", Hash: "h1", CanonicalJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	body := get(t, web.NewHandler(st), "/arch").Body.String()
	if !strings.Contains(body, "unreadable") {
		t.Fatalf("profile list must disclose the undecodable profile:\n%s", body)
	}
	if strings.Count(body, "unreadable") != 1 {
		t.Fatalf("only the broken profile should be marked unreadable:\n%s", body)
	}
}

// The cohort page must state what each arm changed against the baseline, not
// just which profile hash won, so a reader knows what to copy.
func TestCohortPageShowsWhatEachArmChanged(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: "p1", ProfileHash: "hash-base", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1,"agent":{"build":{"description":"works alone"}}}`, CreatedAt: "2026-01-01T00:00:00Z",
	}, []store.ComponentRow{{Kind: "primary", Name: "build", Hash: "hb", CanonicalJSON: `{"model":"m"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: "p2", ProfileHash: "hash-cand", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev",
		CanonicalJSON: `{"schema":1,"agent":{"build":{"description":"works alone"},"reviewer":{"description":"reads diffs"}}}`,
		CreatedAt:     "2026-01-01T00:00:00Z",
	}, []store.ComponentRow{
		{Kind: "primary", Name: "build", Hash: "hb", CanonicalJSON: `{"model":"m"}`},
		{Kind: "agent", Name: "reviewer", Hash: "hr", CanonicalJSON: `{"model":"m"}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{
		ID: "exp-diff", Name: "core efficiency", SpecJSON: `{"baseline":"base","tasks":["task"]}`, CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, arm := range []store.ExperimentArmRow{
		{ID: "arm-b", ExperimentID: "exp-diff", Label: "base", ProfileHash: "hash-base", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "arm-c", ExperimentID: "exp-diff", Label: "candidate", ProfileHash: "hash-cand", OverlayKind: "none", CreatedAt: "2026-01-01T00:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, arm); err != nil {
			t.Fatal(err)
		}
	}
	for repeat := 0; repeat < 3; repeat++ {
		for _, arm := range []struct{ id, pid, hash string }{{"arm-b", "p1", "hash-base"}, {"arm-c", "p2", "hash-cand"}} {
			runID := arm.id + "-r" + string(rune('0'+repeat))
			armID := arm.id
			if err := st.InsertRun(ctx, store.RunRow{
				ID: runID, ExperimentID: "exp-diff", ArmID: &armID, RepeatIndex: repeat,
				ProfileID: arm.pid, ProfileHash: arm.hash,
				SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-h", TaskID: "task", TaskVersion: "1", FixtureSHA: "fixture-h",
				OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", RunnerEnv: "darwin/arm64 · 12 CPU",
				Status: "passed", StartedAt: fmt.Sprintf("2026-01-01T00:00:0%dZ", repeat), ArtifactsDir: "/runs/" + runID,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertRunMetrics(ctx, runID, map[string]float64{"success": 1, "cost": 1, "tokens_total": 100}); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := get(t, web.NewHandler(st), "/cohorts/exp-diff").Body.String()
	for _, want := range []string{"What changed", "reviewer", "candidate"} {
		if !strings.Contains(body, want) {
			t.Errorf("cohort page missing %q, so a reader cannot see what the arm changed:\n%s", want, body)
		}
	}
}
