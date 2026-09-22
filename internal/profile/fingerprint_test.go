package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mbl/ocbench/internal/canon"
	"mbl/ocbench/internal/opencode"
)

// loadSkills reads the synthetic `opencode debug skill` capture. The ruff skill
// is re-pointed at the on-disk testdata skill directory so that files_sha256
// actually walks a references/ file.
func loadSkills(t *testing.T) []opencode.SkillInfo {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	var skills []opencode.SkillInfo
	if err := json.Unmarshal(raw, &skills); err != nil {
		t.Fatal(err)
	}
	for i := range skills {
		if skills[i].Name == "ruff" {
			skills[i].Location = filepath.Join("testdata", "skills", "ruff", "SKILL.md")
		}
	}
	return skills
}

// loadAgents reads the synthetic per-agent captures.
func loadAgents(t *testing.T) []opencode.AgentInfo {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "agents", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var agents []opencode.AgentInfo
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var a opencode.AgentInfo
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		a.Raw = raw
		agents = append(agents, a)
	}
	return agents
}

func loadTestSources(t *testing.T) *Sources {
	t.Helper()
	resolved, err := os.ReadFile(filepath.Join("testdata", "resolved_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Sources{
		OpenCodeVersion: "1.18.32",
		ResolvedConfig:  resolved,
		Skills:          loadSkills(t),
		Agents:          loadAgents(t),
		Instructions: map[string][]byte{
			"global:AGENTS.md": []byte("# global instructions\n"),
		},
		InstructionPaths: map[string]string{
			"global:AGENTS.md":  "/home/u/.config/opencode/AGENTS.md",
			"project:AGENTS.md": "/home/u/project/AGENTS.md",
		},
		Dir:  "/home/u/project",
		Home: "/home/u",
	}
}

func componentByKey(t *testing.T, p *Profile, key string) Component {
	t.Helper()
	for _, c := range p.Components {
		if c.Kind+"/"+c.Name == key || c.Kind == key && c.Name == c.Kind {
			return c
		}
	}
	t.Fatalf("component %q not found in %v", key, componentKeys(p))
	return Component{}
}

func componentKeys(p *Profile) []string {
	var keys []string
	for _, c := range p.Components {
		if c.Name == c.Kind {
			keys = append(keys, c.Kind)
		} else {
			keys = append(keys, c.Kind+"/"+c.Name)
		}
	}
	return keys
}

func TestFingerprintDeterministic(t *testing.T) {
	first := loadTestSources(t)
	second := loadTestSources(t)

	// Deliberately shuffle slice order between the two builds. Map order is
	// randomised by Go itself; re-create the instructions map with a different
	// insertion order to exercise it too.
	reverseSkills(second.Skills)
	reverseAgents(second.Agents)
	second.Instructions = map[string][]byte{}
	second.Instructions["project:AGENTS.md"] = []byte("project\n")
	second.Instructions["global:AGENTS.md"] = []byte("# global instructions\n")
	first.Instructions["project:AGENTS.md"] = []byte("project\n")

	p1, err := Fingerprint(first, Options{Dir: first.Dir, Auto: true, EnvNames: []string{"PATH", "HOME"}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Fingerprint(second, Options{Dir: second.Dir, Auto: true, EnvNames: []string{"PATH", "HOME"}})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash != p2.Hash {
		t.Fatalf("hash not deterministic:\n %s\n %s\n%s\n%s", p1.Hash, p2.Hash, p1.CanonicalJSON, p2.CanonicalJSON)
	}
	// Captures are deterministic too (object keys sorted by canon.JSON).
	if string(p1.Captures.Instructions) != string(p2.Captures.Instructions) {
		t.Fatalf("instructions capture not deterministic:\n %s\n %s", p1.Captures.Instructions, p2.Captures.Instructions)
	}
	if string(p1.Captures.Skills) != string(p2.Captures.Skills) {
		t.Fatalf("skills capture not deterministic:\n %s\n %s", p1.Captures.Skills, p2.Captures.Skills)
	}
	// Fingerprinting the same Sources repeatedly must also be stable.
	for i := 0; i < 3; i++ {
		again, err := Fingerprint(first, Options{Dir: first.Dir, Auto: true, EnvNames: []string{"PATH", "HOME"}})
		if err != nil {
			t.Fatal(err)
		}
		if again.Hash != p1.Hash {
			t.Fatalf("repeat %d hash = %s, want %s", i, again.Hash, p1.Hash)
		}
	}
}

func reverseSkills(s []opencode.SkillInfo) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func reverseAgents(a []opencode.AgentInfo) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

func TestFingerprintSkillChangeIsIsolated(t *testing.T) {
	s := loadTestSources(t)
	opts := Options{Dir: s.Dir}
	before, err := Fingerprint(s, opts)
	if err != nil {
		t.Fatal(err)
	}

	changed := loadTestSources(t)
	for i := range changed.Skills {
		if changed.Skills[i].Name == "ruff" {
			changed.Skills[i].Content += "\nnew instruction\n"
		}
	}
	after, err := Fingerprint(changed, opts)
	if err != nil {
		t.Fatal(err)
	}

	if before.Hash == after.Hash {
		t.Fatal("overall hash did not change after a skill content change")
	}
	if componentByKey(t, before, "skill/ruff").Hash == componentByKey(t, after, "skill/ruff").Hash {
		t.Fatal("skill/ruff hash did not change")
	}
	if componentByKey(t, before, "agent/build").Hash != componentByKey(t, after, "agent/build").Hash {
		t.Fatal("agent/build hash changed when only a skill changed")
	}
}

func TestFingerprintPermissionOrderIndependent(t *testing.T) {
	// OpenCode's `debug agent` returns the permission array in a different
	// order between identical invocations; the profile hash must not depend on
	// it (spec 5.2).
	const permA = `[
		{"permission":"bash","pattern":"*","action":"ask"},
		{"permission":"edit","pattern":"*","action":"allow"},
		{"permission":"webfetch","pattern":"*","action":"deny"}
	]`
	const permB = `[
		{"permission":"webfetch","pattern":"*","action":"deny"},
		{"permission":"bash","pattern":"*","action":"ask"},
		{"permission":"edit","pattern":"*","action":"allow"}
	]`
	build := func(perm string) *Sources {
		s := loadTestSources(t)
		for i := range s.Agents {
			if s.Agents[i].Name == "build" {
				s.Agents[i].Permission = json.RawMessage(perm)
			}
		}
		return s
	}
	p1, err := Fingerprint(build(permA), Options{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Fingerprint(build(permB), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash != p2.Hash {
		t.Fatalf("profile hash depends on permission order:\n %s\n %s", p1.Hash, p2.Hash)
	}
	if componentByKey(t, p1, "permissions").Hash != componentByKey(t, p2, "permissions").Hash {
		t.Fatal("permissions component hash depends on permission order")
	}

	// The canonicalised component still contains exactly the same rule set.
	var comp struct {
		ByAgent map[string][]struct {
			Permission string `json:"permission"`
			Action     string `json:"action"`
		} `json:"by_agent"`
	}
	if err := json.Unmarshal(componentByKey(t, p1, "permissions").CanonicalJSON, &comp); err != nil {
		t.Fatal(err)
	}
	rules := comp.ByAgent["build"]
	if len(rules) != 3 {
		t.Fatalf("build permission rules = %d, want 3", len(rules))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		seen[r.Permission+"/"+r.Action] = true
	}
	for _, want := range []string{"bash/ask", "edit/allow", "webfetch/deny"} {
		if !seen[want] {
			t.Fatalf("missing permission rule %q in %+v", want, rules)
		}
	}
}

func TestFingerprintSandboxModeChangesEnvironment(t *testing.T) {
	s := loadTestSources(t)
	def, err := Fingerprint(s, Options{Dir: s.Dir})
	if err != nil {
		t.Fatal(err)
	}
	inherit, err := Fingerprint(s, Options{Dir: s.Dir, SandboxMode: "inherit"})
	if err != nil {
		t.Fatal(err)
	}

	if componentByKey(t, def, "environment").Hash == componentByKey(t, inherit, "environment").Hash {
		t.Fatal("environment component hash did not change with SandboxMode")
	}
	if def.Hash == inherit.Hash {
		t.Fatal("overall hash did not change with SandboxMode")
	}

	var envDef, envInherit map[string]any
	if err := json.Unmarshal(componentByKey(t, def, "environment").CanonicalJSON, &envDef); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(componentByKey(t, inherit, "environment").CanonicalJSON, &envInherit); err != nil {
		t.Fatal(err)
	}
	if envDef["sandbox"] != "default" {
		t.Fatalf("default sandbox = %v, want default", envDef["sandbox"])
	}
	if envInherit["sandbox"] != "inherit" {
		t.Fatalf("inherit sandbox = %v, want inherit", envInherit["sandbox"])
	}
}

func TestFingerprintNestedPermissionOrderIndependent(t *testing.T) {
	// Permission values may be objects whose nested rule arrays are returned in
	// nondeterministic order. Canonicalisation must sort arrays at any depth,
	// not just a top-level rule list (spec 5.2).
	const (
		globalA = `{"bash":[{"permission":"bash","pattern":"*.go","action":"allow"},{"permission":"bash","pattern":"*.md","action":"deny"}],"edit":"ask"}`
		globalB = `{"bash":[{"permission":"bash","pattern":"*.md","action":"deny"},{"permission":"bash","pattern":"*.go","action":"allow"}],"edit":"ask"}`
		agentA  = `[{"permission":"edit","pattern":"*.go","action":"allow","nested":[{"b":2},{"a":1}]},{"permission":"edit","pattern":"*.md","action":"deny","nested":[{"d":4},{"c":3}]}]`
		agentB  = `[{"permission":"edit","pattern":"*.md","action":"deny","nested":[{"c":3},{"d":4}]},{"permission":"edit","pattern":"*.go","action":"allow","nested":[{"a":1},{"b":2}]}]`
	)
	build := func(global, agent string) *Sources {
		return &Sources{
			OpenCodeVersion: "1.18.32",
			ResolvedConfig:  []byte(`{"permission":` + global + `,"agent":{"build":{}}}`),
			Agents: []opencode.AgentInfo{{
				Name:       "build",
				Permission: json.RawMessage(agent),
			}},
			Home: "/home/u",
		}
	}
	p1, err := Fingerprint(build(globalA, agentA), Options{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Fingerprint(build(globalB, agentB), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash != p2.Hash {
		t.Fatalf("profile hash depends on nested permission order:\n %s\n %s", p1.Hash, p2.Hash)
	}
	if componentByKey(t, p1, "permissions").Hash != componentByKey(t, p2, "permissions").Hash {
		t.Fatal("permissions component hash depends on nested permission order")
	}
}

func TestFingerprintMCPEnvironmentValuesNotCaptured(t *testing.T) {
	build := func(dsn string) *Sources {
		return &Sources{
			OpenCodeVersion: "1.18.32",
			ResolvedConfig: []byte(`{"mcp":{"db":{"type":"local","environment":{` +
				`"DB_DSN":"` + dsn + `","DB_HOST":"localhost"}}}}`),
			Home: "/home/u",
		}
	}
	const secretValue = "postgres://user:pw@host/db"
	before, err := Fingerprint(build(secretValue), Options{})
	if err != nil {
		t.Fatal(err)
	}
	capture := string(before.Captures.ResolvedConfig)
	if !strings.Contains(capture, "DB_DSN") {
		t.Fatalf("resolved-config capture missing environment key name: %s", capture)
	}
	if strings.Contains(capture, secretValue) {
		t.Fatalf("resolved-config capture leaked an MCP environment value: %s", capture)
	}

	// Rotating a non-sensitive MCP environment value must not change the hash.
	after, err := Fingerprint(build("postgres://other:secret@host/otherdb"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash != after.Hash {
		t.Fatalf("profile hash changed when only an MCP environment value rotated:\n %s\n %s", before.Hash, after.Hash)
	}
}

func TestFingerprintAgentsCapturePermissionOrderStable(t *testing.T) {
	const (
		permA = `[{"permission":"bash","pattern":"*","action":"ask"},{"permission":"edit","pattern":"*","action":"allow"}]`
		permB = `[{"permission":"edit","pattern":"*","action":"allow"},{"permission":"bash","pattern":"*","action":"ask"}]`
	)
	build := func(perm string) *Sources {
		s := loadTestSources(t)
		for i := range s.Agents {
			if s.Agents[i].Name == "build" {
				s.Agents[i].Raw = nil
				s.Agents[i].Permission = json.RawMessage(perm)
			}
		}
		return s
	}
	p1, err := Fingerprint(build(permA), Options{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Fingerprint(build(permB), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(p1.Captures.Agents) != string(p2.Captures.Agents) {
		t.Fatalf("agents.json depends on permission order:\n%s\n%s", p1.Captures.Agents, p2.Captures.Agents)
	}
}

func TestFingerprintSkillFilesChange(t *testing.T) {
	dir := t.TempDir()
	if err := copyDir(filepath.Join("testdata", "skills", "ruff"), dir); err != nil {
		t.Fatal(err)
	}
	loc := filepath.Join(dir, "SKILL.md")
	s := loadTestSources(t)
	for i := range s.Skills {
		if s.Skills[i].Name == "ruff" {
			s.Skills[i].Location = loc
		}
	}
	before, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "notes.md"), []byte("changed reference\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if componentByKey(t, before, "skill/ruff").Hash == componentByKey(t, after, "skill/ruff").Hash {
		t.Fatal("skill/ruff hash did not change after a references/ file changed")
	}
}

func TestFingerprintRedactsSecrets(t *testing.T) {
	s := loadTestSources(t)
	p, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"glpat-EXAMPLE-not-real", "Bearer gitlab-pat-EXAMPLE-not-real"} {
		if strings.Contains(string(p.CanonicalJSON), secret) {
			t.Fatalf("secret %q leaked into canonical JSON", secret)
		}
	}
	// The environment map is replaced by the sorted key list.
	mcp := componentByKey(t, p, "mcp/gitlab")
	if !strings.Contains(string(mcp.CanonicalJSON), "GITLAB_TOKEN") {
		t.Fatalf("mcp/gitlab environment_keys missing: %s", mcp.CanonicalJSON)
	}
	if !strings.Contains(string(mcp.CanonicalJSON), "environment_keys") {
		t.Fatalf("mcp/gitlab canonical = %s", mcp.CanonicalJSON)
	}
}

func TestFingerprintNormalizesHomePaths(t *testing.T) {
	s := loadTestSources(t)
	p, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	j := string(p.CanonicalJSON)
	if !strings.Contains(j, "~/.agents/skills/docs") {
		t.Fatalf("home path not normalised to ~: %s", j)
	}
	if strings.Contains(j, "/home/u") {
		t.Fatalf("absolute home path leaked: %s", j)
	}
}

func TestFingerprintInstructionPath(t *testing.T) {
	s := loadTestSources(t)
	s.Instructions["project:AGENTS.md"] = []byte("project\n")
	p, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var global, project map[string]any
	if err := json.Unmarshal(componentByKey(t, p, "instructions/global:AGENTS.md").CanonicalJSON, &global); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(componentByKey(t, p, "instructions/project:AGENTS.md").CanonicalJSON, &project); err != nil {
		t.Fatal(err)
	}
	if global["path"] != "~/.config/opencode/AGENTS.md" {
		t.Fatalf("global path = %v", global["path"])
	}
	if project["path"] != "~/project/AGENTS.md" {
		t.Fatalf("project path = %v", project["path"])
	}
	for _, c := range []map[string]any{global, project} {
		if sha, _ := c["sha256"].(string); sha == "" {
			t.Fatalf("missing sha256 in %v", c)
		}
	}
}

func TestFingerprintComponentKeys(t *testing.T) {
	s := loadTestSources(t)
	s.Instructions["project:AGENTS.md"] = []byte("project\n")
	p, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent/build", "agent/plan", "config", "environment",
		"instructions/global:AGENTS.md", "instructions/project:AGENTS.md",
		"mcp/gitlab", "mcp/local-tool", "permissions",
		"plugin/file://~/.config/opencode/plugins/hello.js",
		"plugin/superpowers@git+https://github.com/obra/superpowers.git",
		"primary", "skill/docs", "skill/ruff",
	}
	got := componentKeys(p)
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("component keys:\n got %v\nwant %v", got, want)
	}
	// Components are returned sorted by key.
	if !sort.SliceIsSorted(p.Components, func(i, j int) bool {
		a, b := p.Components[i], p.Components[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	}) {
		t.Fatalf("components not sorted: %v", componentKeys(p))
	}
	// The config catch-all must not repeat componentised keys.
	var cfg map[string]any
	if err := json.Unmarshal(componentByKey(t, p, "config").CanonicalJSON, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, consumed := range []string{"model", "small_model", "default_agent", "agent", "mcp", "plugin", "plugin_origins", "skills", "permission", "username", "$schema"} {
		if _, ok := cfg[consumed]; ok {
			t.Fatalf("config component still contains consumed key %q", consumed)
		}
	}
	if _, ok := cfg["provider"]; !ok {
		t.Fatalf("config component dropped unknown key provider: %v", cfg)
	}
}

func TestFingerprintLocalPluginHash(t *testing.T) {
	dir := t.TempDir()
	pluginFile := filepath.Join(dir, "hello.js")
	body := []byte("export const hello = () => 'hi';\n")
	if err := os.WriteFile(pluginFile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"plugin": []string{"file://" + pluginFile}})
	s := &Sources{OpenCodeVersion: "1.18.32", ResolvedConfig: cfg, Home: dir}
	p, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var comp map[string]any
	if err := json.Unmarshal(componentByKey(t, p, "plugin/file://~/hello.js").CanonicalJSON, &comp); err != nil {
		t.Fatal(err)
	}
	if comp["local_sha256"] != canon.HashBytes(body) {
		t.Fatalf("local_sha256 = %v, want %s", comp["local_sha256"], canon.HashBytes(body))
	}
	// Changing the plugin bytes must change the plugin hash.
	if err := os.WriteFile(pluginFile, append(body, []byte("// changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if componentByKey(t, p, "plugin/file://~/hello.js").Hash == componentByKey(t, after, "plugin/file://~/hello.js").Hash {
		t.Fatal("plugin hash did not change after file edit")
	}
}

// copyDir copies the regular files of src into dst, preserving relative paths.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
