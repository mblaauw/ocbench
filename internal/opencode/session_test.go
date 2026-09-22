package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// collectEvents drains a session's event channel, blocking until it closes.
func collectEvents(s *Session) [][]byte {
	var out [][]byte
	for line := range s.Events() {
		out = append(out, line)
	}
	return out
}

func TestStartStreamsProbeEvents(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-ok")
	sess, err := a.Start(context.Background(), RunRequest{
		Dir:    t.TempDir(),
		Prompt: "fix calc.py -- and say `done`",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := collectEvents(sess)
	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := probeEventLines()
	if len(lines) != len(want) {
		t.Fatalf("got %d event lines, want %d", len(lines), len(want))
	}
	for i := range want {
		if string(lines[i]) != want[i] {
			t.Fatalf("line %d mismatch:\n got %q\nwant %q", i, lines[i], want[i])
		}
	}
	if got := sess.ID(); got != probeSessionID {
		t.Fatalf("ID = %q, want %q", got, probeSessionID)
	}
}

func TestStartNonZeroExitCapturesStderr(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-fail")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "explode"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if sess.Stderr() == "" {
		t.Fatal("stderr was not captured")
	}
}

func TestStartRequestTimeoutKillsGroup(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-slow")
	start := time.Now()
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "hang", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	code, err := sess.Wait()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if code != -1 {
		t.Fatalf("exit = %d, want -1", code)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Wait took %s, want under 3s", elapsed)
	}
	// Kill must be safe to call repeatedly, including after Wait.
	sess.Kill()
	sess.Kill()
}

func TestStartDeliversLargeLineIntact(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-big")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "big"})
	if err != nil {
		t.Fatal(err)
	}
	lines := collectEvents(sess)
	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if len(lines[0]) != 1<<20 {
		t.Fatalf("line length = %d, want %d", len(lines[0]), 1<<20)
	}
}

func TestStartWithoutSessionID(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-no-session")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "anonymous"})
	if err != nil {
		t.Fatal(err)
	}
	lines := collectEvents(sess)
	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if got := sess.ID(); got != "" {
		t.Fatalf("ID = %q, want empty", got)
	}
}

func TestStartFinalLineWithoutNewline(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-no-newline")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "no trailing newline"})
	if err != nil {
		t.Fatal(err)
	}
	lines := collectEvents(sess)
	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := probeEventLines()
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d", len(lines), len(want))
	}
	last := string(lines[len(lines)-1])
	if last != want[len(want)-1] {
		t.Fatalf("last line mismatch:\n got %q\nwant %q", last, want[len(want)-1])
	}
	if strings.HasSuffix(last, "\n") {
		t.Fatal("last line unexpectedly ends with a newline")
	}
	if got := sess.ID(); got != probeSessionID {
		t.Fatalf("ID = %q, want %q", got, probeSessionID)
	}
}

// TestKillAfterWaitIsNoop verifies the guard that stops Kill from signalling the
// process group of an already-reaped process (PID reuse hazard).
func TestKillAfterWaitIsNoop(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-ok")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "reaped"})
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(sess)
	code, err := sess.Wait()
	if err != nil || code != 0 {
		t.Fatalf("Wait = (%d, %v)", code, err)
	}
	sess.Kill()
	if done := sess.joinKill(); done != nil {
		t.Fatal("Kill after Wait signalled a reaped process group")
	}
}

func TestStartContextCancellation(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-slow")
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := a.Start(ctx, RunRequest{Prompt: "cancel me", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	// Let the child start before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()
	start := time.Now()
	code, err := sess.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if code != -1 {
		t.Fatalf("exit = %d, want -1", code)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Wait took %s after cancel, want under 3s", elapsed)
	}
}

func TestExportReturnsSessionJSON(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "")
	got, err := a.Export(context.Background(), "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fakeExportJSON {
		t.Fatalf("export = %s", got)
	}
	if !json.Valid(got) {
		t.Fatalf("export is not valid JSON: %s", got)
	}
}

func TestExportFailureSurfacesError(t *testing.T) {
	a := newTestAdapter(t, 5*time.Second, "export-fail")
	_, err := a.Export(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("expected an error")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if exitErr.Code != 4 {
		t.Fatalf("exit code = %d, want 4", exitErr.Code)
	}
}

func TestStartNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	a := newTestAdapter(t, 10*time.Second, "run-ok")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "leak check"})
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(sess)
	if _, err := sess.Wait(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWaitDoesNotBlockOnUndrainedEvents ensures a caller that stops draining
// Events cannot deadlock Wait: the tailer must drop pending lines once Wait
// begins. Kept last in the file so its leaked goroutines (before the fix) do
// not disturb the goroutine-leak test.
func TestWaitDoesNotBlockOnUndrainedEvents(t *testing.T) {
	a := newTestAdapter(t, 10*time.Second, "run-many")
	sess, err := a.Start(context.Background(), RunRequest{Prompt: "flood", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately do not drain Events(): run-many produces more lines than the
	// 256-line buffer.
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := sess.Wait()
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Wait: %v", r.err)
		}
		if r.code != 0 {
			t.Fatalf("exit = %d, want 0", r.code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait blocked because Events() was not drained")
	}
}
