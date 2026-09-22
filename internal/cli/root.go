package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the CLI with the process arguments and returns the exit code.
func Execute() int {
	if err := execute(context.Background(), os.Args[1:], Deps{}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// execute builds the command tree with the supplied dependencies and runs it.
// It returns the command error rather than exiting so tests can assert on it.
func execute(ctx context.Context, args []string, d Deps) error {
	root := NewRootWithDeps(d)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

// NewRoot builds the command tree with lazily resolved dependencies.
func NewRoot() *cobra.Command { return NewRootWithDeps(Deps{}) }

// NewRootWithDeps builds the command tree. Zero Deps fields are resolved
// lazily and per command through Deps.resolve.
func NewRootWithDeps(d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "ocbench",
		Short:         "Benchmark the resolved OpenCode execution profile",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newDoctorCmd(d))
	root.AddCommand(newSnapshotCmd(d))
	return root
}
