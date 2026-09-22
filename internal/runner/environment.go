package runner

import (
	"sort"
	"strings"
)

// EnvPolicy controls which inherited variables reach a child process.
type EnvPolicy struct {
	Inherit bool     // true only for --inherit-environment
	PassEnv []string // additional names forwarded in allowlist mode
}

// envAllowlist is always kept in allowlist mode. Any LC_* variable is kept too.
var envAllowlist = []string{
	"HOME", "PATH", "USER", "LOGNAME", "SHELL", "TMPDIR", "TEMP", "TMP",
	"LANG", "TERM", "TZ",
}

// envOverrides are always set in both modes, overriding any inherited value.
var envOverrides = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_PAGER=cat",
	"PAGER=cat",
	"NO_COLOR=1",
	"OCBENCH=1",
}

// BuildEnv merges base with the policy into the sorted child environment.
// Allowlist mode keeps only the allowlist, LC_*, and policy.PassEnv names;
// inherit mode keeps everything. In both modes the always-set overrides win.
// Duplicate keys collapse with last-write-wins. The result is sorted for
// deterministic tests and profiling.
func BuildEnv(base []string, policy EnvPolicy) []string {
	allow := make(map[string]bool, len(envAllowlist)+len(policy.PassEnv))
	for _, name := range envAllowlist {
		allow[name] = true
	}
	for _, name := range policy.PassEnv {
		allow[name] = true
	}

	merged := make(map[string]string, len(base)+len(envOverrides))
	for _, entry := range base {
		if entry == "" {
			continue
		}
		name, _, _ := strings.Cut(entry, "=")
		if !policy.Inherit && !allow[name] && !strings.HasPrefix(name, "LC_") {
			continue
		}
		merged[name] = entry // last write wins
	}
	for _, entry := range envOverrides {
		name, _, _ := strings.Cut(entry, "=")
		merged[name] = entry // always win, in both modes
	}

	out := make([]string, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

// EnvNames returns the sorted variable names of env, never their values. It
// feeds the profile's environment.env_names, so values must not leak.
func EnvNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		if entry == "" {
			continue
		}
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	sort.Strings(names)
	return dedupeSorted(names)
}
