package history

import (
	"sort"
	"strings"
)

// AgentUsage is one agent's recorded usage within a run, read from the
// agent-scoped metrics the runner writes (agent.<name>.<metric>).
type AgentUsage struct {
	Name      string
	Messages  int
	ToolCalls int
	Tokens    float64
	Cost      float64
}

// agentMetricPrefix marks the agent-scoped metric names.
const agentMetricPrefix = "agent."

// AgentUsages extracts per-agent usage from a run's metrics, sorted by tokens
// descending so the heaviest agent reads first.
func AgentUsages(metrics map[string]float64) []AgentUsage {
	byName := map[string]*AgentUsage{}
	for name, value := range metrics {
		if !strings.HasPrefix(name, agentMetricPrefix) {
			continue
		}
		rest := strings.TrimPrefix(name, agentMetricPrefix)
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		agent, metric := rest[:dot], rest[dot+1:]
		usage := byName[agent]
		if usage == nil {
			usage = &AgentUsage{Name: agent}
			byName[agent] = usage
		}
		switch metric {
		case "messages":
			usage.Messages = int(value)
		case "tool_calls":
			usage.ToolCalls = int(value)
		case "tokens_total":
			usage.Tokens = value
		case "cost":
			usage.Cost = value
		}
	}

	out := make([]AgentUsage, 0, len(byName))
	for _, u := range byName {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Name < out[j].Name
	})
	return out
}
