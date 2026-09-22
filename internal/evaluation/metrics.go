package evaluation

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Metrics is the normalised view of a single OpenCode event stream.
type Metrics struct {
	Steps, ToolCalls, ToolCallsFailed, SubagentCalls, SkillLoads int
	MCPCalls, Retries, Compactions, ParseErrors                  int
	ToolCallsByName, MCPCallsByServer                            map[string]int
	TokensInput, TokensOutput, TokensReasoning                   int64
	TokensCacheRead, TokensCacheWrite, TokensTotal               int64
	Cost                                                         float64
	Texts                                                        []string
	FinalAnswer                                                  string // text parts joined by "\n"

	mcpServers []string
}

// NewMetrics returns initialised Metrics. mcpServers lists the configured MCP
// server names used to recognise `<server>_<tool>` tool names; matching is
// order-independent but deterministic per configured server.
func NewMetrics(mcpServers []string) *Metrics {
	m := &Metrics{
		ToolCallsByName:  make(map[string]int),
		MCPCallsByServer: make(map[string]int),
		mcpServers:       append([]string(nil), mcpServers...),
	}
	for _, s := range mcpServers {
		m.MCPCallsByServer[s] = 0
	}
	return m
}

// toolState is the subset of a tool part's state used for metrics.
type toolState struct {
	Status string `json:"status"`
}

// Observe folds a single event into the metrics. It never returns an error:
// undecodable parts increment ParseErrors. Unknown envelope and part types are
// ignored for forward compatibility.
func (m *Metrics) Observe(e Event) {
	p, err := e.PartDecoded()
	if err != nil {
		m.ParseErrors++
		return
	}
	switch e.Type {
	case "step_start":
		m.Steps++
	case "tool_use":
		m.observeTool(p)
	case "step_finish":
		m.observeStepFinish(p)
	case "text":
		m.observeText(p)
	}
	switch p.Type {
	case "retry":
		m.Retries++
	case "compaction":
		m.Compactions++
	}
}

// ObserveLine parses a raw JSONL line and observes it. Malformed envelope lines
// increment ParseErrors instead of returning an error; it never panics.
func (m *Metrics) ObserveLine(line []byte) {
	e, err := ParseLine(line)
	if err != nil {
		m.ParseErrors++
		return
	}
	m.Observe(e)
}

func (m *Metrics) observeTool(p Part) {
	m.ToolCalls++
	m.ToolCallsByName[p.Tool]++
	if len(p.State) > 0 {
		var st toolState
		if err := json.Unmarshal(p.State, &st); err == nil && st.Status == "error" {
			m.ToolCallsFailed++
		}
	}
	switch p.Tool {
	case "task":
		m.SubagentCalls++
	case "skill":
		m.SkillLoads++
	}
	for _, server := range m.mcpServers {
		if strings.HasPrefix(p.Tool, server+"_") {
			m.MCPCalls++
			m.MCPCallsByServer[server]++
			break
		}
	}
}

func (m *Metrics) observeStepFinish(p Part) {
	if p.Cost != nil {
		m.Cost += *p.Cost
	}
	if p.Tokens == nil || p.Tokens.Total == 0 {
		return
	}
	m.TokensInput += p.Tokens.Input
	m.TokensOutput += p.Tokens.Output
	m.TokensReasoning += p.Tokens.Reasoning
	m.TokensCacheRead += p.Tokens.Cache.Read
	m.TokensCacheWrite += p.Tokens.Cache.Write
	m.TokensTotal += p.Tokens.Total
}

func (m *Metrics) observeText(p Part) {
	m.Texts = append(m.Texts, p.Text)
	m.FinalAnswer = strings.Join(m.Texts, "\n")
}

// MetricsMap renders the metrics with the spec §9 names and float64 values.
// Per-tool and per-server counters are suffixed with a sanitised name.
func (m *Metrics) MetricsMap() map[string]float64 {
	out := map[string]float64{
		"steps":              float64(m.Steps),
		"tool_calls_total":   float64(m.ToolCalls),
		"tool_calls_failed":  float64(m.ToolCallsFailed),
		"subagent_calls":     float64(m.SubagentCalls),
		"skill_loads":        float64(m.SkillLoads),
		"mcp_calls":          float64(m.MCPCalls),
		"retries":            float64(m.Retries),
		"compactions":        float64(m.Compactions),
		"parse_errors":       float64(m.ParseErrors),
		"tokens_input":       float64(m.TokensInput),
		"tokens_output":      float64(m.TokensOutput),
		"tokens_reasoning":   float64(m.TokensReasoning),
		"tokens_cache_read":  float64(m.TokensCacheRead),
		"tokens_cache_write": float64(m.TokensCacheWrite),
		"tokens_total":       float64(m.TokensTotal),
		"cost":               m.Cost,
	}
	for tool, n := range m.ToolCallsByName {
		out["tool_calls_"+sanitize(tool)] = float64(n)
	}
	for server, n := range m.MCPCallsByServer {
		out["mcp_calls_"+sanitize(server)] = float64(n)
	}
	return out
}

// sanitize replaces every non-alphanumeric rune with an underscore so a metric
// name is a stable key.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, s)
}
