package profile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ChangeNote is a component difference explained in words, for the dashboard's
// "what changed between these two profiles" panel.
type ChangeNote struct {
	Sign   string // "+" added, "~" changed, "−" removed
	Change string // ChangeAdded, ChangeChanged or ChangeRemoved
	Kind   string
	Name   string
	Note   string
}

// kindOrder groups the notes the way a reader scans them: agents first, then
// what they may do, then the things they can reach.
var kindOrder = map[string]int{
	"primary": 0, "agent": 1, "permissions": 2,
	"skill": 3, "mcp": 4, "plugin": 5, "instructions": 6, "config": 7,
}

// DiffNotes explains how subject differs from reference: entries present only
// in the subject are added, entries only in the reference are removed, and
// entries whose content differs carry a human note such as
// "model a → b, tools +bash −webfetch".
func DiffNotes(reference, subject *Profile) []ChangeNote {
	refView := NewView(reference)
	subView := NewView(subject)

	out := make([]ChangeNote, 0)
	for _, c := range Diff(reference, subject) {
		note := ChangeNote{Change: c.Change, Kind: c.Kind, Name: c.Name}
		switch c.Change {
		case ChangeAdded:
			note.Sign = "+"
			note.Note = describeComponent(subView, c.Kind, c.Name)
		case ChangeRemoved:
			note.Sign = "−"
			note.Note = describeComponent(refView, c.Kind, c.Name)
		default:
			note.Sign = "~"
			note.Note = describeChange(refView, subView, c.Kind, c.Name)
		}
		out = append(out, note)
	}

	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := kindOrder[out[i].Kind], kindOrder[out[j].Kind]
		if oi != oj {
			return oi < oj
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// describeComponent summarises a component as it stands in one profile.
func describeComponent(v View, kind, name string) string {
	switch kind {
	case "primary":
		return strings.TrimSpace(v.Primary.Model + " " + v.Primary.Variant)
	case "agent":
		if a, ok := findAgent(v, name); ok {
			return strings.TrimSpace(shortModel(a.Model) + " " + a.Variant)
		}
	case "skill", "mcp", "plugin":
		// The component name already says what it is; repeating the kind in the
		// note column adds nothing.
		return ""
	case "instructions":
		return "instruction file"
	case "permissions":
		return "permission rules"
	case "config":
		return "configuration"
	case "compaction", "share", "autoupdate", "formatter", "lsp", "tools":
		// The component name is the setting, so the note carries its value.
		return scalarText(v.Settings[kind])
	}
	return ""
}

// describeChange explains what differs between two versions of a component.
func describeChange(refView, subView View, kind, name string) string {
	switch kind {
	case "primary":
		return joinParts(
			pairChange("model", refView.Primary.Model, subView.Primary.Model),
			pairChange("small model", refView.Primary.SmallModel, subView.Primary.SmallModel),
			pairChange("variant", refView.Primary.Variant, subView.Primary.Variant),
			pairChange("default agent", refView.Primary.DefaultAgent, subView.Primary.DefaultAgent),
		)
	case "agent":
		ref, okRef := findAgent(refView, name)
		sub, okSub := findAgent(subView, name)
		if !okRef || !okSub {
			return "agent definition"
		}
		return joinParts(
			pairChange("model", shortModel(ref.Model), shortModel(sub.Model)),
			pairChange("variant", ref.Variant, sub.Variant),
			pairChange("mode", ref.Mode, sub.Mode),
			pairChange("steps", itoa(ref.Steps), itoa(sub.Steps)),
			toolsChange(ref.Tools, sub.Tools),
		)
	case "permissions":
		return "permission rules"
	case "skill", "mcp":
		return kind + " configuration"
	case "instructions":
		return "instruction content"
	case "plugin":
		return "plugin"
	case "config":
		return "configuration"
	case "compaction", "share", "autoupdate", "formatter", "lsp", "tools":
		return describeSetting(kind, refView.Settings[kind], subView.Settings[kind])
	case "command":
		return "command definition"
	case "provider":
		return "provider options"
	case "mode":
		return "mode definition"
	}
	return ""
}

// describeSetting explains what changed about one promoted config setting. A
// scalar shows its two values; a map names the keys that differ, which is the
// attribution the split exists to provide — reporting the whole object as
// "changed" would be barely better than the catch-all it replaced.
func describeSetting(name string, ref, sub any) string {
	refMap, refIsMap := ref.(map[string]any)
	subMap, subIsMap := sub.(map[string]any)
	if refIsMap || subIsMap {
		changed := changedSettingKeys(refMap, subMap)
		if len(changed) == 0 {
			return name + " changed"
		}
		return name + ": " + strings.Join(changed, ", ")
	}
	return name + ": " + scalarText(ref) + "→" + scalarText(sub)
}

// changedSettingKeys lists, in sorted order, the keys whose value differs
// between two settings objects. Values are compared by their JSON encoding,
// which sorts map keys, so iteration order cannot make the result flaky.
func changedSettingKeys(ref, sub map[string]any) []string {
	seen := map[string]bool{}
	var keys []string
	for k := range ref {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range sub {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if jsonText(ref[k]) != jsonText(sub[k]) {
			out = append(out, k)
		}
	}
	return out
}

// jsonText renders a setting value for comparison.
func jsonText(v any) string {
	if v == nil {
		return ""
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(encoded)
}

// scalarText renders a setting value for display, keeping it short.
func scalarText(v any) string {
	switch t := v.(type) {
	case nil:
		return "unset"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return jsonText(v)
	}
}

// toolsChange lists tool toggles that differ, e.g. "+bash −webfetch".
func toolsChange(ref, sub map[string]bool) string {
	var added, removed []string
	for name, on := range sub {
		if on && !ref[name] {
			added = append(added, "+"+name)
		}
	}
	for name, on := range ref {
		if on && !sub[name] {
			removed = append(removed, "−"+name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	parts := append(added, removed...)
	if len(parts) == 0 {
		return ""
	}
	return "tools " + strings.Join(parts, " ")
}

func pairChange(label, ref, sub string) string {
	if ref == sub {
		return ""
	}
	if ref == "" {
		return label + " " + sub
	}
	if sub == "" {
		return label + " " + ref + " → (unset)"
	}
	return label + " " + ref + " → " + sub
}

func joinParts(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ", ")
}

func findAgent(v View, name string) (Agent, bool) {
	for _, a := range v.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}
