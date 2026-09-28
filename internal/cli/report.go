package cli

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/report"
)

// newReportCmd renders a portable, human-readable controlled-cohort report.
// It is read-only: reports are derived from stored runs and never invoke
// OpenCode or write to the store.
func newReportCmd(d Deps) *cobra.Command {
	format := "md"
	cmd := &cobra.Command{
		Use:   "report <experiment-id>",
		Short: "Render a portable efficiency report for one experiment",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "md" && format != "html" {
				return &UsageError{Err: fmt.Errorf("--format must be md or html, got %q", format)}
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
			switch format {
			case "md":
				_, err = cmd.OutOrStdout().Write(report.Markdown(summary))
			case "html":
				var body []byte
				body, err = report.HTML(summary)
				if err == nil {
					_, err = cmd.OutOrStdout().Write(body)
				}
			}
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "report format: md or html")
	return cmd
}
