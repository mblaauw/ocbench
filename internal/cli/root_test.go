package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

// isUsageError reports whether err is (or wraps) a UsageError.
func isUsageError(err error) bool {
	var usage *UsageError
	return errors.As(err, &usage)
}

// silenceOutput redirects os.Stdout and os.Stderr to a temp file while fn
// runs. The cobra completion protocol writes directly to the process streams,
// which execute does not expose, so this keeps that output out of the test log.
func silenceOutput(t *testing.T, fn func()) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	fn()
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

func TestCompletionCommandsAreNotUsageErrors(t *testing.T) {
	d := cliTestDeps(t)

	t.Run("completion bash renders", func(t *testing.T) {
		root := NewRootWithDeps(d)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"completion", "bash"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("completion bash: %v\n%s", err, out.String())
		}
		if out.Len() == 0 {
			t.Fatal("completion bash produced no output")
		}
	})

	t.Run("execute does not misclassify completion", func(t *testing.T) {
		var err error
		silenceOutput(t, func() {
			err = execute(context.Background(), []string{"completion", "bash"}, d)
		})
		if err != nil {
			t.Fatalf("completion bash: %v, want no error", err)
		}
	})

	t.Run("execute does not misclassify __complete", func(t *testing.T) {
		var err error
		silenceOutput(t, func() {
			err = execute(context.Background(), []string{"__complete", "snapshot", ""}, d)
		})
		if isUsageError(err) {
			t.Fatalf("__complete classified as usage error: %v", err)
		}
	})
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
