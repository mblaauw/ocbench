package profile

import "sort"

// Change kinds reported by Diff.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// Diff compares two profiles component by component, keyed by (Kind, Name),
// and returns added, removed and changed entries sorted by kind then name.
// Diff(p, p) is empty and nil profiles are treated as empty.
func Diff(from, to *Profile) []Change {
	before := indexComponents(from)
	after := indexComponents(to)

	keys := make(map[componentKey]struct{}, len(before)+len(after))
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}

	out := make([]Change, 0, len(keys))
	for k := range keys {
		f, okFrom := before[k]
		t, okTo := after[k]
		switch {
		case !okFrom:
			out = append(out, Change{Kind: k.kind, Name: k.name, Change: ChangeAdded, ToHash: t.Hash})
		case !okTo:
			out = append(out, Change{Kind: k.kind, Name: k.name, Change: ChangeRemoved, FromHash: f.Hash})
		case f.Hash != t.Hash:
			out = append(out, Change{Kind: k.kind, Name: k.name, Change: ChangeChanged, FromHash: f.Hash, ToHash: t.Hash})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

type componentKey struct {
	kind string
	name string
}

func indexComponents(p *Profile) map[componentKey]Component {
	out := map[componentKey]Component{}
	if p == nil {
		return out
	}
	for _, c := range p.Components {
		out[componentKey{kind: c.Kind, name: c.Name}] = c
	}
	return out
}
