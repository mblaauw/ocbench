package report

import (
	"strings"
	"testing"

	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

func TestPortableReportsEscapeStoredTextAndMarkUnknownMetrics(t *testing.T) {
	summary := experiment.CohortSummary{
		Experiment: store.ExperimentRow{ID: "exp`1", Name: "# unsafe | [name]", CreatedAt: "2026-01-01"},
		Gate:       "# not eligible | <unsafe>",
		Arms: []experiment.CohortArm{{
			Label: "arm|one", ProfileHash: "hash`one", TaskCount: 0, RunnerEnvironments: []string{"unknown"},
		}},
	}

	markdown := string(Markdown(summary))
	for _, want := range []string{`\# unsafe \| \[name\]`, `\# not eligible \| \<unsafe\>`, "—"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}

	html, err := HTML(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<unsafe>") || !strings.Contains(string(html), "&lt;unsafe&gt;") {
		t.Fatalf("HTML does not escape stored values:\n%s", html)
	}
}

// A portable report exists so someone else can learn from it. A table of
// profile hashes teaches nothing about which setup to copy, so each arm carries
// the configuration changes that separate it from the baseline.
func TestReportNamesTheArchitectureChangesAgainstTheBaseline(t *testing.T) {
	summary := experiment.CohortSummary{
		Experiment: store.ExperimentRow{ID: "exp-1", Name: "core efficiency", CreatedAt: "2026-01-01"},
		Gate:       "eligible",
		Baseline:   "lean",
		Arms: []experiment.CohortArm{
			{Label: "lean", ProfileHash: "hash-lean", TaskCount: 3, MinRepeats: 3, Eligible: true, RunnerEnvironments: []string{"darwin/arm64 · 12 CPU"}},
			{Label: "delegated", ProfileHash: "hash-delegated", TaskCount: 3, MinRepeats: 3, Eligible: true, RunnerEnvironments: []string{"darwin/arm64 · 12 CPU"}},
		},
	}
	summary.Arms[1].Changes = []profile.ChangeNote{
		{Sign: "+", Change: "added", Kind: "agent", Name: "reviewer"},
		{Sign: "~", Change: "changed", Kind: "permissions", Name: "build", Note: "task: off→on"},
	}

	markdown := string(Markdown(summary))
	for _, want := range []string{"reviewer", "task: off→on", "delegated"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q, so a reader cannot tell what the arm changed:\n%s", want, markdown)
		}
	}
	// The change must be attributed to the arm that differs, not to the baseline.
	if strings.Contains(markdown, "| lean | + |") {
		t.Errorf("change attributed to the baseline arm:\n%s", markdown)
	}

	body, err := HTML(summary)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	// html/template escapes the arrow, so compare on the parts a reader sees.
	for _, want := range []string{"reviewer", "permissions/build", "off"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("html missing %q:\n%s", want, body)
		}
	}
}
