package trace

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/session"
)

// childSessionID is the delegated child captured in the shared session
// fixtures (internal/session/testdata).
const childSessionID = "ses_f2bc3c5a4ffe0WMXC38zE4mJNF"

// parseFixtureEvents reads a JSONL fixture into events, failing on a malformed
// line so a broken fixture never silently weakens a test.
func parseFixtureEvents(t *testing.T, path string) []evaluation.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var events []evaluation.Event
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		e, perr := evaluation.ParseLine([]byte(line))
		if perr != nil {
			t.Fatalf("parse %s line %q: %v", path, line, perr)
		}
		events = append(events, e)
	}
	return events
}

func fixtureEvents(t *testing.T) []evaluation.Event {
	t.Helper()
	return parseFixtureEvents(t, filepath.Join("..", "session", "testdata", "events.jsonl"))
}

func fixtureChild(t *testing.T) *session.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "session", "testdata", "child-session.json"))
	if err != nil {
		t.Fatalf("read child fixture: %v", err)
	}
	s, err := session.ParseExport(data)
	if err != nil {
		t.Fatalf("parse child fixture: %v", err)
	}
	return s
}

func syntheticEvents(t *testing.T, lines ...string) []evaluation.Event {
	t.Helper()
	var out []evaluation.Event
	for _, l := range lines {
		e, err := evaluation.ParseLine([]byte(l))
		if err != nil {
			t.Fatalf("parse synthetic line %q: %v", l, err)
		}
		out = append(out, e)
	}
	return out
}

// taskFreeStep is a synthetic step with no tool calls, used to reach the
// "two task-free steps" shape from the two-step probe fixture.
func taskFreeStep(t *testing.T, start int64) []evaluation.Event {
	t.Helper()
	return syntheticEvents(t,
		`{"type":"step_start","timestamp":`+itoa(start)+`,"sessionID":"ses_parent","part":{"type":"step-start"}}`,
		`{"type":"text","timestamp":`+itoa(start+100)+`,"sessionID":"ses_parent","part":{"type":"text","text":"done"}}`,
		`{"type":"step_finish","timestamp":`+itoa(start+200)+`,"sessionID":"ses_parent","part":{"type":"step-finish","tokens":{"total":100,"input":80,"output":20,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0.01}}`,
	)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestTraceBuildFixtureEvents(t *testing.T) {
	events := fixtureEvents(t)
	events = append(events, taskFreeStep(t, 1790267120000)...)
	child := fixtureChild(t)

	tr := Build("run-1", "py-bugfix", events, map[string]*session.Session{child.ID: child})

	if tr.RunID != "run-1" || tr.TaskID != "py-bugfix" {
		t.Fatalf("identity = %q/%q", tr.RunID, tr.TaskID)
	}
	if len(tr.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(tr.Steps))
	}
	for i, s := range tr.Steps {
		if s.Index != i {
			t.Fatalf("step %d Index = %d", i, s.Index)
		}
	}

	// Step 0 carries the task call; step 1 and the appended step are task-free.
	s0 := tr.Steps[0]
	if s0.Tokens.Total != 12491 {
		t.Fatalf("step 0 tokens total = %d, want 12491", s0.Tokens.Total)
	}
	if math.Abs(s0.Cost-0.00194745) > 1e-12 {
		t.Fatalf("step 0 cost = %v, want 0.00194745", s0.Cost)
	}
	if len(s0.Tools) != 1 {
		t.Fatalf("step 0 tools = %d, want 1", len(s0.Tools))
	}
	tool := s0.Tools[0]
	if tool.Tool != "task" || tool.Title != "List files in directory" || tool.Status != "completed" {
		t.Fatalf("task tool = %+v", tool)
	}
	if tool.DurationMS != 3778 {
		t.Fatalf("task duration = %d, want 3778", tool.DurationMS)
	}
	if tool.Child == nil {
		t.Fatal("task tool has no child span")
	}
	if tool.Child.SessionID != childSessionID {
		t.Fatalf("child session = %q, want %q", tool.Child.SessionID, childSessionID)
	}
	if tool.Child.Agent != "explore" {
		t.Fatalf("child agent = %q, want explore", tool.Child.Agent)
	}
	if tool.Child.Tokens.Total != 5653 {
		t.Fatalf("child tokens = %d, want 5653", tool.Child.Tokens.Total)
	}
	if math.Abs(tool.Child.Cost-0.0005117640000000001) > 1e-12 {
		t.Fatalf("child cost = %v, want 0.000511764", tool.Child.Cost)
	}
	if len(tool.Child.Tools) != 1 || tool.Child.Tools[0].Name != "read" {
		t.Fatalf("child tools = %+v, want one read", tool.Child.Tools)
	}

	for _, i := range []int{1, 2} {
		if len(tr.Steps[i].Tools) != 0 {
			t.Fatalf("step %d should be task-free, tools = %+v", i, tr.Steps[i].Tools)
		}
	}
	if tr.Steps[1].Tokens.Total != 12684 {
		t.Fatalf("step 1 tokens total = %d, want 12684", tr.Steps[1].Tokens.Total)
	}

	if len(tr.Subagents) != 1 {
		t.Fatalf("subagents = %d, want 1", len(tr.Subagents))
	}
	if tr.Subagents[0].SessionID != childSessionID || tr.Subagents[0].Agent != "explore" {
		t.Fatalf("subagent = %+v", tr.Subagents[0])
	}
}

func TestTraceBuildTaskWithoutCapturedChild(t *testing.T) {
	tr := Build("run-1", "py-bugfix", fixtureEvents(t), nil)

	if len(tr.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(tr.Steps))
	}
	if len(tr.Steps[0].Tools) != 1 {
		t.Fatalf("step 0 tools = %d, want 1", len(tr.Steps[0].Tools))
	}
	child := tr.Steps[0].Tools[0].Child
	if child == nil {
		t.Fatal("uncaptured task should still yield a child span")
	}
	if child.SessionID != childSessionID {
		t.Fatalf("child session = %q, want %q", child.SessionID, childSessionID)
	}
	if child.Agent != "" || child.Tokens.Total != 0 || child.Cost != 0 {
		t.Fatalf("uncaptured child = %+v, want zero agent/tokens/cost", child)
	}
	if len(tr.Subagents) != 0 {
		t.Fatalf("subagents = %+v, want none", tr.Subagents)
	}
}

func TestTraceBuildRetryCompactionCounters(t *testing.T) {
	events := syntheticEvents(t,
		`{"type":"retry","timestamp":1,"part":{"type":"retry","attempt":1}}`,
		`{"type":"compaction","timestamp":2,"part":{"type":"compaction"}}`,
		`{"type":"step_start","timestamp":10,"part":{"type":"step-start"}}`,
		`{"type":"retry","timestamp":11,"part":{"type":"retry","attempt":2}}`,
		`{"type":"step_finish","timestamp":20,"part":{"type":"step-finish","tokens":{"total":5,"input":5,"output":0,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0.1}}`,
	)
	tr := Build("run-1", "t", events, nil)

	if len(tr.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (synthetic 0 + step 1)", len(tr.Steps))
	}
	if tr.Steps[0].Index != 0 || tr.Steps[0].Retries != 1 || tr.Steps[0].Compactions != 1 {
		t.Fatalf("synthetic step 0 = %+v", tr.Steps[0])
	}
	if tr.Steps[1].Index != 1 || tr.Steps[1].Retries != 1 || tr.Steps[1].Compactions != 0 {
		t.Fatalf("step 1 = %+v", tr.Steps[1])
	}
	if tr.Steps[0].DurationMS != 1 || tr.Steps[1].DurationMS != 10 {
		t.Fatalf("durations = %d/%d, want 1/10", tr.Steps[0].DurationMS, tr.Steps[1].DurationMS)
	}
}

func TestTraceBuildSubagentsFirstAppearanceOrder(t *testing.T) {
	events := syntheticEvents(t,
		`{"type":"step_start","timestamp":10,"part":{"type":"step-start"}}`,
		`{"type":"tool_use","timestamp":11,"part":{"type":"tool","tool":"task","state":{"status":"completed","title":"a","metadata":{"sessionId":"ses_a"},"time":{"start":10,"end":11}}}}`,
		`{"type":"tool_use","timestamp":12,"part":{"type":"tool","tool":"task","state":{"status":"completed","title":"b","metadata":{"sessionId":"ses_b"},"time":{"start":11,"end":12}}}}`,
		`{"type":"tool_use","timestamp":13,"part":{"type":"tool","tool":"task","state":{"status":"completed","title":"a2","metadata":{"sessionId":"ses_a"},"time":{"start":12,"end":13}}}}`,
		`{"type":"step_finish","timestamp":20,"part":{"type":"step-finish","tokens":{"total":1,"input":1,"output":0,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`,
	)
	sessions := map[string]*session.Session{
		"ses_a": {ID: "ses_a", Agent: "explore", Tokens: session.Tokens{Total: 10}},
		"ses_b": {ID: "ses_b", Agent: "build", Tokens: session.Tokens{Total: 20}},
	}
	tr := Build("run-1", "t", events, sessions)

	if len(tr.Subagents) != 2 {
		t.Fatalf("subagents = %d, want 2", len(tr.Subagents))
	}
	if tr.Subagents[0].SessionID != "ses_a" || tr.Subagents[1].SessionID != "ses_b" {
		t.Fatalf("subagent order = %q/%q, want ses_a/ses_b",
			tr.Subagents[0].SessionID, tr.Subagents[1].SessionID)
	}
}
