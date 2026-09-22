package opencode

import (
	"encoding/json"
	"testing"
)

func TestAgentInfoUnmarshalModelObject(t *testing.T) {
	raw := `{"name":"architect","mode":"subagent","model":{"providerID":"openai","modelID":"gpt-5.6-sol"},"variant":"high","steps":25,"temperature":0.2,"tools":{"read":true}}`
	var info AgentInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Model != "openai/gpt-5.6-sol" {
		t.Fatalf("Model = %q, want openai/gpt-5.6-sol", info.Model)
	}
	if info.Mode != "subagent" || info.Variant != "high" {
		t.Fatalf("info = %+v", info)
	}
	if info.Steps == nil || *info.Steps != 25 {
		t.Fatalf("Steps = %v", info.Steps)
	}
	if info.Temperature == nil || *info.Temperature != 0.2 {
		t.Fatalf("Temperature = %v", info.Temperature)
	}
}

func TestAgentInfoUnmarshalModelString(t *testing.T) {
	var info AgentInfo
	if err := json.Unmarshal([]byte(`{"name":"build","model":"p/m"}`), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Model != "p/m" {
		t.Fatalf("Model = %q, want p/m", info.Model)
	}
}

func TestParseVersionTrimsNoise(t *testing.T) {
	got, err := parseVersion([]byte("1.18.32\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.18.32" {
		t.Fatalf("got %q", got)
	}
}

func TestParseMCPListText(t *testing.T) {
	out := "\x1b[0m\n\u250c  MCP Servers\n\u2502\n\u25cf  \u25cb firecrawl \x1b[90mdisabled\n\u2502      https://mcp.firecrawl.dev//v2/mcp\n\u2502\n\u25cf  \u25cf gitlab \x1b[90mconnected\n\u2502      npx -y gitlab-mcp\n"
	got, err := parseMCPList([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("servers = %d: %+v", len(got), got)
	}
	if got[0].Name != "firecrawl" || got[0].Enabled || got[0].Target != "https://mcp.firecrawl.dev//v2/mcp" {
		t.Fatalf("server 0 = %+v", got[0])
	}
	if got[1].Name != "gitlab" || !got[1].Enabled || got[1].Target != "npx -y gitlab-mcp" {
		t.Fatalf("server 1 = %+v", got[1])
	}
}

func TestParseMCPListEmpty(t *testing.T) {
	got, err := parseMCPList([]byte("\x1b[0m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[90mdisabled\x1b[0m"); got != "disabled" {
		t.Fatalf("got %q", got)
	}
	if got := stripANSI("plain"); got != "plain" {
		t.Fatalf("got %q", got)
	}
}
