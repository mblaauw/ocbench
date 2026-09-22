package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/suite"
)

// planFile is the resolved plan written for --dry-run.
type planFile struct {
	RunID        string          `json:"run_id"`
	ProfileID    string          `json:"profile_id"`
	ProfileHash  string          `json:"profile_hash"`
	SuiteName    string          `json:"suite_name"`
	SuiteVersion string          `json:"suite_version"`
	SuiteHash    string          `json:"suite_hash"`
	TaskID       string          `json:"task_id"`
	TaskVersion  string          `json:"task_version"`
	FixtureSHA   string          `json:"fixture_sha"`
	Worktree     string          `json:"worktree"`
	EnvNames     []string        `json:"env_names"`
	Agent        string          `json:"agent,omitempty"`
	Model        string          `json:"model,omitempty"`
	Variant      string          `json:"variant,omitempty"`
	Auto         bool            `json:"auto"`
	Pure         bool            `json:"pure"`
	MCPTools     []string        `json:"mcp_tools,omitempty"`
	Validators   []validatorPlan `json:"validators"`
}

// validatorPlan is one validator as recorded in plan.json.
type validatorPlan struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Command  []string `json:"command,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
	Mode     string   `json:"mode,omitempty"`
}

// validationArtifact is one validator result as recorded in result.json. The
// full output lives in the referenced log file; Excerpt is the bounded prefix.
type validationArtifact struct {
	Seq        int    `json:"seq"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	OutputPath string `json:"output_path"`
	Excerpt    string `json:"excerpt,omitempty"`
}

// resultFile is the aggregate result.json payload: every Result field plus the
// resolved inputs and artifact paths.
type resultFile struct {
	RunID           string               `json:"run_id"`
	Status          string               `json:"status"`
	TaskID          string               `json:"task_id"`
	TaskVersion     string               `json:"task_version"`
	SessionID       string               `json:"session_id,omitempty"`
	ProfileID       string               `json:"profile_id"`
	ProfileHash     string               `json:"profile_hash"`
	SuiteName       string               `json:"suite_name"`
	SuiteVersion    string               `json:"suite_version"`
	SuiteHash       string               `json:"suite_hash"`
	FixtureSHA      string               `json:"fixture_sha"`
	Agent           string               `json:"agent,omitempty"`
	Model           string               `json:"model,omitempty"`
	Variant         string               `json:"variant,omitempty"`
	ExitCode        *int                 `json:"exit_code,omitempty"`
	Metrics         map[string]float64   `json:"metrics"`
	Validations     []validationArtifact `json:"validations"`
	ChangedFiles    []string             `json:"changed_files"`
	UnexpectedFiles []string             `json:"unexpected_files"`
	DurationMS      int64                `json:"duration_ms"`
	Error           string               `json:"error,omitempty"`
	StartedAt       string               `json:"started_at"`
	FinishedAt      string               `json:"finished_at"`
	ArtifactsDir    string               `json:"artifacts_dir"`
	Artifacts       map[string]string    `json:"artifacts"`
}

// writePlan records the resolved dry-run plan.
func writePlan(runDir string, req Request, runID string, env []string, baseline Baseline, worktree string) error {
	validators := make([]validatorPlan, 0, len(req.Task.Validators))
	for _, v := range req.Task.Validators {
		validators = append(validators, validatorPlan{
			Kind: v.Kind, Name: v.Name, Command: v.Command, Patterns: v.Patterns, Mode: v.Mode,
		})
	}
	return writeJSON(filepath.Join(runDir, "plan.json"), planFile{
		RunID:        runID,
		ProfileID:    req.Profile.ID,
		ProfileHash:  req.Profile.Hash,
		SuiteName:    req.Suite.Name,
		SuiteVersion: req.Suite.Version,
		SuiteHash:    req.Suite.Hash,
		TaskID:       req.Task.ID,
		TaskVersion:  req.Task.Version,
		FixtureSHA:   baseline.SHA,
		Worktree:     worktree,
		EnvNames:     EnvNames(env),
		Agent:        req.Agent,
		Model:        req.Model,
		Variant:      req.Variant,
		Auto:         req.Auto && !req.Pure,
		Pure:         req.Pure,
		MCPTools:     req.MCPTools,
		Validators:   validators,
	})
}

// writeResult writes result.json for a completed (or failed) run. The artifact
// map is built after the other artifacts exist and explicitly records
// result.json, which this call is about to create.
func writeResult(runDir string, req Request, res Result, baseline Baseline, started, finished string) error {
	validations := make([]validationArtifact, 0, len(res.Validations))
	for _, v := range res.Validations {
		validations = append(validations, validationArtifact{
			Seq:        v.Seq,
			Kind:       v.Kind,
			Name:       v.Name,
			Status:     v.Status,
			ExitCode:   v.ExitCode,
			DurationMS: v.DurationMS,
			OutputPath: validationRelPath(v.Seq, v.Name),
			Excerpt:    v.Excerpt,
		})
	}
	artifacts := existingArtifacts(runDir)
	artifacts["result.json"] = filepath.Join(runDir, "result.json")
	return writeJSON(filepath.Join(runDir, "result.json"), resultFile{
		RunID:           res.RunID,
		Status:          res.Status,
		TaskID:          req.Task.ID,
		TaskVersion:     req.Task.Version,
		SessionID:       res.SessionID,
		ProfileID:       req.Profile.ID,
		ProfileHash:     req.Profile.Hash,
		SuiteName:       req.Suite.Name,
		SuiteVersion:    req.Suite.Version,
		SuiteHash:       req.Suite.Hash,
		FixtureSHA:      baseline.SHA,
		Agent:           req.Agent,
		Model:           req.Model,
		Variant:         req.Variant,
		ExitCode:        res.ExitCode,
		Metrics:         res.Metrics,
		Validations:     validations,
		ChangedFiles:    res.ChangedFiles,
		UnexpectedFiles: res.UnexpectedFiles,
		DurationMS:      res.DurationMS,
		Error:           res.Error,
		StartedAt:       started,
		FinishedAt:      finished,
		ArtifactsDir:    res.ArtifactsDir,
		Artifacts:       artifacts,
	})
}

// existingArtifacts maps relative artifact names to absolute paths for every
// artifact that was actually written. result.json is added by writeResult
// itself, after this scan, because it does not exist yet at this point.
func existingArtifacts(runDir string) map[string]string {
	out := map[string]string{}
	for _, rel := range []string{
		"plan.json", "events.jsonl", "stderr.txt", "session.json",
		"diff.patch", "changed.json",
		"suite.yaml", "task.yaml", "prompt.md",
	} {
		if info, err := os.Stat(filepath.Join(runDir, rel)); err == nil && info.Mode().IsRegular() {
			out[rel] = filepath.Join(runDir, rel)
		}
	}
	for _, dir := range []string{"untracked", "validation"} {
		if info, err := os.Stat(filepath.Join(runDir, dir)); err == nil && info.IsDir() {
			out[dir] = filepath.Join(runDir, dir)
		}
	}
	return out
}

// copySuiteInputs copies suite.yaml, task.yaml and prompt.md into the artifact
// directory so a run is self-describing. Missing files and a nil suite FS are
// skipped; other read failures are returned.
func copySuiteInputs(runDir string, s *suite.Suite, t *suite.Task) error {
	if s == nil || s.FS == nil || t == nil {
		return nil
	}
	files := []struct{ src, dst string }{
		{"suite.yaml", "suite.yaml"},
		{path.Join("tasks", t.ID, "task.yaml"), "task.yaml"},
		{path.Join("tasks", t.ID, "prompt.md"), "prompt.md"},
	}
	for _, f := range files {
		data, err := fs.ReadFile(s.FS, f.src)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("copy %s: %w", f.src, err)
		}
		if err := os.WriteFile(filepath.Join(runDir, f.dst), data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.dst, err)
		}
	}
	return nil
}

// captureUntracked copies every untracked regular file under worktree to
// <runDir>/untracked/<relpath>. Untracked bytes never appear in a git diff, so
// they are retained separately. Symlinks are deliberately not followed or
// copied: they are recorded in changed.json (via ChangedFiles) only, so a link
// pointing outside the worktree cannot exfiltrate its target into the run
// artifacts. It returns the captured paths, sorted.
func captureUntracked(ctx context.Context, runDir, worktree string) ([]string, error) {
	out, err := runGit(ctx, worktree, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	var captured []string
	for _, rel := range splitNul(out) {
		if !safeUntrackedRel(rel) {
			continue
		}
		src := filepath.Join(worktree, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue // symlinks, directories and specials are not copied
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read untracked %s: %w", rel, err)
		}
		dst := filepath.Join(runDir, "untracked", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("create untracked dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, fmt.Errorf("write untracked %s: %w", rel, err)
		}
		captured = append(captured, rel)
	}
	sort.Strings(captured)
	return captured, nil
}

// safeUntrackedRel reports whether a git-reported untracked path is safe to
// join under the artifacts directory: it must be non-empty, relative, and free
// of ".." segments that would escape the destination.
func safeUntrackedRel(rel string) bool {
	return rel != "" && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, "../") && !strings.Contains(rel, "/../")
}

// validationRelPath is the artifact-relative log path of a validator.
func validationRelPath(seq int, name string) string {
	return path.Join("validation", fmt.Sprintf("%d-%s.log", seq, safeLogName(name)))
}

// changeCounts counts created and deleted paths against baselineSHA by unioning
// `git status --porcelain -z` with `git diff --name-status -z`, keyed by path so
// a file counted from both sources is only counted once. Untracked (??) entries
// count as created; renames and copies count as neither.
func changeCounts(ctx context.Context, worktree, baselineSHA string) (created, deleted int, err error) {
	status, err := runGit(ctx, worktree, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return 0, 0, fmt.Errorf("count created/deleted: %w", err)
	}
	diff, err := runGit(ctx, worktree, nil, "diff", "--name-status", "-z", baselineSHA, "--")
	if err != nil {
		return 0, 0, fmt.Errorf("count created/deleted: %w", err)
	}

	kinds := map[string]byte{}
	fields := bytes.Split(status, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		code := string(f[:2])
		p := string(f[3:])
		switch {
		case code == "??":
			kinds[p] = 'A'
		case strings.Contains(code, "A"):
			kinds[p] = 'A'
		case strings.Contains(code, "D"):
			kinds[p] = 'D'
		}
		if strings.ContainsAny(code, "RC") && i+1 < len(fields) && len(fields[i+1]) > 0 {
			i++ // the other path of a rename/copy: neither created nor deleted
		}
	}
	fields = bytes.Split(diff, []byte{0})
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if len(st) == 0 {
			continue
		}
		i++
		if i >= len(fields) {
			break
		}
		p := string(fields[i])
		switch st[0] {
		case 'A':
			if kinds[p] != 'D' {
				kinds[p] = 'A'
			}
		case 'D':
			kinds[p] = 'D'
		case 'R', 'C':
			i++ // the other path of a rename/copy
		}
	}
	for _, k := range kinds {
		switch k {
		case 'A':
			created++
		case 'D':
			deleted++
		}
	}
	return created, deleted, nil
}

// diffLineCounts counts added and removed lines in a unified diff, excluding
// only the "+++ "/"--- " file header lines. A genuine content line beginning
// "+++" or "---" is prefixed by git with another sign ("++++ ..."/"---- ..."),
// so it must still count.
func diffLineCounts(diff []byte) (added, removed int) {
	for _, line := range bytes.Split(diff, []byte{'\n'}) {
		switch {
		case bytes.HasPrefix(line, []byte("+++ ")), bytes.HasPrefix(line, []byte("--- ")):
		case bytes.HasPrefix(line, []byte("+")):
			added++
		case bytes.HasPrefix(line, []byte("-")):
			removed++
		}
	}
	return added, removed
}

// derivedMetrics builds the spec §9 metrics that come from the runner rather
// than the event stream. It is only called for a run whose session completed,
// so every value is computable.
func derivedMetrics(res Result, changed []string, diff []byte, validations []evaluation.ValidationResult, created, deleted int) map[string]float64 {
	added, removed := diffLineCounts(diff)
	failures := 0
	for _, v := range validations {
		switch v.Status {
		case "failed", "error", "timeout":
			failures++
		}
	}
	success := 0.0
	if res.Status == "passed" {
		success = 1
	}
	firstShot := 0.0
	if failures == 0 && res.Status == "passed" {
		firstShot = 1
	}
	return map[string]float64{
		"files_changed":      float64(len(changed)),
		"files_created":      float64(created),
		"files_deleted":      float64(deleted),
		"diff_lines_added":   float64(added),
		"diff_lines_removed": float64(removed),
		"files_unexpected":   float64(len(res.UnexpectedFiles)),
		"duration_ms":        float64(res.DurationMS),
		"validator_failures": float64(failures),
		"success":            success,
		"first_shot_success": firstShot,
	}
}

// matchAny reports whether name matches at least one glob.
func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if matchGlob(g, name) {
			return true
		}
	}
	return false
}

// matchGlob matches a slash-separated glob against a slash-separated path.
// Each non-** segment uses path.Match semantics, so * and ? never cross a
// separator; a ** segment matches zero or more whole segments.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 0 && pat[0] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat, seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], seg[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		seg = seg[1:]
	}
	return len(seg) == 0
}

// safeLogName maps a validator name to a filesystem-safe path element.
func safeLogName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
}

// writeJSON writes indented JSON with a trailing newline, creating parents.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// splitNul splits NUL-terminated git output, dropping the trailing empty field.
func splitNul(data []byte) []string {
	fields := bytes.Split(data, []byte{0})
	if n := len(fields); n > 0 && len(fields[n-1]) == 0 {
		fields = fields[:n-1]
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, string(f))
	}
	return out
}
