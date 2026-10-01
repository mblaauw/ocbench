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

// Sharing a result is the point of the tool: a second user must be able to read
// and render someone else's cohort without that user's store or runs. The
// portable summary is the exchange artifact, so it round-trips through JSON and
// still renders the standing and the architecture differences.
func TestPortableSummaryRoundTripsAndRenders(t *testing.T) {
	original := experiment.CohortSummary{
		Experiment:         store.ExperimentRow{ID: "exp-9", Name: "core efficiency", CreatedAt: "2026-10-01T10:00:00Z"},
		Gate:               "eligible: three or more validated runs per task for every configuration",
		Baseline:           "lean",
		Ranked:             true,
		RunnerEnvironments: []string{"darwin/arm64 · 12 CPU"},
		Arms: []experiment.CohortArm{
			{Label: "lean", ProfileHash: "hash-lean", TaskCount: 3, MinRepeats: 3, PassRate: 1, Score: 0.94, ScoreOK: true,
				CostPerSolved: 0.02, CostPerSolvedOK: true, MedianTokens: 41000, MedianTokensOK: true, Eligible: true},
			{Label: "delegated", ProfileHash: "hash-delegated", TaskCount: 3, MinRepeats: 3, PassRate: 1, Score: 0.97, ScoreOK: true,
				CostPerSolved: 0.064, CostPerSolvedOK: true, MedianTokens: 96000, MedianTokensOK: true, Eligible: true,
				Changes: []profile.ChangeNote{{Sign: "+", Change: "added", Kind: "agent", Name: "reviewer", Note: "nemotron-3.5-lightning-free"}}},
		},
	}

	encoded, err := JSON(original)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(encoded), `"schema_version"`) {
		t.Errorf("portable summary has no schema version, so a reader cannot tell what it is:\n%s", encoded)
	}

	restored, err := FromJSON(encoded)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if restored.Baseline != "lean" || len(restored.Arms) != 2 {
		t.Fatalf("restored summary lost structure: %+v", restored)
	}
	if restored.Arms[1].Changes[0].Name != "reviewer" {
		t.Errorf("restored summary lost the architecture diff: %+v", restored.Arms[1].Changes)
	}
	if !restored.Arms[0].ScoreOK || restored.Arms[0].Score != 0.94 {
		t.Errorf("restored summary lost the graded score: %+v", restored.Arms[0])
	}

	// A reader who has never seen this cohort renders it exactly as their own.
	markdown := string(Markdown(restored))
	for _, want := range []string{"lean", "delegated", "$0.020000", "$0.064000", "0.94", "reviewer"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("rendered shared summary missing %q:\n%s", want, markdown)
		}
	}
}

func TestFromJSONRejectsAnUnknownSchemaVersion(t *testing.T) {
	if _, err := FromJSON([]byte(`{"schema_version":99}`)); err == nil {
		t.Fatal("a summary from an unknown schema version must not be accepted silently")
	}
}
