package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

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

	// `/` is the profile leaderboard; the run listing lives at /runs.
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

	rec := get(t, web.NewHandler(st), "/profiles/hash-1")

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
