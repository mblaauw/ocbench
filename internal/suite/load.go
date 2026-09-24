package suite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"mbl/ocbench/internal/canon"
)

// scalarString decodes a YAML scalar into its literal text. Suite and task
// versions are opaque strings, so both `version: 1` and `version: "1.0.0"`
// must load without lossy type coercion.
type scalarString string

func (s *scalarString) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected scalar, got YAML kind %d", value.Kind)
	}
	*s = scalarString(value.Value)
	return nil
}

type rawSuite struct {
	Name        string       `yaml:"name"`
	Version     scalarString `yaml:"version"`
	Description string       `yaml:"description"`
	Tier        string       `yaml:"tier"`
	Defaults    struct {
		TimeoutSeconds int `yaml:"timeout"`
	} `yaml:"defaults"`
}

type rawTask struct {
	ID             string         `yaml:"id"`
	Version        scalarString   `yaml:"version"`
	Name           string         `yaml:"name"`
	Tags           []string       `yaml:"tags"`
	Difficulty     string         `yaml:"difficulty"`
	Capabilities   []string       `yaml:"capabilities"`
	ExpectedTokens int            `yaml:"expected_tokens"`
	Timeout        int            `yaml:"timeout"`
	Requires       []string       `yaml:"requires"`
	AllowChanges   []string       `yaml:"allow_changes"`
	Validators     []rawValidator `yaml:"validators"`
}

type rawValidator struct {
	Kind     string   `yaml:"kind"`
	Name     string   `yaml:"name"`
	Command  []string `yaml:"command"`
	Patterns []string `yaml:"patterns"`
	Mode     string   `yaml:"mode"`

	RequiredPaths  []string `yaml:"required_paths"`
	ForbiddenPaths []string `yaml:"forbidden_paths"`
	MaxLines       int      `yaml:"max_lines"`

	Present []string `yaml:"present"`
	Absent  []string `yaml:"absent"`

	ToolPattern string `yaml:"tool_pattern"`

	Weight float64 `yaml:"weight"`
}

// rawAnswer is the schema of evaluator/answer.json, the hidden fallback for
// answer validator patterns.
type rawAnswer struct {
	Patterns []string `json:"patterns"`
	Mode     string   `json:"mode"`
}

// LoadFS loads a suite rooted at root inside fsys. The returned Suite.FS is
// rooted at the suite directory; Dir and Task.Dir are empty because fsys may
// be an embedded filesystem with no meaningful on-disk location.
func LoadFS(fsys fs.FS, root string) (*Suite, error) {
	sub, err := fs.Sub(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("suite root %q: %w", root, err)
	}

	raw, err := readYAML[rawSuite](sub, "suite.yaml")
	if err != nil {
		return nil, err
	}
	if raw.Name == "" || raw.Version == "" {
		return nil, fmt.Errorf("suite.yaml: name and version are required")
	}
	if err := validateTier(raw.Tier); err != nil {
		return nil, fmt.Errorf("suite.yaml: %w", err)
	}

	s := &Suite{
		Name:        raw.Name,
		Version:     string(raw.Version),
		Description: raw.Description,
		Tier:        raw.Tier,
		FS:          sub,
	}
	s.Defaults.TimeoutSeconds = raw.Defaults.TimeoutSeconds

	entries, err := fs.ReadDir(sub, "tasks")
	if err != nil {
		return nil, fmt.Errorf("suite %q: read tasks dir: %w", raw.Name, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		task, err := loadTask(sub, entry.Name(), raw.Defaults.TimeoutSeconds)
		if err != nil {
			return nil, err
		}
		s.Tasks = append(s.Tasks, task)
	}

	hash, err := suiteHash(s.Name, s.Version, s.Tasks)
	if err != nil {
		return nil, fmt.Errorf("suite %q: hash: %w", raw.Name, err)
	}
	s.Hash = hash
	return s, nil
}

// LoadDir loads a suite from a directory on disk. Unlike LoadFS it records the
// absolute locations of the suite and each task.
func LoadDir(dir string) (*Suite, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("suite dir %q: %w", dir, err)
	}
	s, err := LoadFS(os.DirFS(abs), ".")
	if err != nil {
		return nil, err
	}
	s.Dir = abs
	for _, t := range s.Tasks {
		t.Dir = filepath.Join(abs, "tasks", t.ID)
	}
	return s, nil
}

// loadHiddenTests returns the evaluator/tests subtree, or nil when it is
// absent. The subtree is deliberately not part of the evaluator map: the map
// is hashed and used out of band, while these files are copied into the
// worktree after the agent stops.
func loadHiddenTests(fsys fs.FS, dir string) (fs.FS, error) {
	info, err := fs.Stat(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("hidden tests %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("hidden tests %s is not a directory", dir)
	}
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("hidden tests %s: %w", dir, err)
	}
	return sub, nil
}

// loadTask loads tasks/<id>/ as a Task. suiteDefault is the suite's
// defaults.timeout (0 when unset).
func loadTask(fsys fs.FS, id string, suiteDefault int) (*Task, error) {
	base := path.Join("tasks", id)

	raw, err := readYAML[rawTask](fsys, path.Join(base, "task.yaml"))
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}
	switch {
	case raw.ID == "" || raw.Version == "" || raw.Name == "":
		return nil, fmt.Errorf("task %q: task.yaml requires id, version and name", id)
	case raw.ID != id:
		return nil, fmt.Errorf("task %q: task.yaml id %q does not match directory", id, raw.ID)
	}

	if err := validateDifficulty(raw.Difficulty); err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}
	if err := validateCapabilities(raw.Capabilities); err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}
	if raw.ExpectedTokens < 0 {
		return nil, fmt.Errorf("task %q: expected_tokens must not be negative, got %d", id, raw.ExpectedTokens)
	}

	prompt, err := fs.ReadFile(fsys, path.Join(base, "prompt.md"))
	if err != nil {
		return nil, fmt.Errorf("task %q: read prompt.md: %w", id, err)
	}

	fixture, fixtureHash, err := loadFixture(fsys, path.Join(base, "fixture"))
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	evaluator, err := readEvaluator(fsys, path.Join(base, "evaluator"))
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	hidden, err := loadHiddenTests(fsys, path.Join(base, "evaluator", "tests"))
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	reference, err := loadHiddenTests(fsys, path.Join(base, "evaluator", "reference"))
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	t := &Task{
		ID:             raw.ID,
		Version:        string(raw.Version),
		Name:           raw.Name,
		Tags:           raw.Tags,
		Difficulty:     raw.Difficulty,
		Capabilities:   raw.Capabilities,
		ExpectedTokens: raw.ExpectedTokens,
		TimeoutSeconds: resolveTimeout(raw.Timeout, suiteDefault),
		Requires:       raw.Requires,
		AllowChanges:   raw.AllowChanges,
		Prompt:         string(prompt),
		Fixture:        fixture,
		Evaluator:      evaluator,
		HiddenTests:    hidden,
		Reference:      reference,
		FixtureHash:    fixtureHash,
	}
	for i, rv := range raw.Validators {
		v, err := resolveValidator(rv, evaluator, id, i)
		if err != nil {
			return nil, err
		}
		t.Validators = append(t.Validators, v)
	}

	spec, err := canon.Hash(taskSpecOf(t))
	if err != nil {
		return nil, fmt.Errorf("task %q: hash spec: %w", id, err)
	}
	t.SpecHash = spec
	return t, nil
}

// resolveTimeout applies the task → suite default → built-in 900s chain.
func resolveTimeout(task, suiteDefault int) int {
	if task > 0 {
		return task
	}
	if suiteDefault > 0 {
		return suiteDefault
	}
	return DefaultTimeoutSeconds
}

// resolveValidator validates a raw validator and resolves answer patterns and
// mode, falling back to evaluator/answer.json.
func resolveValidator(raw rawValidator, evaluator map[string][]byte, id string, i int) (Validator, error) {
	switch raw.Kind {
	case "command":
		if len(raw.Command) == 0 {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): command requires a non-empty argv", id, i, raw.Name)
		}
		return Validator{Kind: raw.Kind, Name: raw.Name, Command: raw.Command, Weight: raw.Weight}, nil
	case "diff":
		if len(raw.RequiredPaths) == 0 && len(raw.ForbiddenPaths) == 0 && raw.MaxLines <= 0 {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): diff validator needs required_paths, forbidden_paths or max_lines", id, i, raw.Name)
		}
		if raw.MaxLines < 0 {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): max_lines must not be negative", id, i, raw.Name)
		}
		return Validator{
			Kind: raw.Kind, Name: raw.Name,
			RequiredPaths: raw.RequiredPaths, ForbiddenPaths: raw.ForbiddenPaths, MaxLines: raw.MaxLines,
			Weight: raw.Weight,
		}, nil
	case "grep":
		if len(raw.Present) == 0 && len(raw.Absent) == 0 {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): grep validator needs present or absent patterns", id, i, raw.Name)
		}
		if _, err := regexp.Compile(strings.Join(append(append([]string{}, raw.Present...), raw.Absent...), "|")); err != nil {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): %w", id, i, raw.Name, err)
		}
		return Validator{
			Kind: raw.Kind, Name: raw.Name,
			Present: raw.Present, Absent: raw.Absent,
			Weight: raw.Weight,
		}, nil
	case "process":
		if raw.ToolPattern == "" {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): process validator needs tool_pattern", id, i, raw.Name)
		}
		if _, err := regexp.Compile(raw.ToolPattern); err != nil {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): invalid tool_pattern: %w", id, i, raw.Name, err)
		}
		return Validator{Kind: raw.Kind, Name: raw.Name, ToolPattern: raw.ToolPattern, Weight: raw.Weight}, nil
	case "answer":
		patterns, mode := raw.Patterns, raw.Mode
		if len(patterns) == 0 {
			data, ok := evaluator["answer.json"]
			if !ok {
				return Validator{}, fmt.Errorf("task %q: validator %d (%s): answer validator needs patterns in task.yaml or evaluator/answer.json", id, i, raw.Name)
			}
			var answer rawAnswer
			if err := json.Unmarshal(data, &answer); err != nil {
				return Validator{}, fmt.Errorf("task %q: parse evaluator/answer.json: %w", id, err)
			}
			patterns = answer.Patterns
			if mode == "" {
				mode = answer.Mode
			}
		}
		if len(patterns) == 0 {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): answer validator needs patterns in task.yaml or evaluator/answer.json", id, i, raw.Name)
		}
		if mode == "" {
			mode = "all"
		}
		if mode != "all" && mode != "any" {
			return Validator{}, fmt.Errorf("task %q: validator %d (%s): mode %q must be all or any", id, i, raw.Name, mode)
		}
		return Validator{Kind: raw.Kind, Name: raw.Name, Patterns: patterns, Mode: mode, Weight: raw.Weight}, nil
	default:
		return Validator{}, fmt.Errorf("task %q: validator %d: unknown kind %q", id, i, raw.Kind)
	}
}

// loadFixture verifies that dir is a directory and hashes its regular-file
// tree. The returned fs.FS is rooted at the fixture.
func loadFixture(fsys fs.FS, dir string) (fs.FS, string, error) {
	info, err := fs.Stat(fsys, dir)
	if err != nil {
		return nil, "", fmt.Errorf("fixture %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("fixture %s is not a directory", dir)
	}
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, "", fmt.Errorf("fixture %s: %w", dir, err)
	}

	files := map[string][]byte{}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		files[path.Clean(p)] = b
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("walk fixture %s: %w", dir, err)
	}
	return sub, manifestHash(files), nil
}

// readEvaluator reads every regular file under dir into a map keyed by its
// path relative to dir. A missing evaluator directory is not an error.
func readEvaluator(fsys fs.FS, dir string) (map[string][]byte, error) {
	info, err := fs.Stat(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string][]byte{}, nil
		}
		return nil, fmt.Errorf("evaluator %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("evaluator %s is not a directory", dir)
	}

	files := map[string][]byte{}
	err = fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk evaluator %s: %w", dir, err)
	}
	return files, nil
}

// readYAML decodes a YAML file into T with strict field checking, so a
// misspelled or unknown key is a load error instead of a silent default.
func readYAML[T any](fsys fs.FS, name string) (*T, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	var v T
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	return &v, nil
}
