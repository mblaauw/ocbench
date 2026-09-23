package runner

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// envMap indexes KEY=VALUE entries by name. Tests build base from literals;
// none reads the real process environment.
func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("env entry %q has no '='", entry)
		}
		if _, dup := m[name]; dup {
			t.Fatalf("duplicate key %q in env %v", name, env)
		}
		m[name] = value
	}
	return m
}

func TestBuildEnvDefaultAllowlistDropsSecrets(t *testing.T) {
	base := []string{
		"HOME=/home/tester",
		"PATH=/usr/bin:/bin",
		"LC_ALL=en_US.UTF-8",
		"KUBECONFIG=/home/tester/.kube/config",
		"AWS_ACCESS_KEY_ID=AKIAEXAMPLE",
		"GITLAB_TOKEN=glpat-example",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
	}

	got := BuildEnv(base, EnvPolicy{})
	m := envMap(t, got)

	for _, name := range []string{"HOME", "PATH", "LC_ALL"} {
		if _, ok := m[name]; !ok {
			t.Errorf("allowlisted %s missing from %v", name, got)
		}
	}
	for _, name := range []string{"KUBECONFIG", "AWS_ACCESS_KEY_ID", "GITLAB_TOKEN", "SSH_AUTH_SOCK"} {
		if _, ok := m[name]; ok {
			t.Errorf("secret %s leaked into %v", name, got)
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("BuildEnv output not sorted: %v", got)
	}
}

func TestBuildEnvPassEnvForwardsExactlyNamedVars(t *testing.T) {
	base := []string{
		"HOME=/home/tester",
		"PASS_ME=yes",
		"ALSO_PASS=ok",
		"OTHER=no",
	}

	got := BuildEnv(base, EnvPolicy{PassEnv: []string{"PASS_ME", "ALSO_PASS"}})
	m := envMap(t, got)

	if m["PASS_ME"] != "yes" {
		t.Errorf("PASS_ME = %q, want yes (env=%v)", m["PASS_ME"], got)
	}
	if m["ALSO_PASS"] != "ok" {
		t.Errorf("ALSO_PASS = %q, want ok (env=%v)", m["ALSO_PASS"], got)
	}
	if _, ok := m["OTHER"]; ok {
		t.Errorf("unlisted OTHER leaked into %v", got)
	}

	want := map[string]bool{
		"HOME": true, "PASS_ME": true, "ALSO_PASS": true,
		"GIT_TERMINAL_PROMPT": true, "GIT_PAGER": true, "PAGER": true,
		"NO_COLOR": true, "OCBENCH": true,
	}
	for name := range m {
		if !want[name] {
			t.Errorf("unexpected key %q in %v", name, got)
		}
	}
	if len(m) != len(want) {
		t.Errorf("env has %d keys, want %d: %v", len(m), len(want), got)
	}
}

func TestBuildEnvAlwaysSetsNonInteractiveOverrides(t *testing.T) {
	base := []string{"HOME=/home/tester"}

	for _, mode := range []struct {
		name   string
		policy EnvPolicy
	}{
		{"allowlist", EnvPolicy{}},
		{"inherit", EnvPolicy{Inherit: true}},
	} {
		got := BuildEnv(base, mode.policy)
		m := envMap(t, got)
		for name, want := range map[string]string{
			"GIT_TERMINAL_PROMPT": "0",
			"GIT_PAGER":           "cat",
			"PAGER":               "cat",
			"NO_COLOR":            "1",
			"OCBENCH":             "1",
		} {
			if m[name] != want {
				t.Errorf("%s mode: %s = %q, want %q (env=%v)", mode.name, name, m[name], want, got)
			}
		}
	}
}

func TestBuildEnvInheritKeepsEverything(t *testing.T) {
	base := []string{
		"HOME=/home/tester",
		"AWS_ACCESS_KEY_ID=AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY=secret",
		"KUBECONFIG=/home/tester/.kube/config",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
	}

	got := BuildEnv(base, EnvPolicy{Inherit: true})
	m := envMap(t, got)

	for name, want := range map[string]string{
		"HOME":                  "/home/tester",
		"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "secret",
		"KUBECONFIG":            "/home/tester/.kube/config",
		"SSH_AUTH_SOCK":         "/tmp/ssh-agent.sock",
	} {
		if m[name] != want {
			t.Errorf("inherit mode: %s = %q, want %q (env=%v)", name, m[name], want, got)
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("BuildEnv output not sorted: %v", got)
	}
}

func TestBuildEnvLastWriteWins(t *testing.T) {
	base := []string{
		"HOME=/first",
		"PATH=/first/bin",
		"HOME=/second",
		"GIT_TERMINAL_PROMPT=1",
	}

	got := BuildEnv(base, EnvPolicy{})
	m := envMap(t, got)

	if m["HOME"] != "/second" {
		t.Errorf("HOME = %q, want /second (last write wins)", m["HOME"])
	}
	if m["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0 (always-set overrides base)", m["GIT_TERMINAL_PROMPT"])
	}
	if len(got) != len(m) {
		t.Errorf("duplicate keys not collapsed: %v", got)
	}

	inherited := BuildEnv(base, EnvPolicy{Inherit: true})
	im := envMap(t, inherited)
	if im["HOME"] != "/second" {
		t.Errorf("inherit HOME = %q, want /second (last write wins)", im["HOME"])
	}
}

func TestBuildEnvSafeOnEmptyBaseAndNilPassEnv(t *testing.T) {
	for _, base := range [][]string{nil, {}} {
		got := BuildEnv(base, EnvPolicy{PassEnv: nil})
		m := envMap(t, got)
		if len(m) != 5 {
			t.Errorf("empty base produced %d keys, want 5 always-set: %v", len(m), got)
		}
		if !sort.StringsAreSorted(got) {
			t.Errorf("BuildEnv output not sorted: %v", got)
		}
	}
}

func TestApplyExtraEnvOverridesSameKey(t *testing.T) {
	env := []string{"HOME=/home/tester", "OPENCODE_CONFIG=/old/config.json"}

	got := ApplyExtraEnv(env, []string{"OPENCODE_CONFIG=/new/config.json"})
	m := envMap(t, got)

	if m["OPENCODE_CONFIG"] != "/new/config.json" {
		t.Errorf("OPENCODE_CONFIG = %q, want /new/config.json (env=%v)", m["OPENCODE_CONFIG"], got)
	}
	if m["HOME"] != "/home/tester" {
		t.Errorf("HOME = %q, want /home/tester (env=%v)", m["HOME"], got)
	}
	if len(m) != len(env) {
		t.Errorf("key count = %d, want %d: %v", len(m), len(env), got)
	}
}

func TestApplyExtraEnvAppendsNewKeys(t *testing.T) {
	env := []string{"HOME=/home/tester"}

	got := ApplyExtraEnv(env, []string{"OPENCODE_CONFIG_DIR=/cfg"})
	m := envMap(t, got)

	if m["HOME"] != "/home/tester" {
		t.Errorf("HOME = %q, want /home/tester (env=%v)", m["HOME"], got)
	}
	if m["OPENCODE_CONFIG_DIR"] != "/cfg" {
		t.Errorf("OPENCODE_CONFIG_DIR = %q, want /cfg (env=%v)", m["OPENCODE_CONFIG_DIR"], got)
	}
}

func TestApplyExtraEnvKeepsOutputSorted(t *testing.T) {
	got := ApplyExtraEnv([]string{"Z=1", "M=2"}, []string{"A=3"})

	if !sort.StringsAreSorted(got) {
		t.Errorf("ApplyExtraEnv output not sorted: %v", got)
	}
}

func TestApplyExtraEnvNilReturnsInputUnchanged(t *testing.T) {
	env := []string{"B=2", "A=1"}

	got := ApplyExtraEnv(env, nil)
	if !reflect.DeepEqual(got, env) {
		t.Errorf("ApplyExtraEnv(env, nil) = %v, want %v", got, env)
	}
}

func TestEnvNamesSortedAndValueFree(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/tester",
		"LC_ALL=en_US.UTF-8",
		"EMPTY=",
		"WEIRD",
		"AWS_ACCESS_KEY_ID=AKIAEXAMPLE",
	}
	env := BuildEnv(base, EnvPolicy{})

	names := EnvNames(env)
	want := []string{"GIT_PAGER", "GIT_TERMINAL_PROMPT", "HOME", "LC_ALL", "NO_COLOR", "OCBENCH", "PAGER", "PATH"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("EnvNames = %v, want %v", names, want)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("EnvNames not sorted: %v", names)
	}
	for _, name := range names {
		if strings.Contains(name, "=") {
			t.Errorf("EnvNames entry %q contains '='", name)
		}
	}
	for _, secret := range []string{"AKIAEXAMPLE", "/usr/bin", "en_US.UTF-8"} {
		for _, name := range names {
			if strings.Contains(name, secret) {
				t.Errorf("EnvNames leaked value %q in %q", secret, name)
			}
		}
	}
}

func TestEnvNamesEmptyAndKeysOnly(t *testing.T) {
	if got := EnvNames(nil); len(got) != 0 {
		t.Errorf("EnvNames(nil) = %v, want empty", got)
	}

	names := EnvNames([]string{"B=2", "A=1", "B=3", "NOEQUALS"})
	want := []string{"A", "B", "NOEQUALS"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("EnvNames = %v, want %v", names, want)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("EnvNames not sorted: %v", names)
	}
}
