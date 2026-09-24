package web_test

import (
	"context"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/web"
)

// seedScoredProfile inserts a profile with a component set and one scored run.
func seedScoredProfile(t *testing.T, st *store.Store, id, suite, task string, score, success, cost float64) {
	t.Helper()
	ctx := context.Background()
	profileID := "profile-" + id
	hash := "hash-" + id
	if err := st.InsertProfile(ctx, store.ProfileRow{
		ID: profileID, ProfileHash: hash, OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{"schema":1}`,
		CreatedAt: "2026-01-01T00:00:00Z",
	}, []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{"default_agent":"build","model":"opencode-go/deepseek-v4.1-flash","variant":"high"}`},
		{Kind: "agent", Name: "build", Hash: "h-build", CanonicalJSON: `{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash","variant":"high"}`},
	}); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	exit := 0
	dur := int64(1500)
	runID := "run-" + id + "-" + task
	if err := st.InsertRun(ctx, store.RunRow{
		ID: runID, ProfileID: profileID, ProfileHash: hash,
		SuiteName: suite, SuiteVersion: "1", SuiteHash: "hash-" + suite,
		TaskID: task, TaskVersion: "1", FixtureSHA: "fixture",
		OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", Model: "p/m", Agent: "build",
		Status: "passed", ExitCode: &exit, StartedAt: "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:00:01Z", DurationMS: &dur, ArtifactsDir: "/runs/" + runID,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, runID, map[string]float64{
		"score": score, "success": success, "tokens_total": 1000, "cost": cost,
	}); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
}

func TestOverviewRendersLeaderboard(t *testing.T) {
	st := testStore(t)
	seedScoredProfile(t, st, "a", "core", "task-one", 1.0, 1, 0.02)
	seedScoredProfile(t, st, "b", "hard", "task-two", 0.4, 1, 0.05)

	rec := get(t, web.NewHandler(st), "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"Which setup scores best?",
		"Leaderboard",
		"Score by suite",
		"Score vs. cost per solved task",
		"build · deepseek-v4.1-flash high", // the architecture label
		"hash-a",                           // a profile link
		"scope=",                           // the suite switcher
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview body missing %q", want)
		}
	}
	// The leading profile scores 1.00 and its run passed.
	if !strings.Contains(body, "1.00") {
		t.Error("leader score missing")
	}
	// A scatter dot per scored profile.
	if got := strings.Count(body, `<circle`); got != 2 {
		t.Errorf("scatter dots = %d, want 2", got)
	}
	// Both suites appear as matrix columns.
	if !strings.Contains(body, ">core<") || !strings.Contains(body, ">hard<") {
		t.Error("matrix columns missing")
	}
}

func TestOverviewEmptyStateSaysSo(t *testing.T) {
	st := testStore(t)

	rec := get(t, web.NewHandler(st), "/")
	body := rec.Body.String()

	if !strings.Contains(body, "No scored runs in this scope yet") {
		t.Errorf("expected an honest empty state, got:\n%s", body)
	}
	if strings.Contains(body, "<circle") {
		t.Error("an empty store must not draw scatter points")
	}
}

func TestOverviewScopeFiltersSuites(t *testing.T) {
	st := testStore(t)
	seedScoredProfile(t, st, "a", "core", "task-one", 1.0, 1, 0.02)
	seedScoredProfile(t, st, "b", "hard", "task-two", 0.2, 0, 0.05)

	rec := get(t, web.NewHandler(st), "/?scope=hard")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// In the hard scope only the 0.20 profile is scored, so it leads.
	if !strings.Contains(body, "0.20") {
		t.Errorf("hard scope should show the 0.20 score:\n%s", body)
	}
	if strings.Contains(body, "1.00") {
		t.Error("hard scope must not include the core profile's score")
	}
}

func TestOverviewListsProfilesWithoutRuns(t *testing.T) {
	st := testStore(t)
	seedScoredProfile(t, st, "a", "core", "task-one", 1.0, 1, 0.02)
	// A profile with no runs at all.
	if err := st.InsertProfile(context.Background(), store.ProfileRow{
		ID: "profile-idle", ProfileHash: "hash-idle", OpenCodeVersion: "1.18.32",
		OCBenchVersion: "dev", CanonicalJSON: `{"schema":1}`, CreatedAt: "2026-01-02T00:00:00Z",
	}, []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{"default_agent":"build","model":"opencode-go/deepseek-v4-flash"}`},
	}); err != nil {
		t.Fatalf("insert idle profile: %v", err)
	}

	rec := get(t, web.NewHandler(st), "/")
	body := rec.Body.String()

	if !strings.Contains(body, "hash-idle") {
		t.Error("a profile with no runs should still be listed")
	}
	// It is listed but not scored: its score cell shows a dash.
	if strings.Count(body, "—") < 1 {
		t.Error("expected an unscored placeholder for the profile without runs")
	}
}

func TestOverviewSecurityHeaders(t *testing.T) {
	st := testStore(t)
	seedScoredProfile(t, st, "a", "core", "task-one", 0.5, 1, 0.01)

	rec := get(t, web.NewHandler(st), "/")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("CSP %q is not restrictive", got)
	}
	// No script may be served: the dashboard is server-rendered HTML only.
	if strings.Contains(rec.Body.String(), "<script") {
		t.Error("overview must not emit script tags")
	}
}
