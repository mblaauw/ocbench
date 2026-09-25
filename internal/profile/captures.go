package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The capture files Persist writes beside a profile hash. They hold the raw
// material the fingerprint reduces to hashes: prompt text, instruction bodies
// and skill content.
const (
	captureAgents       = "agents.json"
	captureSkills       = "skills.json"
	captureInstructions = "instructions.json"
)

// CapturedPermission is one entry of an agent's permission list. The component
// form of an agent stores only the rules that name a subagent; the capture keeps
// every rule.
type CapturedPermission struct {
	Permission string
	Pattern    string
	Action     string
}

// CapturedAgent is one agent as captured, including the prompt text the
// component form keeps only as a hash.
type CapturedAgent struct {
	Name        string
	Description string
	Mode        string
	ProviderID  string
	ModelID     string
	Variant     string
	Steps       int
	Temperature *float64
	Native      bool
	Prompt      string
	Tools       map[string]bool
	Permissions []CapturedPermission
}

// Model renders the provider-qualified model id, or "" when unset.
func (a CapturedAgent) Model() string {
	switch {
	case a.ProviderID != "" && a.ModelID != "":
		return a.ProviderID + "/" + a.ModelID
	case a.ModelID != "":
		return a.ModelID
	default:
		return ""
	}
}

// CapturedSkill is one skill as captured, including its body.
type CapturedSkill struct {
	Name          string
	Description   string
	Location      string
	Content       string
	ContentSHA256 string
	FilesSHA256   string
}

// CapturedInstruction is one instruction file as captured, including its text.
type CapturedInstruction struct {
	Scope string
	Text  string
}

// CaptureSet is the capture material stored beside one profile hash.
type CaptureSet struct {
	// Available is false when no capture directory was found, which is the
	// normal case for a profile that exists only in the database.
	Available bool
	Agents    []CapturedAgent
	Skills    []CapturedSkill
	// Instructions are the instruction files, ordered by scope.
	Instructions []CapturedInstruction
}

// ReadCaptures reads the capture files in dir. A directory that does not exist
// yields an empty, unavailable set rather than an error, because a profile
// recorded before captures were written, or loaded from the database alone, is
// still a profile the dashboard must render. A file that exists but cannot be
// parsed is an error: silently showing an empty prompt would misrepresent the
// configuration under test.
func ReadCaptures(dir string) (CaptureSet, error) {
	if dir == "" {
		return CaptureSet{}, nil
	}
	var set CaptureSet

	agents, err := readCaptureFile(dir, captureAgents)
	if err != nil {
		return CaptureSet{}, err
	}
	skills, err := readCaptureFile(dir, captureSkills)
	if err != nil {
		return CaptureSet{}, err
	}
	instructions, err := readCaptureFile(dir, captureInstructions)
	if err != nil {
		return CaptureSet{}, err
	}
	if agents == nil && skills == nil && instructions == nil {
		return CaptureSet{}, nil
	}
	set.Available = true

	if agents != nil {
		var raw []struct {
			Description string          `json:"description"`
			Mode        string          `json:"mode"`
			Model       captureModel    `json:"model"`
			Name        string          `json:"name"`
			Native      bool            `json:"native"`
			Permission  []capturePerm   `json:"permission"`
			Prompt      string          `json:"prompt"`
			Steps       int             `json:"steps"`
			Temperature *float64        `json:"temperature"`
			Tools       map[string]bool `json:"tools"`
			Variant     string          `json:"variant"`
		}
		if err := json.Unmarshal(agents, &raw); err != nil {
			return CaptureSet{}, fmt.Errorf("parse %s: %w", captureAgents, err)
		}
		for _, a := range raw {
			agent := CapturedAgent{
				Name: a.Name, Description: a.Description, Mode: a.Mode,
				ProviderID: a.Model.ProviderID, ModelID: a.Model.ModelID,
				Variant: a.Variant, Steps: a.Steps, Temperature: a.Temperature,
				Native: a.Native, Prompt: a.Prompt, Tools: a.Tools,
			}
			for _, p := range a.Permission {
				agent.Permissions = append(agent.Permissions, CapturedPermission{
					Permission: p.Permission, Pattern: p.Pattern, Action: p.Action,
				})
			}
			set.Agents = append(set.Agents, agent)
		}
		sort.Slice(set.Agents, func(i, j int) bool { return set.Agents[i].Name < set.Agents[j].Name })
	}

	if skills != nil {
		var raw []struct {
			Name          string `json:"name"`
			Description   string `json:"description"`
			Location      string `json:"location"`
			Content       string `json:"content"`
			ContentSHA256 string `json:"content_sha256"`
			FilesSHA256   string `json:"files_sha256"`
		}
		if err := json.Unmarshal(skills, &raw); err != nil {
			return CaptureSet{}, fmt.Errorf("parse %s: %w", captureSkills, err)
		}
		for _, s := range raw {
			set.Skills = append(set.Skills, CapturedSkill{
				Name: s.Name, Description: s.Description, Location: s.Location,
				Content: s.Content, ContentSHA256: s.ContentSHA256, FilesSHA256: s.FilesSHA256,
			})
		}
		sort.Slice(set.Skills, func(i, j int) bool { return set.Skills[i].Name < set.Skills[j].Name })
	}

	if instructions != nil {
		var raw map[string]string
		if err := json.Unmarshal(instructions, &raw); err != nil {
			return CaptureSet{}, fmt.Errorf("parse %s: %w", captureInstructions, err)
		}
		for scope, text := range raw {
			set.Instructions = append(set.Instructions, CapturedInstruction{Scope: scope, Text: text})
		}
		sort.Slice(set.Instructions, func(i, j int) bool {
			return set.Instructions[i].Scope < set.Instructions[j].Scope
		})
	}

	return set, nil
}

// captureModel is the provider-qualified model the capture files carry, as
// opposed to the single string the component form normalises it to.
type captureModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// capturePerm is one captured permission rule.
type capturePerm struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

// readCaptureFile returns the contents of one capture file, or nil when it is
// absent. Only a missing file is tolerated: any other error is reported.
func readCaptureFile(dir, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}
