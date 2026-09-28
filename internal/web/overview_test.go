package web_test

import (
	"net/http"
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

func TestLegacyRootScopeRedirectsToExploratoryHistory(t *testing.T) {
	st := testStore(t)
	rec := get(t, web.NewHandler(st), "/?scope=core")
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /?scope=core = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/overview?scope=core" {
		t.Fatalf("redirect location = %q, want /overview?scope=core", got)
	}
}

func TestScopedExploratoryHistoryKeepsTheAllLayout(t *testing.T) {
	st := testStore(t)
	seedRun(t, st, "core-history", "task-one")

	for _, tt := range []struct{ path, current string }{
		{"/overview", `href="/overview" aria-current="true">all</a>`},
		{"/overview?scope=all", `href="/overview" aria-current="true">all</a>`},
		{"/overview?scope=core", `href="/overview?scope=core" aria-current="true">core</a>`},
	} {
		body := get(t, web.NewHandler(st), tt.path).Body.String()
		for _, want := range []string{"Recorded profile observations", "Historic score by suite", "Historic score and cost"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing stable layout section %q", tt.path, want)
			}
		}
		if !strings.Contains(body, tt.current) {
			t.Errorf("%s does not mark its scope current", tt.path)
		}
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
