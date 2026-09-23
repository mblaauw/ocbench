package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// UsageError marks an error as a usage or configuration mistake, which maps to
// exit code 2 (spec section 8): flag-parse failures, positional-argument
// validation failures and unknown subcommands.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// signalContext returns a context that is cancelled when the process receives
// SIGINT (Ctrl-C) or SIGTERM. The returned stop function restores the default
// signal handling and must be deferred by the caller.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Execute runs the CLI with the process arguments and returns the exit code.
func Execute() int {
	ctx, stop := signalContext()
	defer stop()
	if err := execute(ctx, os.Args[1:], Deps{}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitCode(err)
	}
	return 0
}

// execute builds the command tree with the supplied dependencies and runs it.
// It returns the command error rather than exiting so tests can assert on it.
func execute(ctx context.Context, args []string, d Deps) error {
	root := NewRootWithDeps(d)
	root.SetArgs(args)
	// Resolve the target before executing so an unknown subcommand is reported
	// as a usage error (exit 2) instead of silently falling through to cobra's
	// help output. The default help and completion commands must be registered
	// first so the pre-resolution does not reject `ocbench help` or
	// `ocbench completion ...`.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	// The hidden completion protocol is registered by ExecuteC, so it cannot be
	// resolved here; let cobra handle it during execution.
	if len(args) > 0 && strings.HasPrefix(args[0], "__complete") {
		return root.ExecuteContext(ctx)
	}
	if _, _, err := root.Find(args); err != nil {
		return &UsageError{Err: err}
	}
	return root.ExecuteContext(ctx)
}

// exitCode maps an execute error onto the documented process exit code: 0 when
// there is no error, 3 for an opted-in task failure, 2 for a usage/config
// error, 1 for any other (infrastructure) failure.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return 2
	}
	if errors.Is(err, ErrTaskFailure) {
		return 3
	}
	return 1
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
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UsageError{Err: err}
	})
	root.AddCommand(newVersionCmd())
	root.AddCommand(newDoctorCmd(d))
	root.AddCommand(newSnapshotCmd(d))
	root.AddCommand(newRunCmd(d))
	root.AddCommand(newHistoryCmd(d))
	root.AddCommand(newCompareCmd(d))
	root.AddCommand(newServeCmd(d))
	return root
}

// usageArgs wraps a positional-argument validator so its failure is classified
// as a usage error (exit 2) rather than a generic command failure.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return &UsageError{Err: err}
		}
		return nil
	}
}
