// Package profile discovers, canonicalises, redacts, hashes, persists and
// diffs the resolved OpenCode execution profile described in spec section 5.
//
// Discovery is the only layer that talks to the OpenCode adapter and the
// filesystem for capture material. Fingerprint turns a Sources plus Options
// into an immutable, content-addressed Profile. The profile hash is a pure
// function of the canonical, redacted, path-normalised structure, so identical
// environments resolve to identical hashes regardless of map/slice ordering.
package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"mbl/ocbench/internal/opencode"
)

// Sources is the raw observation set that feeds Fingerprint. It is produced by
// Discover and is deliberately decoupled from any IO so tests can construct it
// directly.
type Sources struct {
	OpenCodeVersion  string
	ResolvedConfig   []byte
	Skills           []opencode.SkillInfo
	Agents           []opencode.AgentInfo
	Instructions     map[string][]byte // scope → content, key like "global:AGENTS.md"
	InstructionPaths map[string]string // scope → absolute path, parallel to Instructions
	Dir              string
	Home             string // used for path normalisation; tests set it explicitly
}

// Options captures the run-time selections that are not part of the resolved
// config but do change agent behaviour.
type Options struct {
	Dir         string
	Agent       string
	Model       string
	Variant     string
	SandboxMode string // "default" or "inherit"; empty defaults to "default"
	Auto        bool
	Pure        bool
	EnvNames    []string
}

// Component is one disjoint slice of the profile, keyed by (Kind, Name).
// Singletons use Name == Kind (for example primary/primary).
type Component struct {
	Kind          string
	Name          string
	Hash          string
	CanonicalJSON []byte
}

// Captures carries the raw material for the per-profile capture files that
// Persist writes. Fingerprint fills it; it is intentionally excluded from the
// profile hash and from the database.
type Captures struct {
	ResolvedConfig []byte // resolved-config.json (redacted, normalised)
	Skills         []byte // skills.json (metadata, normalised location, raw content, hashes)
	Agents         []byte // agents.json (redacted, normalised)
	Instructions   []byte // instructions.json (scope → raw text)
}

// Profile is the immutable, content-addressed result of fingerprinting.
type Profile struct {
	ID              string
	Hash            string
	OpenCodeVersion string
	CanonicalJSON   []byte
	Components      []Component
	Snapshot        map[string]any // full canonical structure, redacted + normalised
	Captures        Captures
}

// Change is one component-level difference between two profiles.
type Change struct {
	Kind     string
	Name     string
	Change   string
	FromHash string
	ToHash   string
}

// Discover performs all adapter and filesystem IO for a profile snapshot. The
// home directory comes from os.UserHomeDir, and instructions are read from the
// global OpenCode config directory plus the nearest AGENTS.md ancestor of dir.
func Discover(ctx context.Context, a opencode.Adapter, dir string) (*Sources, error) {
	version, err := a.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover opencode version: %w", err)
	}
	raw, err := a.ResolvedConfig(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("discover resolved config: %w", err)
	}
	skills, err := a.Skills(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("discover skills: %w", err)
	}
	var cfg map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("decode resolved config: %w", err)
		}
	}
	names := agentNames(cfg)
	agents := make([]opencode.AgentInfo, 0, len(names))
	for _, name := range names {
		info, err := a.Agent(ctx, dir, name)
		if err != nil {
			return nil, fmt.Errorf("discover agent %s: %w", name, err)
		}
		agents = append(agents, info)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	instructions, instructionPaths, err := readInstructions(home, dir)
	if err != nil {
		return nil, err
	}
	return &Sources{
		OpenCodeVersion:  version,
		ResolvedConfig:   raw,
		Skills:           skills,
		Agents:           agents,
		Instructions:     instructions,
		InstructionPaths: instructionPaths,
		Dir:              dir,
		Home:             home,
	}, nil
}

// agentNames returns the sorted agent names declared in a resolved config.
func agentNames(cfg map[string]any) []string {
	agents, ok := cfg["agent"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
