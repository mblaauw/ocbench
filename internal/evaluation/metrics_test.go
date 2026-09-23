package evaluation

import (
	"bufio"
	"bytes"
	"context"
	"math"
	"os"
	"testing"
)

const wantFinalAnswer = "Done. `calc.py:2` now returns `a + b`."

func observeFile(t *testing.T, path string, mcp []string) *Metrics {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	m := NewMetrics(mcp)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		m.ObserveLine(line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return m
}

// TestMetricsProbeFixture pins the exact values observed from the real
// OpenCode 1.18.32 probe stream captured in testdata/probe-events.jsonl.
func TestMetricsProbeFixture(t *testing.T) {
	m := observeFile(t, "testdata/probe-events.jsonl", nil)

	if m.ParseErrors != 0 {
		t.Errorf("ParseErrors = %d, want 0", m.ParseErrors)
	}
	if m.Steps != 3 {
		t.Errorf("Steps = %d, want 3", m.Steps)
	}
	if m.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", m.ToolCalls)
	}
	if m.ToolCallsByName["read"] != 1 || m.ToolCallsByName["edit"] != 1 {
		t.Errorf("ToolCallsByName = %v, want read=1 edit=1", m.ToolCallsByName)
	}
	if m.ToolCallsFailed != 0 {
		t.Errorf("ToolCallsFailed = %d, want 0", m.ToolCallsFailed)
	}
	if m.SubagentCalls != 0 {
		t.Errorf("SubagentCalls = %d, want 0", m.SubagentCalls)
	}
	if m.SkillLoads != 0 {
		t.Errorf("SkillLoads = %d, want 0", m.SkillLoads)
	}
	if m.MCPCalls != 0 {
		t.Errorf("MCPCalls = %d, want 0", m.MCPCalls)
	}
	if m.Retries != 0 || m.Compactions != 0 {
		t.Errorf("Retries/Compactions = %d/%d, want 0/0", m.Retries, m.Compactions)
	}
	if len(m.Texts) != 1 {
		t.Fatalf("Texts = %d, want 1", len(m.Texts))
	}
	if m.FinalAnswer != wantFinalAnswer {
		t.Errorf("FinalAnswer = %q, want %q", m.FinalAnswer, wantFinalAnswer)
	}
	if m.TokensInput != 15540 {
		t.Errorf("TokensInput = %d, want 15540", m.TokensInput)
	}
	if m.TokensOutput != 212 {
		t.Errorf("TokensOutput = %d, want 212", m.TokensOutput)
	}
	if m.TokensReasoning != 239 {
		t.Errorf("TokensReasoning = %d, want 239", m.TokensReasoning)
	}
	if m.TokensCacheRead != 31104 {
		t.Errorf("TokensCacheRead = %d, want 31104", m.TokensCacheRead)
	}
	if m.TokensCacheWrite != 0 {
		t.Errorf("TokensCacheWrite = %d, want 0", m.TokensCacheWrite)
	}
	if m.TokensTotal != 47095 {
		t.Errorf("TokensTotal = %d, want 47095", m.TokensTotal)
	}
	if math.Abs(m.Cost-0.002694912) > 1e-9 {
		t.Errorf("Cost = %v, want 0.002694912", m.Cost)
	}
}

func TestObserveTextJoin(t *testing.T) {
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"text","part":{"type":"text","text":"first"}}`))
	m.ObserveLine([]byte(`{"type":"text","part":{"type":"text","text":"second"}}`))
	if len(m.Texts) != 2 {
		t.Fatalf("Texts = %v", m.Texts)
	}
	if m.FinalAnswer != "second" {
		t.Fatalf("FinalAnswer = %q, want the last text event only", m.FinalAnswer)
	}
}

// TestObserveTextFinalAnswerIsLastEvent proves that Metrics.Texts keeps the full
// ordered text transcript while Metrics.FinalAnswer is exactly the most recent
// text event. An intermediate text event that happens to contain an evaluator
// pattern must not let an answer validator pass when the terminal text omits it.
func TestObserveTextFinalAnswerIsLastEvent(t *testing.T) {
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"text","part":{"type":"text","text":"the maximum is 5"}}`))
	m.ObserveLine([]byte(`{"type":"text","part":{"type":"text","text":"done"}}`))

	if len(m.Texts) != 2 || m.Texts[0] != "the maximum is 5" || m.Texts[1] != "done" {
		t.Fatalf("Texts = %q, want both text events in order", m.Texts)
	}
	if m.FinalAnswer != "done" {
		t.Fatalf("FinalAnswer = %q, want terminal text only", m.FinalAnswer)
	}

	// A real answer validator over FinalAnswer must reject the intermediate
	// match: only the final text event is the model's answer.
	spec := ValidatorSpec{Kind: "answer", Name: "mentions maximum", Patterns: []string{`(?i)\bmaximum\b`}, Mode: "all"}
	res := RunValidator(context.Background(), 0, spec, "", nil, 0, m.FinalAnswer)
	if res.Status != "failed" {
		t.Fatalf("answer validator status = %q, want failed; intermediate text must not satisfy a terminal-answer validator; output:\n%s", res.Status, res.Output)
	}
}

func TestObserveUnknownTypeIgnored(t *testing.T) {
	m := NewMetrics(nil)
	// A text-shaped part under an unrecognised envelope must not be appended.
	m.ObserveLine([]byte(`{"type":"message.updated","part":{"type":"text","text":"ignored"}}`))
	m.ObserveLine([]byte(`{"type":"something_new","part":{"type":"tool","tool":"read"}}`))
	if len(m.Texts) != 0 || m.ToolCalls != 0 || m.Steps != 0 {
		t.Fatalf("unknown types observed: %+v", m)
	}
}

func TestObserveToolErrorState(t *testing.T) {
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"error","error":"boom"}}}`))
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}`))
	if m.ToolCalls != 2 || m.ToolCallsFailed != 1 {
		t.Fatalf("ToolCalls/Failed = %d/%d, want 2/1", m.ToolCalls, m.ToolCallsFailed)
	}
	if m.ToolCallsByName["bash"] != 2 {
		t.Fatalf("ToolCallsByName = %v", m.ToolCallsByName)
	}
}

func TestObserveSubagentAndSkill(t *testing.T) {
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"task","state":{"status":"completed"}}}`))
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"skill","state":{"status":"completed"}}}`))
	if m.SubagentCalls != 1 {
		t.Errorf("SubagentCalls = %d, want 1", m.SubagentCalls)
	}
	if m.SkillLoads != 1 {
		t.Errorf("SkillLoads = %d, want 1", m.SkillLoads)
	}
}

func TestObserveMCPPrefixRequiresConfiguredServer(t *testing.T) {
	m := NewMetrics([]string{"firecrawl", "gitlab"})
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"firecrawl_search","state":{"status":"completed"}}}`))
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"gitlab_list","state":{"status":"completed"}}}`))
	// Unknown server prefix: not an MCP call.
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"other_search","state":{"status":"completed"}}}`))
	// Bare server name without separator: not an MCP call.
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"firecrawl","state":{"status":"completed"}}}`))
	if m.MCPCalls != 2 {
		t.Fatalf("MCPCalls = %d, want 2", m.MCPCalls)
	}
	if m.MCPCallsByServer["firecrawl"] != 1 || m.MCPCallsByServer["gitlab"] != 1 {
		t.Fatalf("MCPCallsByServer = %v", m.MCPCallsByServer)
	}
	if m.MCPCallsByServer["other"] != 0 {
		t.Fatalf("unconfigured server counted: %v", m.MCPCallsByServer)
	}
}

func TestObserveRetryAndCompaction(t *testing.T) {
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"retry","part":{"type":"retry","attempt":1}}`))
	m.ObserveLine([]byte(`{"type":"compaction","part":{"type":"compaction"}}`))
	if m.Retries != 1 {
		t.Errorf("Retries = %d, want 1", m.Retries)
	}
	if m.Compactions != 1 {
		t.Errorf("Compactions = %d, want 1", m.Compactions)
	}
}

func TestObserveStepFinishSkipsZeroTotal(t *testing.T) {
	m := NewMetrics(nil)
	// Zero-total step (possibly a duplicate) must not accumulate.
	m.ObserveLine([]byte(`{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":0,"input":0,"output":0,"reasoning":0,"cache":{"read":5,"write":6}},"cost":0.25}}`))
	if m.TokensTotal != 0 || m.TokensCacheRead != 0 || m.TokensCacheWrite != 0 {
		t.Fatalf("zero-total tokens accumulated: %+v", m)
	}
	if math.Abs(m.Cost-0.25) > 1e-12 {
		t.Fatalf("Cost = %v, want 0.25", m.Cost)
	}
	m.ObserveLine([]byte(`{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":10,"input":7,"output":2,"reasoning":1,"cache":{"read":3,"write":4}},"cost":0.5}}`))
	if m.TokensTotal != 10 || m.TokensCacheRead != 3 || m.TokensCacheWrite != 4 {
		t.Fatalf("accumulated tokens = %+v", m)
	}
}

func TestObserveMalformedPartCountsParseError(t *testing.T) {
	m := NewMetrics(nil)
	// Valid envelope, undecodable part.
	m.ObserveLine([]byte(`{"type":"tool_use","part":123}`))
	if m.ParseErrors != 1 {
		t.Fatalf("ParseErrors = %d, want 1", m.ParseErrors)
	}
	if m.ToolCalls != 0 {
		t.Fatalf("ToolCalls = %d, want 0", m.ToolCalls)
	}
}

func TestObserveMalformedLineCountsParseErrorNoPanic(t *testing.T) {
	m := NewMetrics(nil)
	for _, line := range []string{
		`{"type":`,
		`not json`,
		``,
		`null`,
		`[]`,
		`{"type":""}`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", line, r)
				}
			}()
			m.ObserveLine([]byte(line))
		}()
	}
	if m.ParseErrors != 6 {
		t.Fatalf("ParseErrors = %d, want 6", m.ParseErrors)
	}
}

func TestMetricsMapNames(t *testing.T) {
	m := NewMetrics([]string{"firecrawl"})
	m.ObserveLine([]byte(`{"type":"step_start","part":{"type":"step-start"}}`))
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"read","state":{"status":"completed"}}}`))
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"firecrawl_search","state":{"status":"completed"}}}`))
	m.ObserveLine([]byte(`{"type":"text","part":{"type":"text","text":"hi"}}`))

	got := m.MetricsMap()
	for _, key := range []string{
		"steps", "tool_calls_total", "tool_calls_failed",
		"tool_calls_read", "subagent_calls", "skill_loads",
		"mcp_calls", "mcp_calls_firecrawl", "retries", "compactions",
		"parse_errors", "tokens_input", "tokens_output", "tokens_reasoning",
		"tokens_cache_read", "tokens_cache_write", "tokens_total", "cost",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("MetricsMap missing %q", key)
		}
	}
	if got["steps"] != 1 || got["tool_calls_total"] != 2 || got["tool_calls_read"] != 1 {
		t.Errorf("counts = %v", got)
	}
	if got["mcp_calls"] != 1 || got["mcp_calls_firecrawl"] != 1 {
		t.Errorf("mcp counts = %v", got)
	}
}

func TestSanitizeMetricSuffix(t *testing.T) {
	cases := map[string]string{
		"read":          "read",
		"my-tool.v2":    "my_tool_v2",
		"server/tool":   "server_tool",
		"Upper_Case123": "Upper_Case123",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	// Sanitised tool name appears in the metrics map.
	m := NewMetrics(nil)
	m.ObserveLine([]byte(`{"type":"tool_use","part":{"type":"tool","tool":"my-tool.v2","state":{"status":"completed"}}}`))
	if m.MetricsMap()["tool_calls_my_tool_v2"] != 1 {
		t.Fatalf("map = %v", m.MetricsMap())
	}
}
