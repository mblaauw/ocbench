package evaluation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseLineEnvelope(t *testing.T) {
	line := []byte(`{"type":"step_start","timestamp":1790099576240,"sessionID":"ses_abc","part":{"type":"step-start"}}`)
	e, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if e.Type != "step_start" || e.Timestamp != 1790099576240 || e.SessionID != "ses_abc" {
		t.Fatalf("envelope = %+v", e)
	}
	if !json.Valid(e.Part) {
		t.Fatalf("part is not valid JSON: %s", e.Part)
	}
}

func TestParseLineRejectsEmptyType(t *testing.T) {
	if _, err := ParseLine([]byte(`{"timestamp":1,"part":{}}`)); err == nil {
		t.Fatal("want error for missing type")
	}
}

func TestParseLineRejectsMalformedEnvelope(t *testing.T) {
	if _, err := ParseLine([]byte(`{"type":`)); err == nil {
		t.Fatal("want error for malformed envelope")
	}
	if _, err := ParseLine([]byte("not json at all")); err == nil {
		t.Fatal("want error for non-JSON line")
	}
}

func TestPartDecodedTool(t *testing.T) {
	e, err := ParseLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"read","callID":"call_1","state":{"status":"completed"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.PartDecoded()
	if err != nil {
		t.Fatalf("PartDecoded: %v", err)
	}
	if p.Type != "tool" || p.Tool != "read" || p.CallID != "call_1" {
		t.Fatalf("part = %+v", p)
	}
	if string(p.State) != `{"status":"completed"}` {
		t.Fatalf("state = %s", p.State)
	}
}

func TestPartDecodedTokensAndCost(t *testing.T) {
	e, err := ParseLine([]byte(`{"type":"step_finish","part":{"type":"step-finish","reason":"stop","tokens":{"total":10,"input":7,"output":2,"reasoning":1,"cache":{"read":3,"write":4}},"cost":0.5}}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.PartDecoded()
	if err != nil {
		t.Fatalf("PartDecoded: %v", err)
	}
	if p.Tokens == nil {
		t.Fatal("Tokens is nil")
	}
	if p.Tokens.Total != 10 || p.Tokens.Input != 7 || p.Tokens.Output != 2 || p.Tokens.Reasoning != 1 {
		t.Fatalf("tokens = %+v", p.Tokens)
	}
	if p.Tokens.Cache.Read != 3 || p.Tokens.Cache.Write != 4 {
		t.Fatalf("cache = %+v", p.Tokens.Cache)
	}
	if p.Cost == nil || *p.Cost != 0.5 {
		t.Fatalf("cost = %v", p.Cost)
	}
	if p.Reason != "stop" {
		t.Fatalf("reason = %q", p.Reason)
	}
}

func TestPartDecodedInvalid(t *testing.T) {
	e := Event{Type: "tool_use", Part: json.RawMessage(`123`)}
	if _, err := e.PartDecoded(); err == nil {
		t.Fatal("want error decoding non-object part")
	}
}

func TestParseLineLargeText(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	raw, err := json.Marshal(map[string]any{
		"type": "text",
		"part": map[string]any{"type": "text", "text": big},
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseLine(raw)
	if err != nil {
		t.Fatalf("ParseLine 1MB: %v", err)
	}
	p, err := e.PartDecoded()
	if err != nil {
		t.Fatalf("PartDecoded 1MB: %v", err)
	}
	if len(p.Text) != 1<<20 {
		t.Fatalf("text length = %d", len(p.Text))
	}
}
