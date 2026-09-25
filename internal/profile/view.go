package profile

import (
	"encoding/json"
	"sort"
	"strings"
)

// View is a profile decoded into the shape the dashboard renders: models,
// agents, skills, MCP servers, instructions, plugins and the subagent
// architecture. It is derived entirely from the stored component JSON, so it
// needs no capture files except for instruction text (see Captures).
type View struct {
	Hash            string
	OpenCodeVersion string

	Primary       Primary
	Agents        []Agent
	Skills        []Skill
	MCP           []MCP
	Instructions  []Instruction
	Plugins       []Plugin
	Config        map[string]any
	SubagentDepth int
}

// Primary is the profile's default model selection.
type Primary struct {
	DefaultAgent string
	Model        string
	SmallModel   string
	Variant      string
}

// Agent is one configured agent.
type Agent struct {
	Name        string
	Mode        string // "primary" or "subagent"
	Model       string
	Variant     string
	TopP        float64
	Temperature *float64
	Options     map[string]any
	Steps       int
	Native      bool
	Tools       map[string]bool
	PromptSHA   string
	TaskRules   []TaskRule
	Description string
}

// TaskRule is one permission.task rule: which subagent a pattern names, and
// whether calling it is allowed.
type TaskRule struct {
	Pattern string
	Action  string
}

// Skill is a configured skill.
type Skill struct {
	Name        string
	Description string
}

// MCP is a configured MCP server, with the names of its configuration keys
// (never their values).
type MCP struct {
	Name string
	Keys []string
}

// Instruction is one instruction file, by scope.
type Instruction struct {
	Scope string
	Path  string
	SHA   string
}

// Plugin is one configured plugin, by its normalised spec.
type Plugin struct {
	Spec string
}

// NewView decodes a profile's components into a View. Undecodable components are
// skipped rather than failing: a profile written by an older version must still
// render what it has.
func NewView(p *Profile) View {
	v := View{
		Hash:            p.Hash,
		OpenCodeVersion: p.OpenCodeVersion,
	}

	for _, c := range p.Components {
		switch c.Kind {
		case "primary":
			v.Primary = decodePrimary(c.CanonicalJSON)
		case "agent":
			if a, ok := decodeAgent(c.Name, c.CanonicalJSON); ok {
				v.Agents = append(v.Agents, a)
			}
		case "skill":
			s := Skill{Name: c.Name}
			var raw struct {
				Description string `json:"description"`
			}
			if json.Unmarshal(c.CanonicalJSON, &raw) == nil {
				s.Description = raw.Description
			}
			v.Skills = append(v.Skills, s)
		case "mcp":
			v.MCP = append(v.MCP, MCP{Name: c.Name, Keys: objectKeys(c.CanonicalJSON)})
		case "instructions":
			var raw struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			}
			_ = json.Unmarshal(c.CanonicalJSON, &raw)
			v.Instructions = append(v.Instructions, Instruction{Scope: c.Name, Path: raw.Path, SHA: raw.SHA256})
		case "plugin":
			v.Plugins = append(v.Plugins, Plugin{Spec: c.Name})
		case "config":
			var raw map[string]any
			if json.Unmarshal(c.CanonicalJSON, &raw) == nil {
				v.Config = raw
			}
		}
	}

	// The task permission rules live in the permissions component, keyed by
	// agent: they are what names the subagents an agent may call.
	taskRules := decodeTaskRules(p.Components)
	for i := range v.Agents {
		v.Agents[i].TaskRules = taskRules[v.Agents[i].Name]
	}

	if d, ok := v.Config["subagent_depth"].(float64); ok {
		v.SubagentDepth = int(d)
	}

	sort.Slice(v.Agents, func(i, j int) bool { return v.Agents[i].Name < v.Agents[j].Name })
	sort.Slice(v.Skills, func(i, j int) bool { return v.Skills[i].Name < v.Skills[j].Name })
	sort.Slice(v.MCP, func(i, j int) bool { return v.MCP[i].Name < v.MCP[j].Name })
	sort.Slice(v.Instructions, func(i, j int) bool { return v.Instructions[i].Scope < v.Instructions[j].Scope })
	sort.Slice(v.Plugins, func(i, j int) bool { return v.Plugins[i].Spec < v.Plugins[j].Spec })
	return v
}

// PrimaryAgent returns the agent the profile runs by default, falling back to
// the first primary-mode agent.
func (v View) PrimaryAgent() (Agent, bool) {
	if v.Primary.DefaultAgent != "" {
		for _, a := range v.Agents {
			if a.Name == v.Primary.DefaultAgent {
				return a, true
			}
		}
	}
	for _, a := range v.Agents {
		if a.Mode == "primary" {
			return a, true
		}
	}
	return Agent{}, false
}

// Subagents returns the agents with subagent mode, by name.
func (v View) Subagents() []Agent {
	var out []Agent
	for _, a := range v.Agents {
		if a.Mode == "subagent" {
			out = append(out, a)
		}
	}
	return out
}

// Edge is an allowed delegation from one agent to another.
type Edge struct {
	From string
	To   string
}

// Architecture is the delegation graph: which primary agents may call which
// subagents, derived from the permission.task rules.
type Architecture struct {
	Primaries []Agent
	Subagents []Agent
	Edges     []Edge
}

// Architecture resolves the delegation graph. A rule names a subagent
// explicitly or uses "*" as a catch-all; the most specific rule wins, which is
// how the flattened rule list encodes the original map.
func (v View) Architecture() Architecture {
	subs := v.Subagents()
	subNames := make(map[string]bool, len(subs))
	for _, s := range subs {
		subNames[s.Name] = true
	}

	arch := Architecture{Subagents: subs}
	for _, a := range v.Agents {
		if a.Mode != "primary" {
			continue
		}
		arch.Primaries = append(arch.Primaries, a)
		for _, name := range allowedTargets(a.TaskRules, subNames) {
			arch.Edges = append(arch.Edges, Edge{From: a.Name, To: name})
		}
	}
	sort.Slice(arch.Edges, func(i, j int) bool {
		if arch.Edges[i].From != arch.Edges[j].From {
			return arch.Edges[i].From < arch.Edges[j].From
		}
		return arch.Edges[i].To < arch.Edges[j].To
	})
	return arch
}

// allowedTargets resolves the effective action for every known subagent: the
// last rule naming it wins, otherwise the last "*" rule decides.
func allowedTargets(rules []TaskRule, known map[string]bool) []string {
	specific := map[string]string{}
	catchAll := ""
	for _, r := range rules {
		if r.Pattern == "*" {
			catchAll = r.Action
			continue
		}
		specific[r.Pattern] = r.Action
	}
	var out []string
	for name := range known {
		action, ok := specific[name]
		if !ok {
			action = catchAll
		}
		if action == "allow" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Summary renders the one-line architecture label the leaderboard shows, for
// example "build · deepseek-v4.1-flash low · +6 sub".
func (v View) Summary() string {
	agent, ok := v.PrimaryAgent()
	if !ok {
		return "no primary agent"
	}
	parts := []string{agent.Name}
	if model := shortModel(agent.Model); model != "" {
		if agent.Variant != "" {
			model += " " + agent.Variant
		}
		parts = append(parts, model)
	}
	if n := len(v.Subagents()); n > 0 {
		parts = append(parts, "+"+itoa(n)+" sub")
	}
	return strings.Join(parts, " · ")
}

// shortModel drops the provider prefix: "opencode-go/deepseek-v4.1-flash"
// becomes "deepseek-v4.1-flash".
func shortModel(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func decodePrimary(data []byte) Primary {
	var raw struct {
		DefaultAgent string `json:"default_agent"`
		Model        string `json:"model"`
		SmallModel   string `json:"small_model"`
		Variant      string `json:"variant"`
	}
	_ = json.Unmarshal(data, &raw)
	return Primary{DefaultAgent: raw.DefaultAgent, Model: raw.Model, SmallModel: raw.SmallModel, Variant: raw.Variant}
}

func decodeAgent(name string, data []byte) (Agent, bool) {
	var raw struct {
		Mode        string          `json:"mode"`
		Model       string          `json:"model"`
		Variant     string          `json:"variant"`
		TopP        float64         `json:"top_p"`
		Temperature *float64        `json:"temperature"`
		Options     map[string]any  `json:"options"`
		Steps       int             `json:"steps"`
		Native      bool            `json:"native"`
		PromptSHA   string          `json:"prompt_sha256"`
		Tools       map[string]bool `json:"tools"`
		Descripion  string          `json:"description"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Agent{}, false
	}
	return Agent{
		Name: name, Mode: raw.Mode, Model: raw.Model, Variant: raw.Variant,
		TopP: raw.TopP, Temperature: raw.Temperature, Options: raw.Options,
		Steps: raw.Steps, Native: raw.Native, PromptSHA: raw.PromptSHA,
		Tools: raw.Tools, Description: raw.Descripion,
	}, true
}

// decodeTaskRules pulls permission.task rules per agent out of the permissions
// component.
func decodeTaskRules(components []Component) map[string][]TaskRule {
	out := map[string][]TaskRule{}
	for _, c := range components {
		if c.Kind != "permissions" {
			continue
		}
		var raw struct {
			ByAgent map[string][]struct {
				Action     string `json:"action"`
				Pattern    string `json:"pattern"`
				Permission string `json:"permission"`
			} `json:"by_agent"`
		}
		if json.Unmarshal(c.CanonicalJSON, &raw) != nil {
			return out
		}
		for agent, rules := range raw.ByAgent {
			for _, r := range rules {
				if r.Permission == "task" {
					out[agent] = append(out[agent], TaskRule{Pattern: r.Pattern, Action: r.Action})
				}
			}
		}
	}
	return out
}

// objectKeys returns the sorted top-level keys of a JSON object, used to show
// an MCP server's configuration shape without its values.
func objectKeys(data []byte) []string {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
