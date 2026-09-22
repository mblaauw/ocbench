package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func Execute() int {
	root := NewRoot()
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "ocbench",
		Short:         "Benchmark the resolved OpenCode execution profile",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}
