package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// maxGrepFileBytes bounds a single file read by the grep validator. A larger
// file is skipped rather than truncated, so a match is never missed by
// accident.
const maxGrepFileBytes = 1 << 20 // 1 MiB

// ValidatorContext is everything a validator may inspect beyond the task
// definition: the worktree the agent worked in, the change set and diff
// computed against the baseline commit, the parsed event stream, and the
// model's final text. Command and answer validators ignore most of it; diff,
// grep and process validators exist to read it.
type ValidatorContext struct {
	Worktree    string
	Changed     []string
	Diff        []byte
	Events      []Event
	FinalAnswer string
}

// RunValidatorContext executes one validator with the full run context. It
// never returns an error: infrastructure problems become an "error" status with
// the reason in Output.
func RunValidatorContext(ctx context.Context, seq int, spec ValidatorSpec, vctx ValidatorContext, env []string, timeout time.Duration) ValidationResult {
	switch spec.Kind {
	case "command":
		return runCommandValidator(ctx, seq, spec, vctx.Worktree, env, timeout)
	case "answer":
		return runAnswerValidator(seq, spec, vctx.FinalAnswer)
	case "diff":
		return runDiffValidator(seq, spec, vctx)
	case "grep":
		return runGrepValidator(seq, spec, vctx.Worktree)
	case "process":
		return runProcessValidator(seq, spec, vctx.Events)
	default:
		res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, Status: "error", ExitCode: -1, Weight: spec.Weight}
		res.setOutput(fmt.Sprintf("unknown validator kind %q", spec.Kind))
		return res
	}
}

// runDiffValidator checks the change set against required and forbidden path
// globs and, when MaxLines is set, against a ceiling on added plus removed
// lines.
func runDiffValidator(seq int, spec ValidatorSpec, vctx ValidatorContext) ValidationResult {
	res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, ExitCode: -1, Weight: spec.Weight}
	var problems []string

	for _, glob := range spec.RequiredPaths {
		if !anyMatch(glob, vctx.Changed) {
			problems = append(problems, fmt.Sprintf("no changed path matches required %q", glob))
		}
	}
	for _, glob := range spec.ForbiddenPaths {
		if hits := matching(glob, vctx.Changed); len(hits) > 0 {
			problems = append(problems, fmt.Sprintf("changed path %q matches forbidden %q", hits[0], glob))
		}
	}
	if spec.MaxLines > 0 {
		added, removed := DiffLineCounts(vctx.Diff)
		if added+removed > spec.MaxLines {
			problems = append(problems, fmt.Sprintf("changed %d lines (added %d, removed %d), ceiling is %d",
				added+removed, added, removed, spec.MaxLines))
		}
	}

	if len(problems) == 0 {
		res.Status = "passed"
		res.ExitCode = 0
		res.setOutput(fmt.Sprintf("%d changed paths, no diff constraint violated", len(vctx.Changed)))
		return res
	}
	res.Status = "failed"
	res.ExitCode = 1
	res.setOutput(strings.Join(problems, "\n"))
	return res
}

// runGrepValidator requires every Present pattern to match somewhere in the
// worktree and every Absent pattern to match nowhere.
func runGrepValidator(seq int, spec ValidatorSpec, worktree string) ValidationResult {
	res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, ExitCode: -1, Weight: spec.Weight}

	compiled, err := compilePatterns(append(append([]string{}, spec.Present...), spec.Absent...))
	if err != nil {
		res.Status = "error"
		res.setOutput(err.Error())
		return res
	}
	present := compiled[:len(spec.Present)]
	absent := compiled[len(spec.Present):]

	files, err := worktreeFiles(worktree)
	if err != nil {
		res.Status = "error"
		res.setOutput(fmt.Sprintf("scan worktree: %v", err))
		return res
	}

	var problems []string
	for i, re := range present {
		if _, ok := firstMatch(worktree, re, files); !ok {
			problems = append(problems, fmt.Sprintf("no file matches required pattern %q", spec.Present[i]))
		}
	}
	for i, re := range absent {
		if file, ok := firstMatch(worktree, re, files); ok {
			problems = append(problems, fmt.Sprintf("pattern %q matched %s", spec.Absent[i], file))
		}
	}

	if len(problems) == 0 {
		res.Status = "passed"
		res.ExitCode = 0
		res.setOutput(fmt.Sprintf("scanned %d files, all grep constraints satisfied", len(files)))
		return res
	}
	res.Status = "failed"
	res.ExitCode = 1
	res.setOutput(strings.Join(problems, "\n"))
	return res
}

// runProcessValidator passes when at least one tool call in the run matched
// ToolPattern. It is deliberately lenient about which part of the call matched
// (tool name, command or description) because a task prompt may phrase the
// expectation any of those ways.
func runProcessValidator(seq int, spec ValidatorSpec, events []Event) ValidationResult {
	res := ValidationResult{Seq: seq, Kind: spec.Kind, Name: spec.Name, ExitCode: -1, Weight: spec.Weight}
	if spec.ToolPattern == "" {
		res.Status = "error"
		res.setOutput("process validator requires tool_pattern")
		return res
	}
	re, err := regexp.Compile(spec.ToolPattern)
	if err != nil {
		res.Status = "error"
		res.setOutput(fmt.Sprintf("invalid tool_pattern %q: %v", spec.ToolPattern, err))
		return res
	}

	seen := toolCallLabels(events)
	for _, label := range seen {
		if re.MatchString(label) {
			res.Status = "passed"
			res.ExitCode = 0
			res.setOutput(fmt.Sprintf("matched %q against %d tool calls", label, len(seen)))
			return res
		}
	}
	res.Status = "failed"
	res.ExitCode = 1
	res.setOutput(fmt.Sprintf("no tool call matched %q; seen: %s", spec.ToolPattern, strings.Join(seen, ", ")))
	return res
}

// compilePatterns compiles every pattern, returning the first error with its
// index so the caller can name the offending pattern.
func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// worktreeFiles lists the readable files under root, sorted, skipping .git,
// non-regular entries and files above the size ceiling. Symlinks are not
// followed, matching the captureUntracked safety rule.
func worktreeFiles(root string) ([]string, error) {
	if root == "" {
		return nil, fmt.Errorf("no worktree")
	}
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxGrepFileBytes {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func firstMatch(root string, re *regexp.Regexp, files []string) (string, bool) {
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		if re.Match(b) {
			return f, true
		}
	}
	return "", false
}

// toolCallLabels renders each tool call in the stream as a searchable string:
// the tool name plus the command or description it carried. A process
// validator matches against these.
func toolCallLabels(events []Event) []string {
	var out []string
	for _, e := range events {
		p, err := e.PartDecoded()
		if err != nil || p.Tool == "" {
			continue
		}
		label := p.Tool
		var input struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			FilePath    string `json:"filePath"`
		}
		if len(p.State) > 0 {
			var state struct {
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(p.State, &state) == nil && len(state.Input) > 0 {
				_ = json.Unmarshal(state.Input, &input)
			}
		}
		for _, extra := range []string{input.Command, input.Description, input.FilePath} {
			if extra != "" {
				label += " " + extra
			}
		}
		out = append(out, label)
	}
	return out
}

// anyMatch reports whether any path matches the glob.
func anyMatch(glob string, paths []string) bool {
	return len(matching(glob, paths)) > 0
}

// matching returns the paths matching a glob. Matching is segment-aware:
// `path.Match` per segment, with `**` spanning any number of segments, matching
// the convention the runner uses for allow_changes.
func matching(glob string, paths []string) []string {
	var out []string
	for _, p := range paths {
		if matchGlob(glob, p) {
			out = append(out, p)
		}
	}
	return out
}

func matchGlob(glob, p string) bool {
	globParts := strings.Split(path.Clean(glob), "/")
	pathParts := strings.Split(path.Clean(p), "/")
	return matchSegments(globParts, pathParts)
}

func matchSegments(glob, p []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			if len(glob) == 1 {
				return true
			}
			for i := 0; i <= len(p); i++ {
				if matchSegments(glob[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		ok, err := path.Match(glob[0], p[0])
		if err != nil || !ok {
			return false
		}
		glob, p = glob[1:], p[1:]
	}
	return len(p) == 0
}

// DiffLineCounts counts added and removed lines in a unified diff, skipping
// only the two header forms (`+++ ` and `--- `) so content that begins with
// `++` or `--` is still counted.
func DiffLineCounts(diff []byte) (added, removed int) {
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
