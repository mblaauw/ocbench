package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/report"
)

// newReportCmd renders a portable, human-readable controlled-cohort report.
// It is read-only: reports are derived from stored runs and never invoke
// OpenCode or write to the store.
func newReportCmd(d Deps) *cobra.Command {
	format := "md"
	importPath := ""
	cmd := &cobra.Command{
		Use:   "report <experiment-id>",
		Short: "Render a portable efficiency report for one experiment",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --import reads a neighbour's exported cohort summary and renders
			// it through the same path as a local one, so a shared result is
			// displayed identically without importing any runs.
			if importPath != "" {
				if len(args) > 0 {
					return &UsageError{Err: fmt.Errorf("--import renders the summary in the given file; drop the experiment id")}
				}
				data, err := os.ReadFile(importPath)
				if err != nil {
					return &UsageError{Err: fmt.Errorf("--import %s: %w", importPath, err)}
				}
				summary, err := report.FromJSON(data)
				if err != nil {
					return &UsageError{Err: err}
				}
				return renderCohortReport(cmd, summary, format)
			}
			if len(args) == 0 {
				return &UsageError{Err: fmt.Errorf("report needs an experiment id, or --import <file>")}
			}
			if format != "md" && format != "html" && format != "json" {
				return &UsageError{Err: fmt.Errorf("--format must be md, html or json, got %q", format)}
			}
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			st, err := openHistoryStore(cmd.Context(), resolved)
			if err != nil {
				return err
			}
			defer st.Close()
			if _, err := st.GetExperiment(cmd.Context(), args[0]); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return &UsageError{Err: fmt.Errorf("experiment %q does not exist", args[0])}
				}
				return err
			}
			summary, err := experiment.SummarizeCohort(cmd.Context(), st, args[0])
			if err != nil {
				return err
			}
			return renderCohortReport(cmd, summary, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "report format: md, html or json")
	cmd.Flags().StringVar(&importPath, "import", "", "render a cohort summary exported by another user (json)")
	return cmd
}

// renderCohortReport writes one cohort summary in the requested format. `json`
// is the exchange format: it is what another user imports, and it carries the
// standing together with the arm-to-arm configuration differences.
func renderCohortReport(cmd *cobra.Command, summary experiment.CohortSummary, format string) error {
	out := cmd.OutOrStdout()
	switch format {
	case "md":
		_, err := out.Write(report.Markdown(summary))
		return err
	case "html":
		body, err := report.HTML(summary)
		if err != nil {
			return err
		}
		_, err = out.Write(body)
		return err
	case "json":
		return report.WriteJSON(out, summary)
	default:
		return &UsageError{Err: fmt.Errorf("--format must be md, html or json, got %q", format)}
	}
}
