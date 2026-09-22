package profile

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// RenderSummary renders the profile's components as an aligned "key  hash"
// table, one line per component, sorted by (kind, name). The component key is
// "kind/name", or just "kind" for singletons where name == kind. Hashes are
// shown as an 8-character prefix. An empty profile renders an empty string.
func RenderSummary(p *Profile) string {
	if p == nil || len(p.Components) == 0 {
		return ""
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	for _, c := range sortedComponents(p.Components) {
		fmt.Fprintf(tw, "%s\t%s\n", componentLabel(c.Kind, c.Name), hashPrefix(c.Hash, 8))
	}
	tw.Flush()
	return buf.String()
}

// RenderChanges renders a component diff, one line per change. Changed
// components carry a 7-character from/to hash prefix:
//
//	skill/ruff  changed  2e11f4a -> 671ab0c
//
// Added and removed components omit hashes. An empty diff renders "no changes".
func RenderChanges(changes []Change) string {
	if len(changes) == 0 {
		return "no changes\n"
	}
	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "  %s  %s", componentLabel(c.Kind, c.Name), c.Change)
		if c.Change == ChangeChanged {
			fmt.Fprintf(&b, "  %s -> %s", hashPrefix(c.FromHash, 7), hashPrefix(c.ToHash, 7))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// componentLabel is the human-facing component key: "kind/name" for named
// components, "kind" for singletons.
func componentLabel(kind, name string) string {
	if name == "" || name == kind {
		return kind
	}
	return kind + "/" + name
}

// sortedComponents returns a copy sorted by (kind, name) so rendering never
// depends on the caller's slice order.
func sortedComponents(comps []Component) []Component {
	out := append([]Component(nil), comps...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// hashPrefix truncates a hash to its first n characters, leaving shorter
// inputs untouched.
func hashPrefix(h string, n int) string {
	if len(h) <= n {
		return h
	}
	return h[:n]
}
