package opencode

import "testing"

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
