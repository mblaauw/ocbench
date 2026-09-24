package profile

import (
	"reflect"
	"testing"

	"mbl/ocbench/internal/canon"
)

// testProfile builds a profile whose components mirror the shapes the real
// captures use (verified against OpenCode 1.18.32), so the decoder is tested
// against reality without committing anyone's configuration.
func testProfile() *Profile {
	comp := func(kind, name, json string) Component {
		// The hash must follow the content, exactly as production does: Diff
		// compares hashes, so a fixture with constant hashes would hide every
		// change.
		return Component{Kind: kind, Name: name, Hash: canon.SHA256Hex([]byte(json)), CanonicalJSON: []byte(json)}
	}
	return &Profile{
		Hash:            "abc123",
		OpenCodeVersion: "1.18.32",
		Components: []Component{
			comp("primary", "primary", `{"default_agent":"build","model":"opencode-go/deepseek-v4.1-flash","small_model":"opencode/nemotron-3.5-lightning-free","variant":"low"}`),
			comp("agent", "build", `{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash","native":true,"prompt_sha256":"aa","steps":40,"tools":{"bash":true,"edit":true,"task":true,"write":true},"top_p":0.95,"variant":"high"}`),
			comp("agent", "plan", `{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash","prompt_sha256":"bb","steps":20,"tools":{"task":true,"edit":false},"variant":"low"}`),
			comp("agent", "explore", `{"mode":"subagent","model":"opencode-go/deepseek-v4.1-flash","prompt_sha256":"cc","tools":{"read":true,"edit":false},"variant":"low"}`),
			comp("agent", "review", `{"mode":"subagent","model":"opencode-go/deepseek-v4.1-flash","prompt_sha256":"dd","tools":{"read":true},"variant":"max"}`),
			comp("permissions", "permissions", `{"by_agent":{
				"build":[{"action":"allow","pattern":"*","permission":"task"},{"action":"allow","pattern":"explore","permission":"task"},{"action":"allow","pattern":"review","permission":"task"},{"action":"deny","pattern":"*","permission":"task"}],
				"plan":[{"action":"allow","pattern":"explore","permission":"task"},{"action":"deny","pattern":"*","permission":"task"}],
				"explore":[{"action":"deny","pattern":"*","permission":"task"}]
			},"global":["x"]}`),
			comp("skill", "diagram-design", `{"description":"Make diagrams"}`),
			comp("skill", "ruff", `{"description":"Lint python"}`),
			comp("mcp", "firecrawl", `{"type":"local","command":["npx","firecrawl"],"environment":{"FIRECRAWL_API_KEY":"redacted"}}`),
			comp("instructions", "global:AGENTS.md", `{"path":"~/.config/opencode/AGENTS.md","sha256":"ff"}`),
			comp("plugin", "file://~/.config/opencode/plugins/token-guard.ts", `{"spec":"x"}`),
			comp("config", "config", `{"subagent_depth":1,"provider":{"opencode-go":{"options":{"temperature":0.1}}},"command":{"build":{}}}`),
		},
	}
}

func TestNewViewDecodesComposition(t *testing.T) {
	v := NewView(testProfile())

	if v.Hash != "abc123" || v.OpenCodeVersion != "1.18.32" {
		t.Errorf("identity = %q/%q", v.Hash, v.OpenCodeVersion)
	}
	if v.Primary.DefaultAgent != "build" || v.Primary.Model != "opencode-go/deepseek-v4.1-flash" || v.Primary.SmallModel != "opencode/nemotron-3.5-lightning-free" {
		t.Errorf("primary = %+v", v.Primary)
	}
	if len(v.Agents) != 4 {
		t.Fatalf("agents = %d, want 4", len(v.Agents))
	}
	// Agents are sorted by name.
	if got := []string{v.Agents[0].Name, v.Agents[1].Name, v.Agents[2].Name, v.Agents[3].Name}; !reflect.DeepEqual(got, []string{"build", "explore", "plan", "review"}) {
		t.Errorf("agent order = %v", got)
	}

	build := v.Agents[0]
	if build.Mode != "primary" || build.Variant != "high" || build.Steps != 40 || build.TopP != 0.95 || !build.Native {
		t.Errorf("build agent = %+v", build)
	}
	if !build.Tools["task"] || build.Tools["edit"] != true {
		t.Errorf("build tools = %v", build.Tools)
	}
	if build.PromptSHA != "aa" {
		t.Errorf("prompt hash = %q", build.PromptSHA)
	}

	if len(v.Skills) != 2 || v.Skills[0].Name != "diagram-design" || v.Skills[0].Description != "Make diagrams" {
		t.Errorf("skills = %+v", v.Skills)
	}
	if len(v.MCP) != 1 || v.MCP[0].Name != "firecrawl" {
		t.Fatalf("mcp = %+v", v.MCP)
	}
	// Only key names, never values.
	if !reflect.DeepEqual(v.MCP[0].Keys, []string{"command", "environment", "type"}) {
		t.Errorf("mcp keys = %v", v.MCP[0].Keys)
	}
	if len(v.Instructions) != 1 || v.Instructions[0].Scope != "global:AGENTS.md" || v.Instructions[0].Path != "~/.config/opencode/AGENTS.md" {
		t.Errorf("instructions = %+v", v.Instructions)
	}
	if len(v.Plugins) != 1 {
		t.Errorf("plugins = %+v", v.Plugins)
	}
	if v.SubagentDepth != 1 {
		t.Errorf("subagent depth = %d", v.SubagentDepth)
	}
	if _, ok := v.Config["provider"]; !ok {
		t.Errorf("config missing provider: %v", v.Config)
	}
}

func TestViewArchitecture(t *testing.T) {
	v := NewView(testProfile())
	arch := v.Architecture()

	if len(arch.Subagents) != 2 {
		t.Fatalf("subagents = %d, want 2", len(arch.Subagents))
	}
	var primaries []string
	for _, p := range arch.Primaries {
		primaries = append(primaries, p.Name)
	}
	if !reflect.DeepEqual(primaries, []string{"build", "plan"}) {
		t.Errorf("primaries = %v", primaries)
	}

	// build allows review explicitly and explore by catch-all; the trailing
	// deny "*" must not cancel the specific allow.
	want := []Edge{{From: "build", To: "explore"}, {From: "build", To: "review"}, {From: "plan", To: "explore"}}
	if !reflect.DeepEqual(arch.Edges, want) {
		t.Errorf("edges = %+v, want %+v", arch.Edges, want)
	}
}

func TestViewSummary(t *testing.T) {
	v := NewView(testProfile())
	if got, want := v.Summary(), "build · deepseek-v4.1-flash high · +2 sub"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}

	// A profile with no subagents omits the suffix.
	bare := NewView(&Profile{Components: []Component{
		{Kind: "primary", Name: "primary", CanonicalJSON: []byte(`{"default_agent":"quick","model":"opencode-go/deepseek-v4.1-flash","variant":"low"}`)},
		{Kind: "agent", Name: "quick", CanonicalJSON: []byte(`{"mode":"primary","model":"opencode-go/deepseek-v4.1-flash","variant":"low"}`)},
	}})
	if got, want := bare.Summary(), "quick · deepseek-v4.1-flash low"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestNewViewToleratesBrokenComponents(t *testing.T) {
	v := NewView(&Profile{
		Hash: "x",
		Components: []Component{
			{Kind: "agent", Name: "broken", CanonicalJSON: []byte("{not json")},
			{Kind: "primary", Name: "primary", CanonicalJSON: []byte("[]")},
			{Kind: "mcp", Name: "weird", CanonicalJSON: []byte("7")},
			{Kind: "config", Name: "config", CanonicalJSON: []byte("null")},
		},
	})
	if len(v.Agents) != 0 {
		t.Errorf("undecodable agent should be skipped: %+v", v.Agents)
	}
	if v.MCP[0].Keys != nil {
		t.Errorf("non-object mcp keys = %v", v.MCP[0].Keys)
	}
	if _, ok := v.PrimaryAgent(); ok {
		t.Error("no primary agent expected")
	}
	if got := v.Summary(); got != "no primary agent" {
		t.Errorf("Summary = %q", got)
	}
}
