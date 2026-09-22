package opencode

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// probeEvents is the verbatim JSONL stream captured from a live
// `opencode run --format json` session on 1.18.32 (9 events). Every event
// carries the same session ID.
//
//go:embed testdata/probe_events.jsonl
var probeEvents string

// probeSessionID is the session ID carried by every embedded probe event.
const probeSessionID = "ses_f35c03361ffeh5M9DMpbpy4fQW"

// fakeExportJSON is the canned `opencode export` payload served by the helper.
const fakeExportJSON = `{"info":{"id":"ses_test","model":{"providerID":"p","modelID":"m"},"tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.01},"messages":[]}`

// probeEventLines returns the embedded probe stream split into individual
// lines, without the trailing newline on the last line.
func probeEventLines() []string {
	return strings.Split(strings.TrimSuffix(probeEvents, "\n"), "\n")
}

// TestHelperProcess is the fake opencode binary. It is only active when
// GO_WANT_HELPER_PROCESS=1, which the parent test sets on the child env.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode := os.Getenv("FAKE_MODE")
	write := func(s string) {
		fmt.Fprint(os.Stdout, s)
		os.Exit(0)
	}
	switch {
	case mode == "sleep":
		time.Sleep(5 * time.Second)
	case isRunMode(mode):
		helperRun(mode, args)
	case len(args) > 0 && args[0] == "export":
		if mode == "export-fail" {
			fmt.Fprintln(os.Stderr, "export failed: simulated failure")
			os.Exit(4)
		}
		write(fakeExportJSON)
	case len(args) > 0 && args[0] == "--version":
		write("1.18.32\n")
	case len(args) > 1 && args[0] == "debug" && args[1] == "config":
		if mode == "garbage" {
			write("not json")
		}
		write(`{"default_agent":"build","model":"p/m","agent":{"build":{"model":"p/m","variant":"high","steps":60,"options":{}}}}`)
	case len(args) > 1 && args[0] == "debug" && args[1] == "skill":
		if mode == "empty" {
			write("[]")
		}
		write(`[{"name":"ruff","description":"lint","location":"/home/u/.agents/skills/ruff/SKILL.md","content":"# ruff"}]`)
	case len(args) > 2 && args[0] == "debug" && args[1] == "agent":
		write(fmt.Sprintf(`{"name":%q,"mode":"subagent","native":false,"model":"p/m","variant":"low","steps":20,"tools":{"read":true},"options":{},"permission":[]}`, args[2]))
	case len(args) > 1 && args[0] == "mcp" && args[1] == "list":
		if mode == "fail" {
			os.Exit(3)
		}
		write("\u2502\n\u25cf  \u25cb gitlab \u001b[90mdisabled\n\u2502      https://mcp.example/v2/mcp\n")
	}
	os.Exit(42)
}

// isRunMode reports whether mode selects a streaming `run` helper mode.
func isRunMode(mode string) bool {
	switch mode {
	case "run-ok", "run-fail", "run-slow", "run-big", "run-no-session":
		return true
	}
	return false
}

// helperRun implements the fake `opencode run` subcommand for the streaming
// session tests. It always terminates the helper process.
func helperRun(mode string, args []string) {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintf(os.Stderr, "helper mode %q: unexpected argv %v\n", mode, args)
		os.Exit(64)
	}
	switch mode {
	case "run-ok":
		fmt.Fprint(os.Stdout, probeEvents)
	case "run-fail":
		fmt.Fprintln(os.Stdout, `{"type":"step_start","timestamp":1,"sessionID":"ses_fail","part":{"type":"step-start"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"step_finish","timestamp":2,"sessionID":"ses_fail","part":{"type":"step-finish","reason":"error"}}`)
		fmt.Fprintln(os.Stderr, "opencode run failed: simulated failure")
		os.Exit(3)
	case "run-slow":
		fmt.Fprintln(os.Stdout, `{"type":"step_start","timestamp":1,"sessionID":"ses_slow","part":{"type":"step-start"}}`)
		time.Sleep(30 * time.Second)
	case "run-big":
		fmt.Fprint(os.Stdout, strings.Repeat("a", 1<<20), "\n")
	case "run-no-session":
		fmt.Fprintln(os.Stdout, `{"type":"text","timestamp":1,"part":{"type":"text","text":"no session here"}}`)
		fmt.Fprintln(os.Stdout, `{"type":"step_finish","timestamp":2,"part":{"type":"step-finish","reason":"stop"}}`)
	}
	os.Exit(0)
}

func helperEnv() []string {
	return append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
}
