package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/suite"
)

// cancelOnStartAdapter cancels the request context and then returns its error,
// deterministically reproducing a SIGINT that lands immediately before or at
// Session.Start.
type cancelOnStartAdapter struct {
	opencode.Adapter
	cancel context.CancelFunc
}

func (a *cancelOnStartAdapter) Start(ctx context.Context, _ opencode.RunRequest) (*opencode.Session, error) {
	a.cancel()
	return nil, ctx.Err()
}

// TestRunCancelledBeforeStartPersistsRow proves that a cancellation observed at
// Start still records exactly one run row and removes the worktree, rather than
// failing to persist because the request context is dead.
func TestRunCancelledBeforeStartPersistsRow(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	a := &cancelOnStartAdapter{Adapter: newScriptedAdapter(t), cancel: cancel}

	res, err := Run(ctx, a, f.st, runnerRequest(f))
	if err != nil {
		t.Fatalf("Run returned an infrastructure error instead of a recorded row: %v", err)
	}
	if res.Status != "error" && res.Status != "timeout" {
		t.Fatalf("status = %q, want error or timeout", res.Status)
	}
	row, err := f.st.GetRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("cancelled-before-start run was not persisted: %v", err)
	}
	if row.Status != "error" && row.Status != "timeout" {
		t.Fatalf("persisted status = %q, want error or timeout", row.Status)
	}
	if _, err := os.Stat(filepath.Join(res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree still present after cancelled start (err = %v)", err)
	}
}

// blockingExportAdapter pauses inside Export until released, so a test can
// cancel the run context after the session finished but before validators.
type blockingExportAdapter struct {
	*scriptedAdapter
	exportStarted chan struct{}
	releaseExport chan struct{}
}

func (a *blockingExportAdapter) Export(ctx context.Context, id string) ([]byte, error) {
	close(a.exportStarted)
	<-a.releaseExport
	return a.scriptedAdapter.Export(ctx, id)
}

// TestRunCancelledAfterSessionSkipsValidatorsAndPersists proves that a
// cancellation arriving after Wait, while post-session artifacts are captured,
// skips every validator and persists an error row with no validation rows.
func TestRunCancelledAfterSessionSkipsValidatorsAndPersists(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "check", Command: []string{"sh", "-c", "exit 0"}},
	}
	a := &blockingExportAdapter{
		scriptedAdapter: newScriptedAdapter(t),
		exportStarted:   make(chan struct{}),
		releaseExport:   make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Run(ctx, a, f.st, runnerRequest(f))
		done <- outcome{res, err}
	}()

	select {
	case <-a.exportStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("Export was never reached")
	}
	// Cancel after the session completed but before validators, then let the
	// artifact phase finish.
	cancel()
	close(a.releaseExport)

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		if out.res.Status != "error" {
			t.Fatalf("status = %q, want error (cancellation after session must not read as failed)", out.res.Status)
		}
		row, err := f.st.GetRun(context.Background(), out.res.RunID)
		if err != nil {
			t.Fatalf("cancelled run was not persisted: %v", err)
		}
		if row.Status != "error" {
			t.Fatalf("persisted status = %q, want error", row.Status)
		}
		if n := countValidationRows(t, f, out.res.RunID); n != 0 {
			t.Fatalf("validation rows = %d, want 0 after cancellation", n)
		}
		if _, err := os.Stat(filepath.Join(out.res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
			t.Fatalf("worktree still present after late cancellation (err = %v)", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestRunCancelledDuringValidatorPersistsError proves that a cancellation
// landing while a command validator is already running is the authoritative
// outcome: the started validator is still recorded, but the run is persisted as
// error rather than failed, and the worktree is removed.
func TestRunCancelledDuringValidatorPersistsError(t *testing.T) {
	useMode(t, "ok")
	f := setupRunner(t)
	marker := filepath.Join(t.TempDir(), "validator-started")
	f.task.Validators = []suite.Validator{
		{Kind: "command", Name: "blocking", Command: []string{"sh", "-c", "touch " + marker + " && sleep 30"}},
	}
	a := newScriptedAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Run(ctx, a, f.st, runnerRequest(f))
		done <- outcome{res, err}
	}()

	// Cancel only once the validator has actually started, so the cancellation
	// deterministically lands inside RunValidator rather than before it.
	waitForFile(t, marker, 20*time.Second)
	cancel()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		if out.res.Status != "error" {
			t.Fatalf("status = %q, want error (cancellation during a validator must not read as failed)", out.res.Status)
		}
		row, err := f.st.GetRun(context.Background(), out.res.RunID)
		if err != nil {
			t.Fatalf("cancelled run was not persisted: %v", err)
		}
		if row.Status != "error" {
			t.Fatalf("persisted status = %q, want error", row.Status)
		}
		if len(out.res.Validations) != 1 {
			t.Fatalf("validations = %d, want 1 recorded for the started validator", len(out.res.Validations))
		}
		if got := out.res.Validations[0].Status; got != "error" && got != "timeout" {
			t.Fatalf("validation status = %q, want error or timeout", got)
		}
		if n := countValidationRows(t, f, out.res.RunID); n != 1 {
			t.Fatalf("validation rows = %d, want 1", n)
		}
		if _, err := os.Stat(filepath.Join(out.res.ArtifactsDir, "worktree")); !os.IsNotExist(err) {
			t.Fatalf("worktree still present after cancellation (err = %v)", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// waitForFile polls until path exists or the timeout elapses.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within %s", path, timeout)
}

// countValidationRows returns the number of persisted validation rows for a run.
func countValidationRows(t *testing.T, f *runnerFixture, runID string) int {
	t.Helper()
	var n int
	if err := f.st.DB().QueryRow(`SELECT COUNT(*) FROM run_validations WHERE run_id = ?`, runID).Scan(&n); err != nil {
		t.Fatalf("count validations: %v", err)
	}
	return n
}
