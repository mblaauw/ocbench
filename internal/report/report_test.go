package report

import (
	"strings"
	"testing"

	"mbl/ocbench/internal/experiment"
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
