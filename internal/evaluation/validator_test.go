package evaluation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// TestValidatorHelper is the fake validator process. It is only active when
// GO_WANT_HELPER_PROCESS=1, mirroring internal/opencode's helper convention: no
// test in this package invokes the real opencode binary, so most command
// validators are exercised with the test binary itself as argv. The one
// exception is the documented `sh -c` fork case in the process-group test.
func TestValidatorHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("FAKE_VALIDATOR_MODE") {
	case "pass":
		fmt.Fprint(os.Stdout, "stdout-ok")
		fmt.Fprint(os.Stderr, "stderr-ok")
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stdout, "stdout-bad")
		fmt.Fprint(os.Stderr, "stderr-bad")
		os.Exit(1)
	case "exit7":
		os.Exit(7)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "emit":
		fmt.Fprint(os.Stdout, os.Getenv("FAKE_VALIDATOR_PAYLOAD"))
		os.Exit(0)
	case "large":
		chunk := []byte(strings.Repeat("x", 64<<10))
		for i := 0; i < 80; i++ { // 5 MiB, more than the 4 MiB capture bound
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(5)
			}
		}
		os.Exit(0)
	case "writecwd":
		if err := os.WriteFile("validator-dir-probe.txt", []byte("ok"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(2)
}

// helperArgv and helperEnv drive TestValidatorHelper as the command validator.
func helperArgv() []string { return []string{os.Args[0], "-test.run=TestValidatorHelper", "--"} }

func helperEnv(mode string) []string {
	return append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "FAKE_VALIDATOR_MODE="+mode)
}

func TestRequirementsPresentAndMissing(t *testing.T) {
	if missing := Requirements([]string{"sh"}); len(missing) != 0 {
		t.Fatalf("Requirements(sh) = %v, want none", missing)
	}
	missing := Requirements([]string{"sh", "definitely-not-a-real-binary", "sh"})
	if len(missing) != 1 || missing[0] != "definitely-not-a-real-binary" {
		t.Fatalf("Requirements = %v, want [definitely-not-a-real-binary]", missing)
	}
}

func TestRunValidatorCommandPassed(t *testing.T) {
	res := RunValidator(context.Background(), 3, ValidatorSpec{Kind: "command", Name: "unit tests", Command: helperArgv()}, t.TempDir(), helperEnv("pass"), 5*time.Second, "")
	if res.Status != "passed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.Seq != 3 || res.Kind != "command" || res.Name != "unit tests" {
		t.Fatalf("metadata = %+v", res)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "stdout-ok") || !strings.Contains(res.Output, "stderr-ok") {
		t.Fatalf("combined output = %q", res.Output)
	}
	if res.DurationMS < 0 {
		t.Fatalf("duration = %d", res.DurationMS)
	}
}

func TestRunValidatorCommandFailed(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "command", Name: "fail", Command: helperArgv()}, t.TempDir(), helperEnv("fail"), 5*time.Second, "")
	if res.Status != "failed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Output, "stdout-bad") || !strings.Contains(res.Output, "stderr-bad") {
		t.Fatalf("combined output = %q", res.Output)
	}
	if res.Excerpt == "" {
		t.Fatal("excerpt is empty")
	}
}

func TestRunValidatorNonZeroExitCode(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "command", Name: "seven", Command: helperArgv()}, t.TempDir(), helperEnv("exit7"), 5*time.Second, "")
	if res.Status != "failed" || res.ExitCode != 7 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunValidatorStartError(t *testing.T) {
	spec := ValidatorSpec{Kind: "command", Name: "missing", Command: []string{"definitely-not-a-real-binary"}}
	res := RunValidator(context.Background(), 0, spec, t.TempDir(), os.Environ(), 5*time.Second, "")
	if res.Status != "error" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Output, "definitely-not-a-real-binary") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestRunValidatorTimeout(t *testing.T) {
	spec := ValidatorSpec{Kind: "command", Name: "slow", Command: []string{"sleep", "30"}}
	start := time.Now()
	res := RunValidator(context.Background(), 0, spec, t.TempDir(), os.Environ(), 250*time.Millisecond, "")
	elapsed := time.Since(start)
	if res.Status != "timeout" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took %s, child was not reaped promptly", elapsed)
	}
}

func TestRunValidatorTimeoutKillsProcessGroup(t *testing.T) {
	// `sh -c` is used only here: it forks a `sleep` grandchild so we can prove
	// the process group, not just the direct child, is killed. No shell-free
	// argv produces a forked grandchild, and the real opencode binary is never
	// involved. The shell writes the grandchild PID before blocking.
	dir := t.TempDir()
	spec := ValidatorSpec{
		Kind:    "command",
		Name:    "slow tree",
		Command: []string{"sh", "-c", "sleep 30 & echo $! > child.pid; wait"},
	}
	start := time.Now()
	res := RunValidator(context.Background(), 0, spec, dir, os.Environ(), 250*time.Millisecond, "")
	elapsed := time.Since(start)
	if res.Status != "timeout" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	// Promptness: the direct child and its grandchild hold the output pipe, so
	// a prompt return requires the whole group to be killed.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("timeout took %s, process group was not killed promptly", elapsed)
	}
	pid := readChildPID(t, filepath.Join(dir, "child.pid"))
	// The grandchild must actually be gone, not merely disconnected from the
	// pipe. SIGKILL to the process group reaps it; killing only the direct
	// child would leave it running.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			break // ESRCH: reaped
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d still alive after timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readChildPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	return pid
}

func TestRunValidatorContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	defer cancel()
	// A 30s timeout leaves cancellation, not the deadline, as the cause.
	res := RunValidator(ctx, 0, ValidatorSpec{Kind: "command", Name: "canceled", Command: helperArgv()}, t.TempDir(), helperEnv("sleep"), 30*time.Second, "")
	if res.Status != "error" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
}

func TestRunValidatorHonorsDir(t *testing.T) {
	dir := t.TempDir()
	spec := ValidatorSpec{Kind: "command", Name: "cwd", Command: helperArgv()}
	res := RunValidator(context.Background(), 0, spec, dir, helperEnv("writecwd"), 5*time.Second, "")
	if res.Status != "passed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if _, err := os.Stat(filepath.Join(dir, "validator-dir-probe.txt")); err != nil {
		t.Fatalf("probe file not written into Dir: %v", err)
	}
}

func TestRunValidatorExcerptRuneSafe(t *testing.T) {
	const limit = 2 << 10
	// 2047 ASCII bytes then a 2-byte rune: byte 2048 splits it, so the
	// rune-safe excerpt must drop the partial rune and be 2047 bytes.
	payload := strings.Repeat("a", limit-1) + "é"
	res := runEmit(t, payload)
	if len(res.Output) != limit+1 {
		t.Fatalf("output length = %d, want %d", len(res.Output), limit+1)
	}
	if !utf8.ValidString(res.Excerpt) {
		t.Fatalf("excerpt is not valid UTF-8: %q", res.Excerpt)
	}
	if got := res.Excerpt; got != strings.Repeat("a", limit-1) {
		t.Fatalf("excerpt = %q (len %d), want %d ASCII bytes", got, len(got), limit-1)
	}

	// A 3-byte rune straddling the boundary is dropped whole.
	payload = strings.Repeat("b", limit-1) + "☃x"
	res = runEmit(t, payload)
	if got := res.Excerpt; got != strings.Repeat("b", limit-1) {
		t.Fatalf("excerpt = %q (len %d), want %d ASCII bytes", got, len(got), limit-1)
	}

	// Exactly at the limit nothing is dropped.
	payload = strings.Repeat("c", limit)
	res = runEmit(t, payload)
	if len(res.Excerpt) != limit || res.Excerpt != payload {
		t.Fatalf("excerpt length = %d, want %d", len(res.Excerpt), limit)
	}
}

func TestRunValidatorBoundsOutput(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "command", Name: "big", Command: helperArgv()}, t.TempDir(), helperEnv("large"), 10*time.Second, "")
	if res.Status != "passed" {
		t.Fatalf("status = %q", res.Status)
	}
	if len(res.Output) != 4<<20 {
		t.Fatalf("output length = %d, want %d", len(res.Output), 4<<20)
	}
	if len(res.Excerpt) != 2<<10 {
		t.Fatalf("excerpt length = %d, want %d", len(res.Excerpt), 2<<10)
	}
}

func runEmit(t *testing.T, payload string) ValidationResult {
	t.Helper()
	env := append(helperEnv("emit"), "FAKE_VALIDATOR_PAYLOAD="+payload)
	return RunValidator(context.Background(), 0, ValidatorSpec{Kind: "command", Name: "emit", Command: helperArgv()}, t.TempDir(), env, 5*time.Second, "")
}

func TestRunValidatorAnswerAll(t *testing.T) {
	spec := ValidatorSpec{Kind: "answer", Name: "root cause", Patterns: []string{"retry", "off-by-one"}, Mode: "all"}

	res := RunValidator(context.Background(), 1, spec, "", nil, 0, "The retry path had an off-by-one bug")
	if res.Status != "passed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.Seq != 1 || res.Kind != "answer" || res.Name != "root cause" {
		t.Fatalf("metadata = %+v", res)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}

	res = RunValidator(context.Background(), 1, spec, "", nil, 0, "The retry path is fine")
	if res.Status != "failed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if !strings.Contains(res.Output, "off-by-one") {
		t.Fatalf("failure output does not name the unmatched pattern: %q", res.Output)
	}
	if strings.Contains(res.Output, "retry") {
		t.Fatalf("failure output names a matched pattern: %q", res.Output)
	}
}

func TestRunValidatorAnswerAny(t *testing.T) {
	spec := ValidatorSpec{Kind: "answer", Name: "one of", Patterns: []string{"alpha", "beta"}, Mode: "any"}

	res := RunValidator(context.Background(), 0, spec, "", nil, 0, "the beta release")
	if res.Status != "passed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	res = RunValidator(context.Background(), 0, spec, "", nil, 0, "gamma only")
	if res.Status != "failed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if !strings.Contains(res.Output, "alpha") || !strings.Contains(res.Output, "beta") {
		t.Fatalf("failure output should list all unmatched patterns: %q", res.Output)
	}
}

func TestRunValidatorAnswerModeDefaultsToAll(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "answer", Name: "default", Patterns: []string{"a", "b"}}, "", nil, 0, "only a")
	if res.Status != "failed" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
}

func TestRunValidatorAnswerInvalidRegex(t *testing.T) {
	spec := ValidatorSpec{Kind: "answer", Name: "bad", Patterns: []string{"["}, Mode: "all"}
	res := RunValidator(context.Background(), 0, spec, "", nil, 0, "anything")
	if res.Status != "error" {
		t.Fatalf("status = %q, output = %q", res.Status, res.Output)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Output, "error parsing regexp") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestRunValidatorUnknownKind(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "magic", Name: "x"}, "", nil, 0, "")
	if res.Status != "error" || res.ExitCode != -1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunValidatorCommandRequiresArgv(t *testing.T) {
	res := RunValidator(context.Background(), 0, ValidatorSpec{Kind: "command", Name: "empty"}, "", nil, 0, "")
	if res.Status != "error" || res.ExitCode != -1 {
		t.Fatalf("result = %+v", res)
	}
}
