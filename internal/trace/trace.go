// Package trace turns a run's stored event stream and captured session exports
// into a per-step timeline with subagent spans nested under the `task` call
// that produced them.
package trace

import (
	"encoding/json"

	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/session"
)

// ToolSpan is one tool call in a step. Child is set only for a `task` call and
// carries the delegated session's usage, captured or not.
type ToolSpan struct {
	Tool, Title, Status string
	DurationMS          int64
	Child               *SubagentSpan
}

// Step is one model step: its token accounting, cost, retry/compaction counts
// and tool calls. DurationMS is the span between the step's first and last part
// timestamps, zero when unavailable.
type Step struct {
	Index                int
	Tokens               session.Tokens
	Cost                 float64
	DurationMS           int64
	Retries, Compactions int
	Tools                []ToolSpan
}

// SubagentSpan is a delegated `task` call's child session. An uncaptured child
// has a SessionID but zero Agent, Tokens and Cost.
type SubagentSpan struct {
	SessionID, Agent string
	Tokens           session.Tokens
	Cost             float64
	Tools            []session.ToolCall
}

// Trace is one run's timeline.
type Trace struct {
	RunID, TaskID string
	Steps         []Step
	Subagents     []SubagentSpan
}

// taskState is the subset of a `task` tool part's state used to locate the
// delegated child session.
type taskState struct {
	Metadata struct {
		SessionID string `json:"sessionId"`
	} `json:"metadata"`
}

// toolState is the subset of any tool part's state used for display.
type toolState struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Time   struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"time"`
}

// Build walks events in order and assembles the trace. step_start opens a step,
// tool_use appends a span, step_finish closes the current step with its tokens
// and cost, and retry/compaction increment the current step. Any of the latter
// events seen before the first step_start land on a synthetic step 0. Subagents
// lists the captured child sessions in first-appearance order.
func Build(runID, taskID string, events []evaluation.Event, sessions map[string]*session.Session) Trace {
	tr := Trace{RunID: runID, TaskID: taskID, Steps: []Step{}, Subagents: []SubagentSpan{}}
	seen := make(map[string]bool)

	var times []span
	var cur *Step

	ensure := func(ts int64) *Step {
		if cur == nil {
			tr.Steps = append(tr.Steps, Step{Index: len(tr.Steps), Tools: []ToolSpan{}})
			times = append(times, span{first: ts, last: ts, set: true})
			cur = &tr.Steps[len(tr.Steps)-1]
		}
		return cur
	}
	mark := func(ts int64) {
		i := len(tr.Steps) - 1
		if i < 0 {
			return
		}
		if !times[i].set {
			times[i] = span{first: ts, last: ts, set: true}
			return
		}
		if ts < times[i].first {
			times[i].first = ts
		}
		if ts > times[i].last {
			times[i].last = ts
		}
	}

	for _, e := range events {
		p, err := e.PartDecoded()
		if err != nil {
			continue
		}
		switch e.Type {
		case "step_start":
			tr.Steps = append(tr.Steps, Step{Index: len(tr.Steps), Tools: []ToolSpan{}})
			times = append(times, span{first: e.Timestamp, last: e.Timestamp, set: true})
			cur = &tr.Steps[len(tr.Steps)-1]
		case "tool_use":
			ensure(e.Timestamp)
			mark(e.Timestamp)
			cur.Tools = append(cur.Tools, buildTool(p, sessions, &tr, seen))
		case "step_finish":
			ensure(e.Timestamp)
			mark(e.Timestamp)
			cur.Tokens = toSessionTokens(p.Tokens)
			if p.Cost != nil {
				cur.Cost = *p.Cost
			}
		default:
			switch p.Type {
			case "retry":
				ensure(e.Timestamp)
				mark(e.Timestamp)
				cur.Retries++
			case "compaction":
				ensure(e.Timestamp)
				mark(e.Timestamp)
				cur.Compactions++
			}
		}
	}

	for i := range tr.Steps {
		if times[i].set && times[i].last > times[i].first {
			tr.Steps[i].DurationMS = times[i].last - times[i].first
		}
	}
	return tr
}

// span tracks the first and last timestamp observed for one step.
type span struct {
	first, last int64
	set         bool
}

// buildTool decodes one tool part into a span, attaching the child session for
// a `task` call when its metadata names a captured session.
func buildTool(p evaluation.Part, sessions map[string]*session.Session, tr *Trace, seen map[string]bool) ToolSpan {
	ts := ToolSpan{Tool: p.Tool}
	if len(p.State) == 0 {
		return ts
	}
	var st toolState
	if err := json.Unmarshal(p.State, &st); err != nil {
		return ts
	}
	ts.Title = st.Title
	ts.Status = st.Status
	if st.Time.End > st.Time.Start {
		ts.DurationMS = st.Time.End - st.Time.Start
	}
	if p.Tool != "task" {
		return ts
	}
	var task taskState
	if err := json.Unmarshal(p.State, &task); err != nil {
		return ts
	}
	id := task.Metadata.SessionID
	if id == "" {
		return ts
	}
	child := &SubagentSpan{SessionID: id, Tools: []session.ToolCall{}}
	if s, ok := sessions[id]; ok && s != nil {
		child.Agent = s.Agent
		child.Tokens = s.Tokens
		child.Cost = s.Cost
		child.Tools = flattenTools(s)
		if !seen[id] {
			seen[id] = true
			tr.Subagents = append(tr.Subagents, *child)
		}
	}
	ts.Child = child
	return ts
}

// flattenTools collects every tool call from a session's messages in order.
func flattenTools(s *session.Session) []session.ToolCall {
	out := []session.ToolCall{}
	for _, m := range s.Messages {
		out = append(out, m.Tools...)
	}
	return out
}

// toSessionTokens converts the event-stream token shape into the session shape.
func toSessionTokens(t *evaluation.Tokens) session.Tokens {
	if t == nil {
		return session.Tokens{}
	}
	return session.Tokens{
		Input:      t.Input,
		Output:     t.Output,
		Reasoning:  t.Reasoning,
		CacheRead:  t.Cache.Read,
		CacheWrite: t.Cache.Write,
		Total:      t.Total,
	}
}
