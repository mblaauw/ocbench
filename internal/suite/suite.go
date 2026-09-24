// Package suite loads benchmark suite and task definitions, validates them
// against the schema in spec §7, and computes content hashes.
package suite

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// difficultyLevels and suiteTiers are the accepted YAML values (design §7).
var difficultyLevels = []string{"easy", "medium", "hard"}
var suiteTiers = []string{"smoke", "standard", "hard"}

// CapabilityVocabulary is the closed set from design §14.1. It is closed on
// purpose: results can be grouped by capability without a schema change only if
// every task uses the same words.
var CapabilityVocabulary = []string{
	"delegation", "planning", "tool-use", "context", "skill-use",
	"instruction-following", "restraint", "debugging", "multi-file", "search",
}

func validateDifficulty(v string) error {
	if v == "" || slices.Contains(difficultyLevels, v) {
		return nil
	}
	return fmt.Errorf("difficulty %q is not one of %s", v, strings.Join(difficultyLevels, ", "))
}

func validateCapabilities(cs []string) error {
	for _, c := range cs {
		if !slices.Contains(CapabilityVocabulary, c) {
			return fmt.Errorf("capability %q is not one of %s", c, strings.Join(CapabilityVocabulary, ", "))
		}
	}
	return nil
}

func validateTier(v string) error {
	if v == "" || slices.Contains(suiteTiers, v) {
		return nil
	}
	return fmt.Errorf("suite tier %q is not one of %s", v, strings.Join(suiteTiers, ", "))
}

// DefaultTimeoutSeconds is the timeout used when neither the task nor the
// suite declares one.
const DefaultTimeoutSeconds = 900

// Suite is a loaded benchmark suite.
type Suite struct {
	Name        string
	Version     string
	Description string
	// Tier is "smoke", "standard" or "hard" ("" when unset). It says how
	// discriminating the suite is meant to be, not how it is executed.
	Tier     string
	Defaults struct {
		TimeoutSeconds int
	} `yaml:"defaults"`
	Dir   string // absolute dir for on-disk suites; "" for embedded
	FS    fs.FS  // rooted at the suite dir (embedded or os.DirFS)
	Tasks []*Task
	Hash  string
}

// Task is a loaded benchmark task.
type Task struct {
	ID      string
	Version string
	Name    string
	Tags    []string
	// Difficulty is "easy", "medium" or "hard" ("" when unset).
	Difficulty string
	// Capabilities are the behaviours this task exercises, drawn from the
	// vocabulary in design §14.1.
	Capabilities []string
	// ExpectedTokens is an estimate used to normalise cost per solved task.
	// It is deliberately excluded from the task hash: retuning an estimate
	// must not invalidate comparability.
	ExpectedTokens int
	TimeoutSeconds int
	Requires       []string
	AllowChanges   []string
	Validators     []Validator
	Prompt         string
	Dir            string
	Fixture        fs.FS // subtree of the suite FS at tasks/<id>/fixture
	Evaluator      map[string][]byte
	// HiddenTests is the subtree at tasks/<id>/evaluator/tests, or nil. It is
	// copied into the worktree only after the agent stops, so a task can be
	// graded by tests the agent never saw (design §7).
	HiddenTests fs.FS
	// HiddenTestsDest is the worktree-relative directory the hidden tests are
	// copied into. It defaults to "tests"; a language whose tests live beside
	// the source (Go) sets "." instead.
	HiddenTestsDest string
	// Reference is the subtree at tasks/<id>/evaluator/reference, or nil: the
	// solution that must make the validators pass. It is never materialised
	// into a worktree; a suite test uses it to prove the task measures
	// something (design §7).
	Reference   fs.FS
	FixtureHash string
	SpecHash    string
}

// Validator is a single task validator. Kind is "command", "answer", "diff",
// "grep" or "process"; the fields below apply per kind (design §7).
type Validator struct {
	Kind     string // command | answer | diff | grep | process
	Name     string
	Command  []string
	Patterns []string
	Mode     string // answer: "all" (default) | "any"

	RequiredPaths  []string // diff
	ForbiddenPaths []string // diff
	MaxLines       int      // diff

	Present []string // grep
	Absent  []string // grep

	ToolPattern string // process

	// Weight feeds the weighted score; zero means 1.
	Weight float64
}

// Task returns the task with the given id.
func (s *Suite) Task(id string) (*Task, error) {
	for _, t := range s.Tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("suite %q: unknown task %q", s.Name, id)
}

// EffectiveTimeout resolves the task timeout with the chain task → suite
// default → 900 seconds. Loaded tasks already carry the resolved value in
// TimeoutSeconds; this method keeps the chain available for hand-built tasks.
func (t *Task) EffectiveTimeout(s *Suite) time.Duration {
	seconds := t.TimeoutSeconds
	if seconds <= 0 && s != nil {
		seconds = s.Defaults.TimeoutSeconds
	}
	if seconds <= 0 {
		seconds = DefaultTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}
