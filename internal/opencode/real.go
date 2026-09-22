package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultBin     = "opencode"
	defaultTimeout = 60 * time.Second
	excerptLimit   = 2 << 10 // 2 KiB
)

// Real executes the configured OpenCode binary. It is safe for concurrent use;
// every call spawns its own process.
type Real struct {
	opts Options
}

var _ Adapter = (*Real)(nil)

// NewReal applies the option defaults described by the adapter contract.
func NewReal(opts Options) *Real {
	if opts.Bin == "" {
		opts.Bin = defaultBin
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	return &Real{opts: opts}
}

// Run executes the binary with args and returns captured stdout and stderr. It
// is exported for testing and doctor diagnostics.
func (r *Real) Run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	return r.run(ctx, "", args...)
}

// Start begins one streaming `opencode run` invocation. The child's stdout and
// stderr are redirected to temp files (never pipes; Bun truncates piped output
// at 64 KiB) and a tailer goroutine emits stdout JSONL lines on the returned
// session. The caller must consume Events and call Wait exactly once.
func (r *Real) Start(ctx context.Context, req RunRequest) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = r.opts.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)

	argv := append([]string{}, r.opts.TestPrefix...)
	argv = append(argv, "run", "--format", "json", "--dir", req.Dir)
	if req.Agent != "" {
		argv = append(argv, "--agent", req.Agent)
	}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.Variant != "" {
		argv = append(argv, "--variant", req.Variant)
	}
	if req.Auto {
		argv = append(argv, "--auto")
	}
	if req.Pure {
		argv = append(argv, "--pure")
	}
	// The prompt goes last, after "--", so prompts starting with "-" are safe.
	argv = append(argv, "--", req.Prompt)

	tmpDir, err := os.MkdirTemp("", "ocbench-run-*")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opencode run: create temp dir: %w", err)
	}
	outPath := filepath.Join(tmpDir, "stdout.jsonl")
	errPath := filepath.Join(tmpDir, "stderr.log")
	outFile, err := os.Create(outPath)
	if err != nil {
		cancel()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("opencode run: create stdout capture: %w", err)
	}
	errFile, err := os.Create(errPath)
	if err != nil {
		cancel()
		outFile.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("opencode run: create stderr capture: %w", err)
	}

	cmd := exec.Command(r.opts.Bin, argv...)
	cmd.Dir = req.Dir
	if req.Env != nil {
		cmd.Env = req.Env
	} else {
		cmd.Env = r.opts.Env
	}
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	configureProcessGroup(cmd)
	// WaitDelay mirrors the validator engine's process hygiene; with regular
	// files it is a backstop for inherited output descriptors.
	cmd.WaitDelay = 2 * time.Second

	s := &Session{
		cmd:      cmd,
		ctx:      ctx,
		cancel:   cancel,
		tmpDir:   tmpDir,
		outPath:  outPath,
		errPath:  errPath,
		outFile:  outFile,
		errFile:  errFile,
		events:   make(chan []byte, eventBuffer),
		procDone: make(chan struct{}),
		tailDone: make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		outFile.Close()
		errFile.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("opencode %s: %w", strings.Join(argv, " "), err)
	}
	go s.reap()
	go s.tail()
	return s, nil
}

// Export returns the raw JSON printed by `opencode export <sessionID>`.
func (r *Real) Export(ctx context.Context, sessionID string) ([]byte, error) {
	stdout, _, err := r.run(ctx, "", "export", sessionID)
	if err != nil {
		return nil, err
	}
	return stdout, nil
}

// run is Run with an optional working directory.
//
// Output is captured to temporary files rather than pipes. OpenCode is a Bun
// binary and drops a single stdout write larger than the pipe buffer (64 KiB)
// when it exits before the write drains: `opencode debug skill` produces
// ~320 KiB of JSON and arrived truncated at exactly 64 KiB through a pipe,
// while file redirection returned the whole document. Regular files make those
// writes synchronous and lossless.
func (r *Real) run(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error) {
	argv := append(append([]string{}, r.opts.TestPrefix...), args...)

	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()

	outFile, err := os.CreateTemp("", "ocbench-stdout-*")
	if err != nil {
		return nil, nil, fmt.Errorf("opencode %s: create stdout capture: %w", strings.Join(args, " "), err)
	}
	defer os.Remove(outFile.Name())
	defer outFile.Close()
	errFile, err := os.CreateTemp("", "ocbench-stderr-*")
	if err != nil {
		return nil, nil, fmt.Errorf("opencode %s: create stderr capture: %w", strings.Join(args, " "), err)
	}
	defer os.Remove(errFile.Name())
	defer errFile.Close()

	cmd := exec.CommandContext(ctx, r.opts.Bin, argv...)
	cmd.Env = r.opts.Env
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = outFile
	cmd.Stderr = errFile

	runErr := cmd.Run()
	stdout, readErr := os.ReadFile(outFile.Name())
	if readErr != nil {
		return nil, nil, fmt.Errorf("opencode %s: read stdout capture: %w", strings.Join(args, " "), readErr)
	}
	stderr, readErr = os.ReadFile(errFile.Name())
	if readErr != nil {
		return nil, nil, fmt.Errorf("opencode %s: read stderr capture: %w", strings.Join(args, " "), readErr)
	}
	if runErr == nil {
		return stdout, stderr, nil
	}
	if ctx.Err() != nil {
		return stdout, stderr, fmt.Errorf("opencode %s timed out after %s: %w", strings.Join(args, " "), r.opts.Timeout, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return stdout, stderr, &ExitError{
			Args:   args,
			Code:   exitErr.ExitCode(),
			Stderr: excerpt(stderr),
			Stdout: excerpt(stdout),
		}
	}
	return stdout, stderr, fmt.Errorf("opencode %s: %w", strings.Join(args, " "), runErr)
}

// ExitError describes a non-zero exit of the OpenCode binary.
type ExitError struct {
	Args   []string
	Code   int
	Stderr string
	Stdout string
}

func (e *ExitError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "opencode %s exited with code %d", strings.Join(e.Args, " "), e.Code)
	if e.Stderr != "" {
		fmt.Fprintf(&b, ": stderr: %s", e.Stderr)
	}
	if e.Stdout != "" {
		fmt.Fprintf(&b, ": stdout: %s", e.Stdout)
	}
	return b.String()
}

// Version runs `opencode --version`.
func (r *Real) Version(ctx context.Context) (string, error) {
	out, _, err := r.Run(ctx, "--version")
	if err != nil {
		return "", err
	}
	return parseVersion(out)
}

// ResolvedConfig runs `opencode debug config` and returns the raw JSON object.
func (r *Real) ResolvedConfig(ctx context.Context, dir string) ([]byte, error) {
	out, stderr, err := r.run(ctx, dir, "debug", "config")
	if err != nil {
		return nil, err
	}
	if !json.Valid(out) {
		return nil, invalidJSONError("debug config", nil, out, stderr)
	}
	return out, nil
}

// Skills runs `opencode debug skill`.
func (r *Real) Skills(ctx context.Context, dir string) ([]SkillInfo, error) {
	out, stderr, err := r.run(ctx, dir, "debug", "skill")
	if err != nil {
		return nil, err
	}
	var skills []SkillInfo
	if err := json.Unmarshal(out, &skills); err != nil {
		return nil, invalidJSONError("debug skill", err, out, stderr)
	}
	return skills, nil
}

// Agent runs `opencode debug agent <name>`.
func (r *Real) Agent(ctx context.Context, dir, name string) (AgentInfo, error) {
	out, stderr, err := r.run(ctx, dir, "debug", "agent", name)
	if err != nil {
		return AgentInfo{}, err
	}
	var info AgentInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return AgentInfo{}, invalidJSONError("debug agent "+name, err, out, stderr)
	}
	info.Raw = append(json.RawMessage(nil), out...)
	return info, nil
}

// MCPStatus runs `opencode mcp list`. Doctor is the only caller.
func (r *Real) MCPStatus(ctx context.Context, dir string) ([]MCPStatus, error) {
	out, _, err := r.run(ctx, dir, "mcp", "list")
	if err != nil {
		return nil, err
	}
	return parseMCPList(out)
}

func invalidJSONError(what string, decodeErr error, stdout, stderr []byte) error {
	msg := "invalid JSON from opencode " + what
	if decodeErr != nil {
		msg += ": " + decodeErr.Error()
	}
	if s := excerpt(stdout); s != "" {
		msg += fmt.Sprintf(" (stdout: %s)", s)
	}
	if s := excerpt(stderr); s != "" {
		msg += fmt.Sprintf(" (stderr: %s)", s)
	}
	return errors.New(msg)
}

// excerpt returns at most 2 KiB of b, trimmed of surrounding whitespace.
func excerpt(b []byte) string {
	if len(b) > excerptLimit {
		b = b[:excerptLimit]
	}
	return strings.TrimSpace(string(b))
}
