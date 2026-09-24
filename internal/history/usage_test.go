package history_test

import (
	"reflect"
	"testing"

	"mbl/ocbench/internal/history"
)

func TestAgentUsagesExtractsAndSorts(t *testing.T) {
	metrics := map[string]float64{
		"agent.build.messages":       7,
		"agent.build.tool_calls":     3,
		"agent.build.tokens_total":   24434,
		"agent.build.cost":           0.0048,
		"agent.explore.messages":     4,
		"agent.explore.tool_calls":   2,
		"agent.explore.tokens_total": 37606,
		"agent.explore.cost":         0.0039,
		"tokens_total":               62040, // run-scoped, must be ignored
		"agent.build.tokens_input":   12641, // not a field we report
	}
	got := history.AgentUsages(metrics)
	if len(got) != 2 {
		t.Fatalf("usages = %+v, want 2", got)
	}
	// Heaviest first.
	if got[0].Name != "explore" || got[1].Name != "build" {
		t.Errorf("order = %s, %s", got[0].Name, got[1].Name)
	}
	want := history.AgentUsage{Name: "explore", Messages: 4, ToolCalls: 2, Tokens: 37606, Cost: 0.0039}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("explore = %+v, want %+v", got[0], want)
	}
	if got := history.AgentUsages(nil); len(got) != 0 {
		t.Errorf("nil metrics produced %+v", got)
	}
}
