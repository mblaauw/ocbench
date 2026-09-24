package evaluation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Output capture bounds. Output is the full captured combined stdout+stderr,
// truncated to maxOutputBytes so a runaway validator cannot exhaust memory;
// Excerpt is a rune-safe prefix used for the database and human display.
const (
	maxOutputBytes = 4 << 20 // 4 MiB
	excerptBytes   = 2 << 10 // 2 KiB
)

// ValidatorSpec describes one task validator. Kind is "command", "answer",
// "diff", "grep" or "process". Command is an argv (never a shell string);
// Patterns/Mode apply to answer validators, where Mode is "all" (default) or
// "any"; RequiredPaths/ForbiddenPaths/MaxLines apply to diff validators;
// Present/Absent apply to grep validators; ToolPattern applies to process
// validators. Weight defaults to 1 and feeds the weighted score.
type ValidatorSpec struct {
	Kind     string
	Name     string
	Command  []string
	Patterns []string
	Mode     string

	RequiredPaths  []string
	ForbiddenPaths []string
	MaxLines       int

	Present []string
	Absent  []string

	ToolPattern string

	Weight float64
}

// ValidationResult is the outcome of one validator run. Status is one of
// passed, failed, error, skipped, or timeout. ExitCode is the process exit code
// for command validators (-1 for errors and timeouts) and 0/1 for answer
// validators. Output is the full captured output (bounded to 4 MiB) for the
// runner to persist; Excerpt is its first 2 KiB, rune-safe.
type ValidationResult struct {
	Seq        int
	Kind       string
	Name       string
	Status     string
	ExitCode   int
	DurationMS int64
	Output     string
	Excerpt    string
	// Weight is copied from the validator spec and feeds the weighted score.
	// Zero means one.
	Weight float64
}

// Requirements returns the names in requires that cannot be found on PATH, in
// first-seen order and without duplicates. The runner turns non-empty results
// into skipped validators; RunValidator itself never inspects requires.
func Requirements(requires []string) (missing []string) {
	seen := make(map[string]bool, len(requires))
	for _, name := range requires {
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// RunValidator executes a single validator and returns its result. It never
// returns an error: infrastructure problems become an "error" status with the
// reason in Output. dir and env apply to command validators; finalAnswer is
// matched by answer validators. A non-positive timeout means no deadline.
func RunValidator(ctx context.Context, seq int, spec ValidatorSpec, dir string, env []string, timeout time.Duration, finalAnswer string) ValidationResult {
	return RunValidatorContext(ctx, seq, spec, ValidatorContext{Worktree: dir, FinalAnswer: finalAnswer}, env, timeout)
}

// runCommandValidator runs spec.Command as an argv with dir and env, capturing
// combined output. The child is started in its own process group so a deadline
// kills the whole tree, not just the direct child.
func runCommandValidator(ctx context.Context, seq int, spec ValidatorSpec, dir string, env []string, timeout time.Duration) ValidationResult {
	res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, ExitCode: -1, Weight: spec.Weight}
	if len(spec.Command) == 0 {
		res.Status = "error"
		res.setOutput("command validator requires a non-empty argv")
		return res
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, spec.Command[0], spec.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// CommandContext's default Cancel kills only the direct child. Killing the
	// process group reaps grandchildren that inherited the output pipe, so Wait
	// cannot block until they exit on their own. WaitDelay is a backstop.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	out := &boundedBuffer{max: maxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out

	start := time.Now()
	if err := cmd.Start(); err != nil {
		res.DurationMS = time.Since(start).Milliseconds()
		res.Status = "error"
		res.setOutput(err.Error())
		return res
	}
	err := cmd.Wait()
	res.DurationMS = time.Since(start).Milliseconds()

	if runCtx.Err() != nil {
		res.Status = "error"
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			res.Status = "timeout"
		}
		res.ExitCode = -1
		res.setOutput(out.String())
		return res
	}
	if err != nil && (cmd.ProcessState == nil || !cmd.ProcessState.Exited()) {
		res.Status = "error"
		res.ExitCode = -1
		res.setOutput(err.Error())
		return res
	}
	res.ExitCode = cmd.ProcessState.ExitCode()
	if res.ExitCode == 0 {
		res.Status = "passed"
	} else {
		res.Status = "failed"
	}
	res.setOutput(out.String())
	return res
}

// runAnswerValidator matches every pattern against finalAnswer. Invalid
// patterns yield an "error" status; otherwise "all" (the default) requires
// every pattern and "any" requires at least one.
func runAnswerValidator(seq int, spec ValidatorSpec, finalAnswer string) ValidationResult {
	res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, ExitCode: -1, Weight: spec.Weight}

	mode := spec.Mode
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "any" {
		res.Status = "error"
		res.setOutput(fmt.Sprintf("unknown answer mode %q", spec.Mode))
		return res
	}
	if len(spec.Patterns) == 0 {
		res.Status = "error"
		res.setOutput("answer validator requires at least one pattern")
		return res
	}

	var unmatched []string
	for _, pattern := range spec.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			res.Status = "error"
			res.setOutput(fmt.Sprintf("invalid pattern %q: %v", pattern, err))
			return res
		}
		if !re.MatchString(finalAnswer) {
			unmatched = append(unmatched, pattern)
		}
	}

	if len(unmatched) == 0 || (mode == "any" && len(unmatched) < len(spec.Patterns)) {
		res.Status = "passed"
		res.ExitCode = 0
		return res
	}
	res.Status = "failed"
	res.ExitCode = 1
	if mode == "all" {
		res.setOutput(fmt.Sprintf("answer did not match all patterns; unmatched: %s", strings.Join(unmatched, ", ")))
	} else { // mode == "any" and nothing matched, so every pattern is unmatched
		res.setOutput(fmt.Sprintf("answer did not match any pattern; unmatched: %s", strings.Join(unmatched, ", ")))
	}
	return res
}

// setOutput records the full output and its rune-safe excerpt.
func (r *ValidationResult) setOutput(s string) {
	r.Output = s
	r.Excerpt = excerpt(s)
}

// excerpt returns the first excerptBytes of s without splitting a multi-byte
// rune. Shorter strings are returned unchanged.
func excerpt(s string) string {
	if len(s) <= excerptBytes {
		return s
	}
	end := excerptBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// boundedBuffer captures up to max bytes and discards the rest. Write always
// reports full consumption so the child never blocks on a full pipe.
type boundedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) <= remaining {
		b.buf.Write(p)
	} else {
		b.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }
