package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// isUsageError reports whether err is (or wraps) a UsageError.
func isUsageError(err error) bool {
	var usage *UsageError
	return errors.As(err, &usage)
}

func TestExecuteUnknownFlagIsUsageError(t *testing.T) {
	err := execute(context.Background(), []string{"snapshot", "--nope"}, Deps{})
	if err == nil {
		t.Fatal("snapshot --nope: expected an error")
	}
	if !isUsageError(err) {
		t.Fatalf("snapshot --nope error = %v, want a UsageError", err)
	}
}

func TestExecuteUnknownSubcommandIsUsageError(t *testing.T) {
	err := execute(context.Background(), []string{"bogus"}, Deps{})
	if err == nil {
		t.Fatal("unknown subcommand: expected an error")
	}
	if !isUsageError(err) {
		t.Fatalf("unknown subcommand error = %v, want a UsageError", err)
	}
}

func TestExecuteRejectsPositionalArgsAsUsageError(t *testing.T) {
	err := execute(context.Background(), []string{"version", "extra"}, Deps{})
	if err == nil {
		t.Fatal("version extra: expected an error")
	}
	if !isUsageError(err) {
		t.Fatalf("version extra error = %v, want a UsageError", err)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "plain error", err: errors.New("boom"), want: 1},
		{name: "usage error", err: &UsageError{Err: errors.New("bad flag")}, want: 2},
		{name: "wrapped usage error", err: fmt.Errorf("run: %w", &UsageError{Err: errors.New("bad flag")}), want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
