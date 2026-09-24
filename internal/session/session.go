// Package session parses OpenCode session exports and discovers delegated
// child sessions from the `task` tool metadata carried in the event stream.
package session

import (
	"encoding/json"
	"sort"

	"mbl/ocbench/internal/evaluation"
)

// Tokens is the token accounting for a session or a single message. Total is
// taken from the export when present and otherwise computed as
// Input+Output+Reasoning+CacheRead, matching the shape emitted by OpenCode.
type Tokens struct {
	Input, Output, Reasoning, CacheRead, CacheWrite, Total int64
}

// ToolCall is one tool invocation extracted from a message part.
type ToolCall struct {
	Name, Title, Status string
	DurationMS          int64
}

// Message is one exported message with its agent, cost, tokens and tool calls.
// RawParts retains each part's raw JSON so delegated `task` parts can be
// re-discovered from a captured child export.
type Message struct {
	Agent    string
	Cost     float64
	Tokens   Tokens
	Tools    []ToolCall
	RawParts []json.RawMessage
}

// Session is a parsed session export: the session info plus its messages.
type Session struct {
	ID       string
	Agent    string
	Cost     float64
	Tokens   Tokens
	Messages []Message
}

// AgentRollup aggregates message-level usage per agent.
type AgentRollup struct {
	Agent           string
	Messages        int
	ToolCalls       int
	ToolCallsFailed int
	Cost            float64
	Tokens          Tokens
}

// ChildRef identifies a delegated child session found in the event stream.
type ChildRef struct {
	SessionID       string
	ParentSessionID string
	Description     string
}

// rawTokens mirrors the token object in an export. Total is a pointer so an
// absent field is distinguishable from an explicit zero.
type rawTokens struct {
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
	Reasoning int64  `json:"reasoning"`
	Total     *int64 `json:"total"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// clampNonNegative floors a token counter at zero. Exports occasionally carry
// negative deltas; a negative component or total would corrupt downstream sums.
func clampNonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func (r rawTokens) toTokens() Tokens {
	t := Tokens{
		Input:      clampNonNegative(r.Input),
		Output:     clampNonNegative(r.Output),
		Reasoning:  clampNonNegative(r.Reasoning),
		CacheRead:  clampNonNegative(r.Cache.Read),
		CacheWrite: clampNonNegative(r.Cache.Write),
	}
	if r.Total != nil {
		t.Total = clampNonNegative(*r.Total)
	} else {
		t.Total = t.Input + t.Output + t.Reasoning + t.CacheRead
	}
	return t
}

type exportInfo struct {
	ID     string    `json:"id"`
	Agent  string    `json:"agent"`
	Cost   float64   `json:"cost"`
	Tokens rawTokens `json:"tokens"`
}

type exportPart struct {
	Type  string      `json:"type"`
	Tool  string      `json:"tool"`
	State exportState `json:"state"`
}

type exportState struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Time   struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"time"`
}

type exportMessage struct {
	Info  exportInfo        `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

type exportFile struct {
	Info     exportInfo      `json:"info"`
	Messages []exportMessage `json:"messages"`
}

// ParseExport decodes a session export of the form
// {"info":{…},"messages":[…]}. Decoding is tolerant: unknown fields are
// ignored and missing agent/cost/tokens decode to zero values. Only malformed
// JSON is an error.
func ParseExport(data []byte) (*Session, error) {
	var exp exportFile
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, err
	}
	s := &Session{
		ID:     exp.Info.ID,
		Agent:  exp.Info.Agent,
		Cost:   exp.Info.Cost,
		Tokens: exp.Info.Tokens.toTokens(),
	}
	for _, m := range exp.Messages {
		msg := Message{
			Agent:    m.Info.Agent,
			Cost:     m.Info.Cost,
			Tokens:   m.Info.Tokens.toTokens(),
			RawParts: m.Parts,
		}
		for _, raw := range m.Parts {
			var p exportPart
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			if p.Type != "tool" {
				continue
			}
			dur := p.State.Time.End - p.State.Time.Start
			if dur < 0 {
				// A tool still running has a start but no end; never report a
				// negative duration.
				dur = 0
			}
			msg.Tools = append(msg.Tools, ToolCall{
				Name:       p.Tool,
				Title:      p.State.Title,
				Status:     p.State.Status,
				DurationMS: dur,
			})
		}
		s.Messages = append(s.Messages, msg)
	}
	return s, nil
}

// Rollup aggregates message-level usage per agent across the given sessions,
// sorted by agent name. A tool call counts as failed when its status is
// "error", matching the event-stream metrics.
func Rollup(sessions []*Session) []AgentRollup {
	byAgent := make(map[string]*AgentRollup)
	for _, s := range sessions {
		if s == nil {
			continue
		}
		for _, m := range s.Messages {
			r := byAgent[m.Agent]
			if r == nil {
				r = &AgentRollup{Agent: m.Agent}
				byAgent[m.Agent] = r
			}
			r.Messages++
			r.Cost += m.Cost
			r.Tokens = addTokens(r.Tokens, m.Tokens)
			for _, tc := range m.Tools {
				r.ToolCalls++
				if tc.Status == "error" {
					r.ToolCallsFailed++
				}
			}
		}
	}
	out := make([]AgentRollup, 0, len(byAgent))
	for _, r := range byAgent {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

func addTokens(a, b Tokens) Tokens {
	return Tokens{
		Input:      a.Input + b.Input,
		Output:     a.Output + b.Output,
		Reasoning:  a.Reasoning + b.Reasoning,
		CacheRead:  a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite,
		Total:      a.Total + b.Total,
	}
}

// taskState is the subset of a `task` tool part's state used for discovery.
type taskState struct {
	Title    string `json:"title"`
	Metadata struct {
		SessionID       string `json:"sessionId"`
		ParentSessionID string `json:"parentSessionId"`
	} `json:"metadata"`
}

// DiscoverChildren returns the delegated child sessions referenced by `task`
// tool parts, in input order and de-duplicated by session id. Parts with an
// empty session id, a non-task tool, or an undecodable state are skipped.
func DiscoverChildren(events []evaluation.Event) []ChildRef {
	var refs []ChildRef
	seen := make(map[string]bool)
	for _, e := range events {
		p, err := e.PartDecoded()
		if err != nil || p.Tool != "task" || len(p.State) == 0 {
			continue
		}
		var st taskState
		if err := json.Unmarshal(p.State, &st); err != nil {
			continue
		}
		id := st.Metadata.SessionID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, ChildRef{
			SessionID:       id,
			ParentSessionID: st.Metadata.ParentSessionID,
			Description:     st.Title,
		})
	}
	return refs
}
