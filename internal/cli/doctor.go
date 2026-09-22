package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/doctor"
)

// ErrUnhealthy is returned by `doctor` when at least one check failed. It is a
// sentinel so callers and tests can tell an unhealthy environment from a
// command-level error.
var ErrUnhealthy = errors.New("doctor: environment is not healthy")

func newDoctorCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the local environment is ready to benchmark",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			report, err := doctor.Run(cmd.Context(), resolved.Adapter, resolved.Paths, resolved.Config)
			if err != nil {
				return err
			}
			if err := renderDoctor(cmd.OutOrStdout(), report, asJSON); err != nil {
				return err
			}
			if !report.Healthy() {
				return ErrUnhealthy
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// renderDoctor prints the report as an aligned table or as indented JSON.
func renderDoctor(w io.Writer, report doctor.Report, asJSON bool) error {
	if asJSON {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tSTATUS\tDETAIL"); err != nil {
		return err
	}
	ok, warn, fail := 0, 0, 0
	for _, c := range report.Checks {
		switch c.Status {
		case doctor.StatusOK:
			ok++
		case doctor.StatusWarn:
			warn++
		case doctor.StatusFail:
			fail++
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, c.Status, c.Detail); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d ok, %d warn, %d fail\n", ok, warn, fail)
	return err
}
