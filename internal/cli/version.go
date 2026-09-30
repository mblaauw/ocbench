package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/version"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print ocbench build metadata",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), version.Info())
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
