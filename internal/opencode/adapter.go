// Package opencode is the CLI adapter for OpenCode. All observation of the
// installed tool goes through its command line; nothing reads OpenCode's
// internal database or configuration files directly. It deliberately does not
// import internal/config or internal/canon so that it stays a pure adapter.
package opencode

import (
	"context"
	"encoding/json"
	"time"
)

// Adapter is the read-only observation surface over the OpenCode CLI. It is an
// interface so tests and CI never touch inference.
type Adapter interface {
	Version(ctx context.Context) (string, error)
	ResolvedConfig(ctx context.Context, dir string) ([]byte, error)
	Skills(ctx context.Context, dir string) ([]SkillInfo, error)
	Agent(ctx context.Context, dir, name string) (AgentInfo, error)
	MCPStatus(ctx context.Context, dir string) ([]MCPStatus, error)
}

// SkillInfo is one entry of `opencode debug skill`.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Content     string `json:"content"`
}

// AgentInfo is the resolved detail of `opencode debug agent <name>`. Raw keeps
// the undecoded capture for lossless retention.
type AgentInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mode        string          `json:"mode"`
	Native      bool            `json:"native"`
	Model       string          `json:"model"`
	Variant     string          `json:"variant"`
	Steps       *int            `json:"steps"`
	Temperature *float64        `json:"temperature"`
	Tools       json.RawMessage `json:"tools"`
	Options     json.RawMessage `json:"options"`
	Permission  json.RawMessage `json:"permission"`
	Prompt      json.RawMessage `json:"prompt"`
	Raw         json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes `opencode debug agent` output. Current OpenCode
// releases emit the resolved model as an object ({"providerID","modelID"})
// rather than the "provider/model" string the test helper serves; both are
// accepted and normalised to "provider/model" so the profile component matches
// the resolved-config encoding.
func (a *AgentInfo) UnmarshalJSON(b []byte) error {
	type wire struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Mode        string          `json:"mode"`
		Native      bool            `json:"native"`
		Model       json.RawMessage `json:"model"`
		Variant     string          `json:"variant"`
		Steps       *int            `json:"steps"`
		Temperature *float64        `json:"temperature"`
		Tools       json.RawMessage `json:"tools"`
		Options     json.RawMessage `json:"options"`
		Permission  json.RawMessage `json:"permission"`
		Prompt      json.RawMessage `json:"prompt"`
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*a = AgentInfo{
		Name:        w.Name,
		Description: w.Description,
		Mode:        w.Mode,
		Native:      w.Native,
		Model:       modelString(w.Model),
		Variant:     w.Variant,
		Steps:       w.Steps,
		Temperature: w.Temperature,
		Tools:       w.Tools,
		Options:     w.Options,
		Permission:  w.Permission,
		Prompt:      w.Prompt,
	}
	return nil
}

// modelString normalises the two model encodings to "provider/model".
func modelString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	switch {
	case obj.ProviderID != "" && obj.ModelID != "":
		return obj.ProviderID + "/" + obj.ModelID
	case obj.ModelID != "":
		return obj.ModelID
	default:
		return obj.ProviderID
	}
}

// MCPStatus is one server parsed from `opencode mcp list`.
type MCPStatus struct {
	Name    string          `json:"name"`
	Enabled bool            `json:"enabled"`
	Type    string          `json:"type"`
	Target  string          `json:"target"`
	Raw     json.RawMessage `json:"-"`
}

// Options configures a Real adapter.
type Options struct {
	Bin        string
	Timeout    time.Duration
	Env        []string // nil means os.Environ()
	TestPrefix []string // test-only: argv inserted before the opencode args
}
