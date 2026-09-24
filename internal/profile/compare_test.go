package profile

import (
	"reflect"
	"strings"
	"testing"

	"mbl/ocbench/internal/canon"
)

func TestDiffNotesExplainsChanges(t *testing.T) {
	reference := testProfile()
	subject := testProfile()
	// Subject: build switched model and gained bash; a new skill appeared; the
	// firecrawl MCP server was removed.
	setComponent(subject, "agent", "build", `{"mode":"primary","model":"anthropic/claude-sonnet-4-5","prompt_sha256":"aa","steps":40,"tools":{"bash":true,"edit":true,"task":true,"write":true},"top_p":0.95,"variant":"high"}`)
	subject.Components = append(subject.Components,
		Component{Kind: "skill", Name: "new-skill", CanonicalJSON: []byte(`{"description":"new"}`)})
	subject.Components = removeComponent(subject.Components, "mcp", "firecrawl")

	notes := DiffNotes(reference, subject)

	find := func(kind, name string) (ChangeNote, bool) {
		for _, n := range notes {
			if n.Kind == kind && n.Name == name {
				return n, true
			}
		}
		return ChangeNote{}, false
	}

	agent, ok := find("agent", "build")
	if !ok {
		t.Fatalf("no note for agent/build: %+v", notes)
	}
	if agent.Sign != "~" || !strings.Contains(agent.Note, "model deepseek-v4.1-flash → claude-sonnet-4-5") {
		t.Errorf("agent note = %+v", agent)
	}

	skill, ok := find("skill", "new-skill")
	if !ok || skill.Sign != "+" {
		t.Errorf("added skill note = %+v (ok=%v)", skill, ok)
	}

	mcp, ok := find("mcp", "firecrawl")
	if !ok || mcp.Sign != "−" {
		t.Errorf("removed mcp note = %+v (ok=%v)", mcp, ok)
	}

	// Agents sort before skills, which sort before MCP servers.
	var order []string
	for _, n := range notes {
		order = append(order, n.Kind+"/"+n.Name)
	}
	want := []string{"agent/build", "skill/new-skill", "mcp/firecrawl"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestDiffNotesIdenticalProfiles(t *testing.T) {
	if notes := DiffNotes(testProfile(), testProfile()); len(notes) != 0 {
		t.Errorf("identical profiles produced %+v", notes)
	}
}

// setComponent replaces a component's content and its hash together, because
// Diff compares hashes.
func setComponent(p *Profile, kind, name, json string) {
	for i := range p.Components {
		if p.Components[i].Kind == kind && p.Components[i].Name == name {
			p.Components[i].CanonicalJSON = []byte(json)
			p.Components[i].Hash = canon.SHA256Hex([]byte(json))
			return
		}
	}
}

func removeComponent(comps []Component, kind, name string) []Component {
	out := comps[:0]
	for _, c := range comps {
		if c.Kind == kind && c.Name == name {
			continue
		}
		out = append(out, c)
	}
	return out
}
