package suite

import (
	"sort"
	"strings"

	"mbl/ocbench/internal/canon"
)

// taskSpec is the canonical, hashable description of a task. Field order is
// the struct declaration order; canon.JSON only re-sorts map keys, so every
// producer must use this same type.
type taskSpec struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// omitempty keeps a task without metadata hashing exactly as it did
	// before these fields existed, so historical runs stay comparable.
	Difficulty      string          `json:"difficulty,omitempty"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	Timeout         int             `json:"timeout"`
	Requires        []string        `json:"requires"`
	AllowChanges    []string        `json:"allow_changes"`
	Validators      []validatorSpec `json:"validators"`
	PromptSHA256    string          `json:"prompt_sha256"`
	FixtureHash     string          `json:"fixture_hash"`
	EvaluatorSHA256 string          `json:"evaluator_sha256"`
}

type validatorSpec struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Command  []string `json:"command"`
	Patterns []string `json:"patterns"`
	Mode     string   `json:"mode"`
}

type suiteSpec struct {
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Tasks   []taskSpec `json:"tasks"`
}

// HashTasks returns the SHA-256 of the canonical JSON of the task specs,
// sorted by task id. It is order independent: shuffling tasks yields the same
// hash.
func HashTasks(tasks []*Task) (string, error) {
	return canon.Hash(taskSpecs(tasks))
}

// suiteHash returns the SHA-256 of the canonical JSON of {name, version,
// tasks} with tasks sorted by id.
func suiteHash(name, version string, tasks []*Task) (string, error) {
	return canon.Hash(suiteSpec{Name: name, Version: version, Tasks: taskSpecs(tasks)})
}

func taskSpecs(tasks []*Task) []taskSpec {
	specs := make([]taskSpec, 0, len(tasks))
	for _, t := range tasks {
		specs = append(specs, taskSpecOf(t))
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs
}

func taskSpecOf(t *Task) taskSpec {
	return taskSpec{
		ID:              t.ID,
		Version:         t.Version,
		Difficulty:      t.Difficulty,
		Capabilities:    nonNil(t.Capabilities),
		Timeout:         t.TimeoutSeconds,
		Requires:        nonNil(t.Requires),
		AllowChanges:    nonNil(t.AllowChanges),
		Validators:      validatorSpecs(t.Validators),
		PromptSHA256:    canon.SHA256Hex([]byte(t.Prompt)),
		FixtureHash:     t.FixtureHash,
		EvaluatorSHA256: manifestHash(t.Evaluator),
	}
}

func validatorSpecs(validators []Validator) []validatorSpec {
	specs := make([]validatorSpec, 0, len(validators))
	for _, v := range validators {
		specs = append(specs, validatorSpec{
			Kind:     v.Kind,
			Name:     v.Name,
			Command:  nonNil(v.Command),
			Patterns: nonNil(v.Patterns),
			Mode:     v.Mode,
		})
	}
	return specs
}

// manifestHash hashes a relative-path → bytes map as
// SHA256 over the sorted "relpath\0sha256(bytes)" entries joined by newlines.
// Only relative paths are used, so the hash is location independent.
func manifestHash(files map[string][]byte) string {
	entries := make([]string, 0, len(files))
	for rel, b := range files {
		entries = append(entries, rel+"\x00"+canon.SHA256Hex(b))
	}
	sort.Strings(entries)
	return canon.SHA256Hex([]byte(strings.Join(entries, "\n")))
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
