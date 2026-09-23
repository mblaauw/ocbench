package experiment

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"

	"mbl/ocbench/internal/canon"
	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/runner"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
)

// ArmSpec is one labelled config-overlay variant of an experiment.
type ArmSpec struct {
	Label   string
	Overlay Overlay
}

// Step is one scheduled execution: a task, an arm and the repeat index.
type Step struct {
	Task        *suite.Task
	Arm         ArmSpec
	RepeatIndex int
}

// Request describes one interleaved A/B experiment.
type Request struct {
	Suite    *suite.Suite
	Tasks    []*suite.Task
	Arms     []ArmSpec
	Baseline string
	Paths    config.Paths
	Profile  profile.Options
	// AdapterFor builds the OpenCode adapter for one arm. The caller gives it
	// the arm's overlay environment (append(os.Environ(), arm.Overlay.Env...))
	// so profile discovery sees the overlay. Run never mutates the process
	// environment; each arm's adapter is used for its discovery and its runs.
	AdapterFor   func(arm ArmSpec) opencode.Adapter
	EnvPolicy    runner.EnvPolicy
	Repeat       int
	KeepWorktree bool
}

// Outcome is the result of Run: the created experiment and every run id in
// execution order.
type Outcome struct {
	ExperimentID string
	RunIDs       []string
}

// ParseArm parses an arm spec of the form "label=path" and resolves the path
// into an Overlay. A bare "label" or "label=" is the no-overlay arm (spec
// §12.1's "none" kind, the unmodified resolved profile). An empty label is an
// error. Duplicate labels across arms are the caller's validation.
func ParseArm(spec string) (ArmSpec, error) {
	label, path, _ := strings.Cut(spec, "=")
	if label == "" {
		return ArmSpec{}, fmt.Errorf("arm %q: empty label", spec)
	}
	if path == "" {
		return ArmSpec{Label: label, Overlay: Overlay{Kind: OverlayNone}}, nil
	}
	overlay, err := ResolveOverlay(path)
	if err != nil {
		return ArmSpec{}, err
	}
	return ArmSpec{Label: label, Overlay: overlay}, nil
}

// BuildPlan returns the spec §12.2 execution order: task-major, with arms
// alternating inside each repeat, so t1/A/0, t1/B/0, t1/A/1, t1/B/1, t2/A/0, …
func BuildPlan(tasks []*suite.Task, arms []ArmSpec, repeat int) []Step {
	plan := make([]Step, 0, len(tasks)*len(arms)*repeat)
	for _, task := range tasks {
		for r := 0; r < repeat; r++ {
			for _, arm := range arms {
				plan = append(plan, Step{Task: task, Arm: arm, RepeatIndex: r})
			}
		}
	}
	return plan
}

// Run persists the experiment and one arm row per arm before executing any
// step, then runs the interleaved plan. It returns an Outcome carrying the
// experiment id and the run ids in execution order; it never renders or prints.
// A Baseline that names no arm is rejected (an empty Baseline defaults to the
// first arm, per spec §12.1).
func Run(ctx context.Context, st *store.Store, req Request) (Outcome, error) {
	if st == nil {
		return Outcome{}, fmt.Errorf("experiment: nil store")
	}
	if req.Suite == nil {
		return Outcome{}, fmt.Errorf("experiment: suite is required")
	}
	tasks := req.Tasks
	if len(tasks) == 0 {
		tasks = req.Suite.Tasks
	}
	if len(tasks) == 0 {
		return Outcome{}, fmt.Errorf("experiment: no tasks to run")
	}
	if len(req.Arms) < 2 {
		return Outcome{}, fmt.Errorf("experiment: at least two arms are required")
	}
	if req.AdapterFor == nil {
		return Outcome{}, fmt.Errorf("experiment: AdapterFor is required")
	}

	baseline := req.Baseline
	if baseline == "" {
		baseline = req.Arms[0].Label
	}
	if !hasArm(req.Arms, baseline) {
		return Outcome{}, fmt.Errorf("experiment: baseline %q is not one of the arms", req.Baseline)
	}

	repeat := req.Repeat
	if repeat < 1 {
		repeat = 1
	}

	expID, err := newExperimentID()
	if err != nil {
		return Outcome{}, err
	}
	created := time.Now().UTC().Format(time.RFC3339)

	spec, err := canon.JSON(map[string]any{
		"suite":         req.Suite.Name,
		"suite_version": req.Suite.Version,
		"suite_hash":    req.Suite.Hash,
		"tasks":         taskIDs(tasks),
		"arms":          armSpecs(req.Arms),
		"baseline":      baseline,
		"repeat":        repeat,
	})
	if err != nil {
		return Outcome{}, err
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{
		ID:        expID,
		Name:      fmt.Sprintf("experiment %s@%s %s", req.Suite.Name, req.Suite.Version, created),
		SpecJSON:  string(spec),
		CreatedAt: created,
	}); err != nil {
		return Outcome{}, err
	}

	// Resolve, fingerprint and persist every arm before the first step runs, so
	// the arm rows exist when the runs reference them.
	profiles := make(map[string]*profile.Profile, len(req.Arms))
	armIDs := make(map[string]string, len(req.Arms))
	adapters := make(map[string]opencode.Adapter, len(req.Arms))
	for _, arm := range req.Arms {
		adapter := req.AdapterFor(arm)
		if adapter == nil {
			return Outcome{ExperimentID: expID}, fmt.Errorf("experiment: adapter for arm %q is nil", arm.Label)
		}
		adapters[arm.Label] = adapter

		opts := req.Profile
		if opts.Dir == "" {
			dir, err := os.Getwd()
			if err != nil {
				return Outcome{ExperimentID: expID}, fmt.Errorf("experiment: resolve working directory: %w", err)
			}
			opts.Dir = dir
		}
		sources, err := profile.Discover(ctx, adapter, opts.Dir)
		if err != nil {
			return Outcome{ExperimentID: expID}, fmt.Errorf("experiment arm %s: %w", arm.Label, err)
		}
		p, err := profile.Fingerprint(sources, opts)
		if err != nil {
			return Outcome{ExperimentID: expID}, fmt.Errorf("experiment arm %s: %w", arm.Label, err)
		}
		if _, err := profile.Persist(ctx, st, req.Paths, p); err != nil {
			return Outcome{ExperimentID: expID}, fmt.Errorf("experiment arm %s: %w", arm.Label, err)
		}
		profiles[arm.Label] = p

		armID, err := newExperimentID()
		if err != nil {
			return Outcome{ExperimentID: expID}, err
		}
		armIDs[arm.Label] = armID
		if err := st.InsertExperimentArm(ctx, store.ExperimentArmRow{
			ID:            armID,
			ExperimentID:  expID,
			Label:         arm.Label,
			ProfileID:     optionalString(p.ID),
			ProfileHash:   p.Hash,
			OverlayKind:   string(arm.Overlay.Kind),
			OverlayPath:   optionalString(arm.Overlay.Path),
			OverlaySHA256: optionalString(arm.Overlay.SHA256),
			CreatedAt:     created,
		}); err != nil {
			return Outcome{ExperimentID: expID}, err
		}
	}

	plan := BuildPlan(tasks, req.Arms, repeat)
	runIDs := make([]string, 0, len(plan))
	for _, step := range plan {
		p := profiles[step.Arm.Label]
		res, err := runner.Run(ctx, adapters[step.Arm.Label], st, runner.Request{
			Suite:        req.Suite,
			Task:         step.Task,
			Profile:      p,
			Paths:        req.Paths,
			EnvPolicy:    req.EnvPolicy,
			Agent:        req.Profile.Agent,
			Model:        req.Profile.Model,
			Variant:      req.Profile.Variant,
			Auto:         req.Profile.Auto,
			Pure:         req.Profile.Pure,
			KeepWorktree: req.KeepWorktree,
			ExperimentID: expID,
			ArmID:        armIDs[step.Arm.Label],
			RepeatIndex:  step.RepeatIndex,
			MCPTools:     mcpToolNames(p),
			ExtraEnv:     step.Arm.Overlay.Env,
		})
		if err != nil {
			return Outcome{ExperimentID: expID, RunIDs: runIDs}, err
		}
		runIDs = append(runIDs, res.RunID)
	}
	return Outcome{ExperimentID: expID, RunIDs: runIDs}, nil
}

// hasArm reports whether label names one of the arms.
func hasArm(arms []ArmSpec, label string) bool {
	for _, a := range arms {
		if a.Label == label {
			return true
		}
	}
	return false
}

// taskIDs returns the task ids in plan order for the experiment spec.
func taskIDs(tasks []*suite.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// armSpecs encodes the arms for the experiment spec JSON.
func armSpecs(arms []ArmSpec) []map[string]any {
	out := make([]map[string]any, 0, len(arms))
	for _, a := range arms {
		out = append(out, map[string]any{
			"label":          a.Label,
			"overlay_kind":   string(a.Overlay.Kind),
			"overlay_path":   a.Overlay.Path,
			"overlay_sha256": a.Overlay.SHA256,
		})
	}
	return out
}

// mcpToolNames lists the configured MCP server names from the arm's profile so
// the runner can classify `<server>_<tool>` calls.
func mcpToolNames(p *profile.Profile) []string {
	var out []string
	for _, c := range p.Components {
		if c.Kind == "mcp" {
			out = append(out, c.Name)
		}
	}
	return out
}

// optionalString returns nil for the empty string so an unset optional column
// is stored as SQL NULL.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// newExperimentID returns a random RFC 4122 version 4 identifier formatted as
// 8-4-4-4-12 hex, mirroring the profile and runner generators.
func newExperimentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate experiment id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
