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
