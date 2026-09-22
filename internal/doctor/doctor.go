// Package doctor runs the environment readiness checks behind `ocbench doctor`.
// Every diagnosable problem is reported as a Check inside the returned Report;
// a non-nil error is reserved for programming errors (for example a nil
// adapter), so callers can tell an unhealthy environment apart from a broken
// doctor.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/version"
)

// Status is the outcome of a single check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one named diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Report is the complete doctor result.
type Report struct {
	Checks          []Check `json:"checks"`
	ProfileHash     string  `json:"profile_hash,omitempty"`
	OpenCodeVersion string  `json:"opencode_version,omitempty"`
	AgentCount      int     `json:"agent_count"`
	SkillCount      int     `json:"skill_count"`
	EnabledMCPs     int     `json:"enabled_mcps"`
}

// Healthy reports whether no check failed. Warnings are healthy: they flag
// degraded or optional facilities without blocking benchmark runs.
func (r Report) Healthy() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// Run executes every check against the supplied adapter, paths and config. The
// discovery directory is the process working directory, falling back to the
// ocbench data home if that cannot be resolved.
func Run(ctx context.Context, a opencode.Adapter, paths config.Paths, cfg config.Config) (Report, error) {
	if a == nil {
		return Report{}, errors.New("doctor: nil adapter")
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = paths.Home
	}

	report := Report{}
	report.Checks = append(report.Checks,
		checkOpenCode(ctx, a, cfg),
		checkGit(),
		checkDB(ctx, paths),
		checkConfig(paths),
	)

	profileCheck, sources := checkProfile(ctx, a, dir, &report)
	report.Checks = append(report.Checks, profileCheck)
	if sources != nil {
		report.Checks = append(report.Checks,
			countCheck("agents", len(sources.Agents), "agents discovered"),
			countCheck("skills", len(sources.Skills), "skills discovered"),
		)
	}

	mcpCheck, enabled := checkMCP(ctx, a, dir, sources)
	report.EnabledMCPs = enabled
	report.Checks = append(report.Checks, mcpCheck, checkSandbox(cfg))

	return report, nil
}

// checkOpenCode verifies that the configured binary resolves and reports a
// parseable version.
func checkOpenCode(ctx context.Context, a opencode.Adapter, cfg config.Config) Check {
	bin := cfg.OpenCodeBin
	if bin == "" {
		bin = "opencode"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Check{Name: "opencode", Status: StatusFail, Detail: fmt.Sprintf("binary %q not found: %v", bin, err)}
	}
	v, err := a.Version(ctx)
	if err != nil {
		return Check{Name: "opencode", Status: StatusFail, Detail: fmt.Sprintf("version check failed: %v", err)}
	}
	return Check{Name: "opencode", Status: StatusOK, Detail: fmt.Sprintf("%s (%s)", v, path)}
}

// checkGit warns when git is missing because benchmark runs need worktrees.
func checkGit() Check {
	path, err := exec.LookPath("git")
	if err != nil {
		return Check{Name: "git", Status: StatusWarn, Detail: "git not found (benchmark runs need it for worktrees)"}
	}
	return Check{Name: "git", Status: StatusOK, Detail: path}
}

// checkDB creates the data directories, opens the SQLite database and applies
// any pending migrations, closing the store before returning.
func checkDB(ctx context.Context, paths config.Paths) Check {
	if err := config.EnsureDirs(paths); err != nil {
		return Check{Name: "db", Status: StatusFail, Detail: "create data dirs: " + err.Error()}
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		return Check{Name: "db", Status: StatusFail, Detail: err.Error()}
	}
	schema, migrateErr := st.Migrate(ctx)
	closeErr := st.Close()
	if migrateErr != nil {
		return Check{Name: "db", Status: StatusFail, Detail: "migrate: " + migrateErr.Error()}
	}
	if closeErr != nil {
		return Check{Name: "db", Status: StatusFail, Detail: "close: " + closeErr.Error()}
	}
	return Check{Name: "db", Status: StatusOK, Detail: fmt.Sprintf("%s (schema version %d)", paths.DB, schema)}
}

// checkConfig parses the config file. A missing file is fine; it means
// defaults apply. Load is called directly so the check is honest and testable
// in isolation from whatever the CLI layer already loaded.
func checkConfig(paths config.Paths) Check {
	if _, err := config.Load(paths); err != nil {
		return Check{Name: "config", Status: StatusFail, Detail: err.Error()}
	}
	detail := paths.ConfigFile
	if _, err := os.Stat(paths.ConfigFile); errors.Is(err, os.ErrNotExist) {
		detail += " (missing, defaults apply)"
	}
	return Check{Name: "config", Status: StatusOK, Detail: detail}
}

// checkProfile discovers and fingerprints the resolved execution profile. It
// records the profile identity and counts on the report and returns the raw
// sources (nil when discovery failed) so the MCP fallback can use them.
func checkProfile(ctx context.Context, a opencode.Adapter, dir string, report *Report) (Check, *profile.Sources) {
	sources, err := profile.Discover(ctx, a, dir)
	if err != nil {
		return Check{Name: "profile", Status: StatusFail, Detail: err.Error()}, nil
	}
	p, err := profile.Fingerprint(sources, profile.Options{Dir: dir})
	if err != nil {
		return Check{Name: "profile", Status: StatusFail, Detail: err.Error()}, sources
	}
	report.ProfileHash = p.Hash
	report.OpenCodeVersion = p.OpenCodeVersion
	report.AgentCount = len(sources.Agents)
	report.SkillCount = len(sources.Skills)
	return Check{Name: "profile", Status: StatusOK, Detail: "profile " + p.Hash}, sources
}

// countCheck turns a discovered count into an ok/warn check.
func countCheck(name string, n int, noun string) Check {
	if n == 0 {
		return Check{Name: name, Status: StatusWarn, Detail: "0 " + noun}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%d %s", n, noun)}
}

// checkMCP reports enabled MCP servers. A failure of `opencode mcp list` is a
// warning, never a failure of the whole report, and falls back to counting
// `enabled: true` entries in the already-discovered resolved config.
func checkMCP(ctx context.Context, a opencode.Adapter, dir string, sources *profile.Sources) (Check, int) {
	servers, err := a.MCPStatus(ctx, dir)
	if err != nil {
		return Check{
			Name:   "mcp",
			Status: StatusWarn,
			Detail: fmt.Sprintf("mcp status unavailable: %v (counted %d from resolved config)", err, countEnabledMCPs(sources)),
		}, countEnabledMCPs(sources)
	}
	enabled := 0
	for _, s := range servers {
		if s.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return Check{Name: "mcp", Status: StatusWarn, Detail: fmt.Sprintf("0 of %d servers enabled", len(servers))}, 0
	}
	return Check{Name: "mcp", Status: StatusOK, Detail: fmt.Sprintf("%d of %d servers enabled", enabled, len(servers))}, enabled
}

// countEnabledMCPs counts `enabled: true` servers in the resolved config.
func countEnabledMCPs(sources *profile.Sources) int {
	if sources == nil || len(sources.ResolvedConfig) == 0 {
		return 0
	}
	var doc struct {
		MCP map[string]map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(sources.ResolvedConfig, &doc); err != nil {
		return 0
	}
	n := 0
	for _, server := range doc.MCP {
		if enabled, ok := server["enabled"].(bool); ok && enabled {
			n++
		}
	}
	return n
}

// checkSandbox summarises the environment sandbox mode. It is informational.
func checkSandbox(cfg config.Config) Check {
	mode := "default"
	if cfg.Sandbox.InheritEnvironment {
		mode = "inherit"
	}
	return Check{
		Name:   "sandbox",
		Status: StatusOK,
		Detail: fmt.Sprintf("mode=%s pass_env=%d ocbench=%s", mode, len(cfg.Sandbox.PassEnv), version.Info().Version),
	}
}
