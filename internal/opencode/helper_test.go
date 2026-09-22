package opencode

import (
	"fmt"
	"os"
	"testing"
	"time"
)

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

func helperEnv() []string {
	return append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
}
