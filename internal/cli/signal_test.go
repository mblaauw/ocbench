package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalHelperGuard marks the subprocess that exercises signalContext so it
// never self-signals the main test process.
const signalHelperGuard = "OCBENCH_SIGNAL_HELPER"

// TestRunCommandCancellationPersistsAndReapsChild is the Ctrl-C regression: a
// live `run` is cancelled through its command context (exactly what the
// entrypoint signal bridge does on SIGINT/SIGTERM). It proves the four
// behaviours the live defect violated: the command returns promptly with a
// cancellation error, the interrupted run is still persisted as error/timeout,
// its disposable worktree is removed, and the `opencode run` child is reaped
// rather than orphaned.
func TestRunCommandCancellationPersistsAndReapsChild(t *testing.T) {
	d, _ := newRunTestDeps(t)
	startedDir := t.TempDir()
	t.Setenv(runHelperMode, "slow")
	t.Setenv(runHelperStartedDir, startedDir)
	suiteDir := writeRunSuite(t, runTaskSpec{id: "t1"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newRunCmd(d)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"mini", "--suite-dir", suiteDir})

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	// Cancel only once the helper session is actually live, so the test
	// exercises an in-flight child rather than a start failure.
	pid := waitForHelperStart(t, startedDir, 20*time.Second)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want context.Canceled\n%s", err, buf.String())
		}
	case <-time.After(10 * time.Second):
		// Do not read buf here: the command goroutine may still be writing.
		t.Fatal("run did not return promptly after cancellation")
	}

	// The direct child was reaped by Wait; a stale PID means it was orphaned.
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper child %d still alive (kill(0) err = %v)", pid, err)
	}

	st := openRunStore(t, d)
	runs, err := st.ListRuns(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 persisted row for the cancelled run", len(runs))
	}
	if runs[0].Status != "error" && runs[0].Status != "timeout" {
		t.Fatalf("run status = %q, want error or timeout", runs[0].Status)
	}
	if _, err := os.Stat(filepath.Join(runs[0].ArtifactsDir, "worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree still present under %s (err = %v)", runs[0].ArtifactsDir, err)
	}
}

// waitForHelperStart polls for the slow helper's started marker and returns the
// PID it recorded.
func waitForHelperStart(t *testing.T, dir string, timeout time.Duration) int {
	t.Helper()
	path := filepath.Join(dir, "started")
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(path); err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
			if convErr != nil || pid <= 0 {
				t.Fatalf("invalid helper pid %q: %v", b, convErr)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper session did not start within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSignalContextCancelsOnSIGTERM proves the entrypoint bridge turns SIGTERM
// into context cancellation. It runs signalContext in a subprocess and signals
// only that process, never the test process.
func TestSignalContextCancelsOnSIGTERM(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestSignalContextHelperProcess")
	cmd.Env = append(os.Environ(), signalHelperGuard+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signal helper failed: %v\n%s", err, out)
	}
}

// TestSignalContextHelperProcess is the subprocess body for the signal test. It
// is inert unless the guard is set.
func TestSignalContextHelperProcess(t *testing.T) {
	if os.Getenv(signalHelperGuard) != "1" {
		return
	}
	ctx, stop := signalContext()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		os.Exit(2)
	}
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			os.Exit(4)
		}
		os.Exit(0)
	case <-time.After(5 * time.Second):
		os.Exit(3)
	}
}
