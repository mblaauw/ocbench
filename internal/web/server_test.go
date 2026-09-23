package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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

	rec := get(t, web.NewHandler(st), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("body does not look like an HTML document:\n%s", body)
	}
	if !strings.Contains(body, "run-1") {
		t.Errorf("body does not include the seeded run id:\n%s", body)
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

	rec := get(t, web.NewHandler(st), "/")

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
