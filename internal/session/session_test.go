package session

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"mbl/ocbench/internal/evaluation"
)

const (
	parentSessionID = "ses_f2bc3d92affeCnLLfkkU6BkZso"
	childSessionID  = "ses_f2bc3c5a4ffe0WMXC38zE4mJNF"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

func parseFixture(t *testing.T, name string) *Session {
	t.Helper()
	s, err := ParseExport(readFixture(t, name))
	if err != nil {
		t.Fatalf("ParseExport(%s): %v", name, err)
	}
	return s
}

func wantCost(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v (±1e-9)", got, want)
	}
}

func TestParseExportParent(t *testing.T) {
	s := parseFixture(t, "parent-session.json")
	if s.ID != parentSessionID {
		t.Fatalf("ID = %q, want %q", s.ID, parentSessionID)
	}
	if s.Agent != "build" {
		t.Fatalf("Agent = %q, want build", s.Agent)
	}
	wantCost(t, s.Cost, 0.002078364)
	if s.Tokens.Total != 25175 {
		t.Fatalf("Tokens.Total = %d, want 25175", s.Tokens.Total)
	}
	if s.Tokens.Input != 12646 || s.Tokens.Output != 211 || s.Tokens.Reasoning != 30 {
		t.Fatalf("token breakdown = %+v", s.Tokens)
	}
	if s.Tokens.CacheRead != 12288 || s.Tokens.CacheWrite != 0 {
		t.Fatalf("cache = %+v", s.Tokens)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(s.Messages))
	}
	// The task tool call on the first assistant message.
	var found *ToolCall
	for i := range s.Messages {
		for j := range s.Messages[i].Tools {
			if s.Messages[i].Tools[j].Name == "task" {
				found = &s.Messages[i].Tools[j]
			}
		}
	}
	if found == nil {
		t.Fatal("no task tool call found in parent messages")
	}
	if found.Title != "List files in directory" || found.Status != "completed" {
		t.Fatalf("task tool = %+v", *found)
	}
	if found.DurationMS != 3778 {
		t.Fatalf("task DurationMS = %d, want 3778", found.DurationMS)
	}
}

func TestParseExportChild(t *testing.T) {
	s := parseFixture(t, "child-session.json")
	if s.ID != childSessionID {
		t.Fatalf("ID = %q, want %q", s.ID, childSessionID)
	}
	if s.Agent != "explore" {
		t.Fatalf("Agent = %q, want explore", s.Agent)
	}
	wantCost(t, s.Cost, 0.000511764)
	if s.Tokens.Total != 5653 {
		t.Fatalf("Tokens.Total = %d, want 5653", s.Tokens.Total)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(s.Messages))
	}
	var found *ToolCall
	for i := range s.Messages {
		for j := range s.Messages[i].Tools {
			if s.Messages[i].Tools[j].Name == "read" {
				found = &s.Messages[i].Tools[j]
			}
		}
	}
	if found == nil {
		t.Fatal("no read tool call found in child messages")
	}
	if found.Status != "completed" {
		t.Fatalf("read tool = %+v", *found)
	}
	if found.DurationMS != 4 {
		t.Fatalf("read DurationMS = %d, want 4", found.DurationMS)
	}
}

func TestParseExportComputesTotalWhenMissing(t *testing.T) {
	data := []byte(`{"info":{"id":"ses_syn","agent":"build","cost":0.5,"tokens":{"input":10,"output":2,"reasoning":1,"cache":{"read":3,"write":4}}},"messages":[]}`)
	s, err := ParseExport(data)
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if s.Tokens.Total != 16 {
		t.Fatalf("Tokens.Total = %d, want 16 (10+2+1+3)", s.Tokens.Total)
	}
	if s.Tokens.CacheWrite != 4 {
		t.Fatalf("CacheWrite = %d, want 4", s.Tokens.CacheWrite)
	}
}

func TestParseExportUsesExplicitTotal(t *testing.T) {
	data := []byte(`{"info":{"id":"ses_syn","tokens":{"total":99,"input":10,"output":2,"reasoning":1,"cache":{"read":3,"write":4}}}}`)
	s, err := ParseExport(data)
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if s.Tokens.Total != 99 {
		t.Fatalf("Tokens.Total = %d, want explicit 99", s.Tokens.Total)
	}
}

func TestParseExportTolerantOfMissingFields(t *testing.T) {
	s, err := ParseExport([]byte(`{"info":{"id":"ses_min"}}`))
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if s.ID != "ses_min" || s.Agent != "" || s.Cost != 0 {
		t.Fatalf("session = %+v", s)
	}
	if s.Tokens != (Tokens{}) {
		t.Fatalf("tokens = %+v, want zero", s.Tokens)
	}
}

func TestParseExportUnknownFieldsIgnored(t *testing.T) {
	s, err := ParseExport([]byte(`{"info":{"id":"ses_x","agent":"build","future":{"x":1},"tokens":{"input":1,"mystery":7}},"messages":[],"extra":true}`))
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if s.ID != "ses_x" || s.Agent != "build" || s.Tokens.Input != 1 {
		t.Fatalf("session = %+v", s)
	}
}

func TestParseExportNoMessages(t *testing.T) {
	s, err := ParseExport([]byte(`{"info":{"id":"ses_empty","agent":"build"}}`))
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if len(s.Messages) != 0 {
		t.Fatalf("Messages = %+v, want none", s.Messages)
	}
}

func TestParseExportMalformed(t *testing.T) {
	if _, err := ParseExport([]byte(`{"info":`)); err == nil {
		t.Fatal("want error for malformed export")
	}
}

func TestRollup(t *testing.T) {
	parent := parseFixture(t, "parent-session.json")
	child := parseFixture(t, "child-session.json")

	got := Rollup([]*Session{parent, child})
	if len(got) != 2 {
		t.Fatalf("len(Rollup) = %d, want 2", len(got))
	}
	if got[0].Agent != "build" || got[1].Agent != "explore" {
		t.Fatalf("order = [%q, %q], want [build, explore]", got[0].Agent, got[1].Agent)
	}

	build := got[0]
	if build.Messages != 3 || build.ToolCalls != 1 || build.ToolCallsFailed != 0 {
		t.Fatalf("build counts = %+v", build)
	}
	wantCost(t, build.Cost, 0.002078364)
	if build.Tokens.Total != 25175 || build.Tokens.Input != 12646 {
		t.Fatalf("build tokens = %+v", build.Tokens)
	}

	explore := got[1]
	if explore.Messages != 3 || explore.ToolCalls != 1 || explore.ToolCallsFailed != 0 {
		t.Fatalf("explore counts = %+v", explore)
	}
	wantCost(t, explore.Cost, 0.000511764)
	if explore.Tokens.Total != 5653 {
		t.Fatalf("explore tokens = %+v", explore.Tokens)
	}
}

func TestRollupCountsFailedTools(t *testing.T) {
	data := []byte(`{"info":{"id":"ses_f","agent":"build"},"messages":[
		{"info":{"agent":"build"},"parts":[
			{"type":"tool","tool":"bash","state":{"status":"completed","time":{"start":100,"end":200}}},
			{"type":"tool","tool":"edit","state":{"status":"error","time":{"start":300,"end":400}}}
		]}
	]}`)
	s, err := ParseExport(data)
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	got := Rollup([]*Session{s})
	if len(got) != 1 {
		t.Fatalf("len(Rollup) = %d, want 1", len(got))
	}
	if got[0].ToolCalls != 2 || got[0].ToolCallsFailed != 1 {
		t.Fatalf("rollup = %+v", got[0])
	}
}

func TestRollupEmpty(t *testing.T) {
	if got := Rollup(nil); len(got) != 0 {
		t.Fatalf("Rollup(nil) = %+v, want empty", got)
	}
}

// TestRollupUsesMessageLevelNotSessionLevel pins spec §13.3: the roll-up uses
// each message's info.agent/tokens/cost, not the session-level info totals.
// The session totals here are deliberately much larger than the message sums.
func TestRollupUsesMessageLevelNotSessionLevel(t *testing.T) {
	data := []byte(`{"info":{"id":"ses_agg","agent":"build","cost":99,"tokens":{"total":9999,"input":9000,"output":900,"reasoning":90,"cache":{"read":9,"write":0}}},"messages":[
		{"info":{"agent":"build","cost":0.25,"tokens":{"total":11,"input":7,"output":2,"reasoning":1,"cache":{"read":1,"write":0}}},"parts":[
			{"type":"tool","tool":"bash","state":{"status":"completed","time":{"start":100,"end":200}}}
		]},
		{"info":{"agent":"explore","cost":0.75,"tokens":{"total":22,"input":14,"output":4,"reasoning":2,"cache":{"read":2,"write":0}}},"parts":[]}
	]}`)
	s, err := ParseExport(data)
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	got := Rollup([]*Session{s})
	if len(got) != 2 {
		t.Fatalf("len(Rollup) = %d, want 2: %+v", len(got), got)
	}
	if got[0].Agent != "build" || got[1].Agent != "explore" {
		t.Fatalf("order = [%q, %q], want [build, explore]", got[0].Agent, got[1].Agent)
	}
	build := got[0]
	if build.Messages != 1 || build.ToolCalls != 1 {
		t.Fatalf("build counts = %+v", build)
	}
	wantCost(t, build.Cost, 0.25)
	if build.Tokens.Total != 11 || build.Tokens.Input != 7 {
		t.Fatalf("build tokens = %+v, want message-derived 11/7 (not session 9999/9000)", build.Tokens)
	}
	explore := got[1]
	if explore.Messages != 1 {
		t.Fatalf("explore counts = %+v", explore)
	}
	wantCost(t, explore.Cost, 0.75)
	if explore.Tokens.Total != 22 {
		t.Fatalf("explore tokens = %+v, want message-derived 22 (not session 9999)", explore.Tokens)
	}
}

func TestRollupAggregatesSameAgentAcrossSessions(t *testing.T) {
	s1, err := ParseExport([]byte(`{"info":{"id":"ses_1","agent":"x"},"messages":[{"info":{"agent":"x","cost":1,"tokens":{"total":10,"input":10}},"parts":[]}]}`))
	if err != nil {
		t.Fatalf("ParseExport s1: %v", err)
	}
	s2, err := ParseExport([]byte(`{"info":{"id":"ses_2","agent":"x"},"messages":[{"info":{"agent":"x","cost":2,"tokens":{"total":20,"input":20}},"parts":[]}]}`))
	if err != nil {
		t.Fatalf("ParseExport s2: %v", err)
	}
	got := Rollup([]*Session{s1, s2})
	if len(got) != 1 {
		t.Fatalf("len(Rollup) = %d, want 1 merged entry: %+v", len(got), got)
	}
	if got[0].Agent != "x" || got[0].Messages != 2 {
		t.Fatalf("rollup = %+v, want agent x with 2 messages", got[0])
	}
	wantCost(t, got[0].Cost, 3)
	if got[0].Tokens.Total != 30 || got[0].Tokens.Input != 30 {
		t.Fatalf("tokens = %+v, want 30", got[0].Tokens)
	}
}

func TestParseExportClampsNegativeDuration(t *testing.T) {
	data := []byte(`{"info":{"id":"ses_clamp","agent":"build"},"messages":[
		{"info":{"agent":"build"},"parts":[
			{"type":"tool","tool":"bash","state":{"status":"running","time":{"start":500}}}
		]}
	]}`)
	s, err := ParseExport(data)
	if err != nil {
		t.Fatalf("ParseExport: %v", err)
	}
	if len(s.Messages) != 1 || len(s.Messages[0].Tools) != 1 {
		t.Fatalf("messages = %+v", s.Messages)
	}
	if got := s.Messages[0].Tools[0].DurationMS; got != 0 {
		t.Fatalf("DurationMS = %d, want clamped 0", got)
	}
}

func mustEvent(t *testing.T, line string) evaluation.Event {
	t.Helper()
	e, err := evaluation.ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseLine(%s): %v", line, err)
	}
	return e
}

func TestDiscoverChildrenFixture(t *testing.T) {
	raw := readFixture(t, "events.jsonl")
	var events []evaluation.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		events = append(events, mustEvent(t, line))
	}

	got := DiscoverChildren(events)
	if len(got) != 1 {
		t.Fatalf("len(DiscoverChildren) = %d, want 1: %+v", len(got), got)
	}
	want := ChildRef{
		SessionID:       childSessionID,
		ParentSessionID: parentSessionID,
		Description:     "List files in directory",
	}
	if got[0] != want {
		t.Fatalf("ref = %+v, want %+v", got[0], want)
	}
}

func TestDiscoverChildrenSkipsNonTaskAndEmpty(t *testing.T) {
	events := []evaluation.Event{
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"read","state":{"metadata":{"sessionId":"ses_read"}}}}`),
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"title":"empty","metadata":{"sessionId":"","parentSessionId":"ses_p"}}}}`),
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"metadata":{"parentSessionId":"ses_p"}}}}`),
	}
	if got := DiscoverChildren(events); len(got) != 0 {
		t.Fatalf("DiscoverChildren = %+v, want empty", got)
	}
}

func TestDiscoverChildrenDedupesInputOrder(t *testing.T) {
	events := []evaluation.Event{
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"title":"first","metadata":{"sessionId":"ses_a","parentSessionId":"ses_p"}}}}`),
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"title":"second","metadata":{"sessionId":"ses_a","parentSessionId":"ses_p"}}}}`),
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"title":"third","metadata":{"sessionId":"ses_b","parentSessionId":"ses_p"}}}}`),
	}
	got := DiscoverChildren(events)
	if len(got) != 2 {
		t.Fatalf("len(DiscoverChildren) = %d, want 2: %+v", len(got), got)
	}
	if got[0].SessionID != "ses_a" || got[0].Description != "first" {
		t.Fatalf("first ref = %+v, want ses_a/first", got[0])
	}
	if got[1].SessionID != "ses_b" || got[1].Description != "third" {
		t.Fatalf("second ref = %+v, want ses_b/third", got[1])
	}
}

func TestDiscoverChildrenTolerantOfBadPart(t *testing.T) {
	events := []evaluation.Event{
		{Type: "tool_use", Part: json.RawMessage(`123`)},
		mustEvent(t, `{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"title":"ok","metadata":{"sessionId":"ses_ok","parentSessionId":"ses_p"}}}}`),
	}
	got := DiscoverChildren(events)
	if len(got) != 1 || got[0].SessionID != "ses_ok" {
		t.Fatalf("DiscoverChildren = %+v, want ses_ok", got)
	}
}
