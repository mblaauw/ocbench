// Package evaluation normalises OpenCode JSONL event streams and validator
// output into comparable run metrics.
package evaluation

import (
	"encoding/json"
	"errors"
)

// Event is the JSONL envelope emitted by `opencode run --format json`:
// {"type": "<snake_case>", "timestamp": <unix-ms>, "sessionID": "ses_…", "part": {…}}.
type Event struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	SessionID string          `json:"sessionID"`
	Part      json.RawMessage `json:"part"`
}

// Part is the decoded payload carried by an Event. The SDK uses kebab-case
// part types (step-start, tool, step-finish, text, retry, compaction).
type Part struct {
	Type    string          `json:"type"`
	Tool    string          `json:"tool"`
	CallID  string          `json:"callID"`
	State   json.RawMessage `json:"state"`
	Text    string          `json:"text"`
	Reason  string          `json:"reason"`
	Tokens  *Tokens         `json:"tokens"`
	Cost    *float64        `json:"cost"`
	Attempt *int            `json:"attempt"`
}

// Tokens mirrors the token accounting carried by a step-finish part.
type Tokens struct {
	Total, Input, Output, Reasoning int64
	Cache                           struct{ Read, Write int64 } `json:"cache"`
}

// errEmptyType is returned for an envelope without a type.
var errEmptyType = errors.New("evaluation: event missing type")

// ParseLine decodes a single JSONL envelope line and validates that it carries
// a non-empty type. Unknown types are not rejected; normalisation ignores them.
func ParseLine(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, err
	}
	if e.Type == "" {
		return Event{}, errEmptyType
	}
	return e, nil
}

// PartDecoded decodes the event's raw part payload. An absent part yields the
// zero Part; a present but undecodable part yields an error.
func (e Event) PartDecoded() (Part, error) {
	var p Part
	if len(e.Part) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(e.Part, &p); err != nil {
		return Part{}, err
	}
	return p, nil
}
