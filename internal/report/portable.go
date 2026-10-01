package report

import (
	"encoding/json"
	"fmt"
	"io"

	"mbl/ocbench/internal/experiment"
)

// portableSchemaVersion is the envelope version of an exported cohort summary.
// It is bumped when the shape changes incompatibly; FromJSON refuses anything
// it does not understand rather than misreading a neighbour's result.
const portableSchemaVersion = 1

// portableSummary is the exchange envelope. It wraps the same derived view the
// dashboard renders, so an imported result is displayed through exactly one code
// path. It carries no run artifacts, prompts or session text: a cohort summary
// is already reduced to counts, medians and configuration differences, and the
// profile hashes are opaque fingerprints.
type portableSummary struct {
	SchemaVersion int                      `json:"schema_version"`
	Tool          string                   `json:"tool"`
	Summary       experiment.CohortSummary `json:"summary"`
}

// JSON renders one cohort summary as a portable, versioned document that another
// user can import and read without this user's store.
func JSON(summary experiment.CohortSummary) ([]byte, error) {
	return json.MarshalIndent(portableSummary{
		SchemaVersion: portableSchemaVersion,
		Tool:          "ocbench",
		Summary:       summary,
	}, "", "  ")
}

// WriteJSON writes the portable summary to w.
func WriteJSON(w io.Writer, summary experiment.CohortSummary) error {
	encoded, err := JSON(summary)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

// FromJSON reads a portable summary produced by JSON. An unknown schema version
// is an error rather than a best-effort parse, so a future format cannot be
// silently misread as this one.
func FromJSON(data []byte) (experiment.CohortSummary, error) {
	var envelope portableSummary
	if err := json.Unmarshal(data, &envelope); err != nil {
		return experiment.CohortSummary{}, fmt.Errorf("read cohort summary: %w", err)
	}
	if envelope.SchemaVersion != portableSchemaVersion {
		return experiment.CohortSummary{}, fmt.Errorf(
			"cohort summary schema version %d is not supported (this build reads version %d)",
			envelope.SchemaVersion, portableSchemaVersion)
	}
	if envelope.Tool != "ocbench" {
		return experiment.CohortSummary{}, fmt.Errorf("not an ocbench cohort summary: tool = %q", envelope.Tool)
	}
	return envelope.Summary, nil
}
