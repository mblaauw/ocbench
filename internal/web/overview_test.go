package web_test

import (
	"strings"
	"testing"

	"mbl/ocbench/internal/web"
)

// The landing page deliberately refuses to pool independent historic runs into
// a winner. A controlled experiment is the comparable unit, so a fresh store
// must direct the user to create one rather than show a synthetic leaderboard.
func TestOverviewDirectsUsersToControlledCohorts(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "historic-run", "task-one")

	body := get(t, web.NewHandler(st), "/").Body.String()
	if !strings.Contains(body, "Controlled cohorts") {
		t.Fatalf("landing page does not explain controlled cohorts:\n%s", body)
	}
	if !strings.Contains(body, "No controlled cohorts yet") {
		t.Fatalf("landing page does not state the evidence gate:\n%s", body)
	}
	if strings.Contains(body, "Profile leaderboard") || strings.Contains(body, "Which setup scores best?") {
		t.Fatalf("landing page still ranks mixed historic runs:\n%s", body)
	}
}

func TestCohortLandingKeepsSecurityAndFirstPartyEnhancement(t *testing.T) {
	st := testStore(t)
	rec := get(t, web.NewHandler(st), "/")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("CSP %q is not restrictive", got)
	}
	if !strings.Contains(rec.Body.String(), `src="/static/dashboard.js"`) {
		t.Error("cohort landing must load the first-party dashboard enhancement")
	}
}
