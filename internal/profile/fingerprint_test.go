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
