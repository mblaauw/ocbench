package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	// tailPollInterval is how often the tailer checks the stdout file for new
	// lines while the child runs.
	tailPollInterval = 10 * time.Millisecond
	// killGracePeriod is how long Kill waits after SIGTERM before escalating to
	// SIGKILL against the process group.
	killGracePeriod = 5 * time.Second
	// eventBuffer bounds the buffered event channel so a briefly stalled
	// consumer cannot deadlock the tailer before Wait is called.
	eventBuffer = 256
)

// Session is one streaming `opencode run` invocation.
//
// The child's stdout and stderr are redirected to temp files rather than pipes:
// opencode is a Bun binary that drops a single stdout write larger than the
// 64 KiB pipe buffer when it exits before the write drains. A tailer goroutine
// polls the stdout file and emits complete JSONL lines on Events.
//
// Callers consume Events while the run proceeds and then call Wait exactly
// once (it is safe to call from several goroutines). Events is closed once the
// process has exited and the stdout file is fully drained; no tailer goroutine
// survives Wait.
type Session struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc

	tmpDir  string
	outPath string
	errPath string
	outFile *os.File
	errFile *os.File

	events   chan []byte
	procDone chan struct{}
	tailDone chan struct{}

	waitOnce sync.Once
	killOnce sync.Once

	mu     sync.Mutex
	id     string
	stderr string

	// waitResult is written by the reaper before procDone is closed and read
	// by Wait after receiving from procDone.
	waitResult error

	exitCode int
	waitErr  error
	ctxErr   error

	killMu   sync.Mutex
	killDone chan struct{}
}

// ID returns the session ID reported by the event stream, or "" until the first
// event carrying one has been parsed.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Events returns the raw JSONL stdout lines of the run. The channel is closed
// once the process has exited and the stdout file is fully drained, which
// happens before Wait returns.
func (s *Session) Events() <-chan []byte {
	return s.events
}

// Stderr returns the child's captured stderr. It is populated by Wait.
func (s *Session) Stderr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr
}

// Wait reaps the child, removes the run's temp files and returns its exit code.
// It is single-call-safe: concurrent and repeated calls return the same result.
//
// A non-zero exit is reported through exitCode with a nil error. Wait returns
// ctx.Err() (context.Canceled or context.DeadlineExceeded) when the session's
// context ended, and it kills the process group in that case.
func (s *Session) Wait() (int, error) {
	s.waitOnce.Do(s.wait)
	return s.exitCode, s.waitErr
}

func (s *Session) wait() {
	defer s.cancel()

	select {
	case <-s.procDone:
	case <-s.ctx.Done():
		s.ctxErr = s.ctx.Err()
		s.Kill()
		<-s.procDone
	}

	// The tailer drains the stdout file and closes Events before tailDone.
	<-s.tailDone
	// Join the Kill grace goroutine if it was started.
	if done := s.joinKill(); done != nil {
		<-done
	}

	if s.errFile != nil {
		// Read the path, not the *os.File: the child inherited the same open
		// file description, so its writes advanced the shared offset and a read
		// from errFile would start at EOF.
		b, _ := os.ReadFile(s.errPath)
		s.mu.Lock()
		s.stderr = string(b)
		s.mu.Unlock()
		s.errFile.Close()
	}
	if s.outFile != nil {
		s.outFile.Close()
	}
	os.Remove(s.outPath)
	os.Remove(s.errPath)
	os.RemoveAll(s.tmpDir)

	if s.ctxErr != nil {
		s.exitCode, s.waitErr = -1, s.ctxErr
		return
	}
	if s.waitResult == nil {
		return // exit 0
	}
	var exitErr *exec.ExitError
	if errors.As(s.waitResult, &exitErr) {
		s.exitCode = exitErr.ExitCode()
		return
	}
	s.exitCode, s.waitErr = -1, s.waitResult
}

// Kill terminates the child's process group with SIGTERM, escalating to SIGKILL
// after killGracePeriod. It is idempotent and safe to call after Wait.
func (s *Session) Kill() {
	s.killOnce.Do(func() {
		_ = terminateProcessGroup(s.cmd)
		done := make(chan struct{})
		s.killMu.Lock()
		s.killDone = done
		s.killMu.Unlock()
		go func() {
			defer close(done)
			select {
			case <-s.procDone:
			case <-time.After(killGracePeriod):
				_ = killProcessGroup(s.cmd)
			}
		}()
	})
}

// joinKill returns the grace goroutine's done channel if Kill has run.
func (s *Session) joinKill() chan struct{} {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	return s.killDone
}

// reap waits for the child once and records the result before signalling the
// tailer and Wait.
func (s *Session) reap() {
	s.waitResult = s.cmd.Wait()
	close(s.procDone)
}

// tail polls the stdout file, emitting complete lines on Events until the
// process has exited and the file is fully drained. Defers run LIFO, so Events
// is closed before tailDone; Wait's receive from tailDone therefore guarantees
// Events is already closed.
func (s *Session) tail() {
	defer close(s.tailDone)
	defer close(s.events)

	f, err := os.Open(s.outPath)
	if err != nil {
		return
	}
	defer f.Close()

	var buf []byte
	var offset int64
	chunk := make([]byte, 64<<10)
	ticker := time.NewTicker(tailPollInterval)
	defer ticker.Stop()

	for {
		s.readAvailable(f, &offset, chunk, &buf)
		if !s.exited() {
			<-ticker.C
			continue
		}
		// The process has exited. stdout is a regular file, so no further
		// writes can arrive; drain once more and emit any final line that lacks
		// a trailing newline.
		s.readAvailable(f, &offset, chunk, &buf)
		if len(buf) > 0 {
			s.emit(buf)
		}
		return
	}
}

func (s *Session) exited() bool {
	select {
	case <-s.procDone:
		return true
	default:
		return false
	}
}

// readAvailable reads bytes appended to f since offset, appends them to buf and
// emits every complete line. A trailing partial line remains in buf.
func (s *Session) readAvailable(f *os.File, offset *int64, chunk []byte, buf *[]byte) {
	for {
		n, err := f.ReadAt(chunk, *offset)
		if n > 0 {
			*offset += int64(n)
			*buf = append(*buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	for {
		i := bytes.IndexByte(*buf, '\n')
		if i < 0 {
			break
		}
		line := make([]byte, i)
		copy(line, (*buf)[:i])
		*buf = (*buf)[i+1:]
		s.emit(line)
	}
}

// emit records any session ID on line and publishes the raw line.
func (s *Session) emit(line []byte) {
	s.captureSessionID(line)
	s.events <- line
}

// captureSessionID extracts the first sessionID seen. Lines that are not JSON
// envelopes, or that omit the field, are ignored.
func (s *Session) captureSessionID(line []byte) {
	var envelope struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.SessionID == "" {
		return
	}
	s.mu.Lock()
	if s.id == "" {
		s.id = envelope.SessionID
	}
	s.mu.Unlock()
}
