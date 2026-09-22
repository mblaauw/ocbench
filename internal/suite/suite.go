// Package suite loads benchmark suite and task definitions, validates them
// against the schema in spec §7, and computes content hashes.
package suite

import (
	"fmt"
	"io/fs"
	"time"
)

// DefaultTimeoutSeconds is the timeout used when neither the task nor the
// suite declares one.
const DefaultTimeoutSeconds = 900

// Suite is a loaded benchmark suite.
type Suite struct {
	Name        string
	Version     string
	Description string
	Defaults    struct {
		TimeoutSeconds int
	} `yaml:"defaults"`
	Dir   string // absolute dir for on-disk suites; "" for embedded
	FS    fs.FS  // rooted at the suite dir (embedded or os.DirFS)
	Tasks []*Task
	Hash  string
}

// Task is a loaded benchmark task.
type Task struct {
	ID             string
	Version        string
	Name           string
	Tags           []string
	TimeoutSeconds int
	Requires       []string
	AllowChanges   []string
	Validators     []Validator
	Prompt         string
	Dir            string
	Fixture        fs.FS // subtree of the suite FS at tasks/<id>/fixture
	Evaluator      map[string][]byte
	FixtureHash    string
	SpecHash       string
}

// Validator is a single task validator.
type Validator struct {
	Kind     string // "command" | "answer"
	Name     string
	Command  []string
	Patterns []string
	Mode     string // answer: "all" (default) | "any"
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
