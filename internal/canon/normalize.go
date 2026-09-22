package canon

import (
	"sort"
	"strings"
)

// PathPrefix maps a leading path prefix to the portable placeholder that
// replaces it in canonical output.
type PathPrefix struct {
	From string
	To   string
}

// NormalizePaths returns a deep copy of v in which each leading path prefix is
// replaced by its placeholder. Empty From entries are dropped and the rest are
// sorted by descending prefix length (stable), so the most specific prefix wins
// even when one prefix is nested inside another (e.g. a run directory under
// $HOME). Only whole leading path segments are replaced: a string is rewritten
// when it equals the prefix or starts with prefix+"/", so paths embedded in
// prose are left untouched.
func NormalizePaths(v any, prefixes ...PathPrefix) (any, error) {
	ordered := make([]PathPrefix, 0, len(prefixes))
	for _, p := range prefixes {
		if p.From != "" {
			ordered = append(ordered, p)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return len(ordered[i].From) > len(ordered[j].From)
	})
	return normalize(v, ordered)
}

func normalize(v any, prefixes []PathPrefix) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nv, err := normalize(val, prefixes)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			nv, err := normalize(item, prefixes)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case string:
		return normalizeString(t, prefixes), nil
	default:
		return v, nil
	}
}

func normalizeString(s string, prefixes []PathPrefix) string {
	for _, p := range prefixes {
		if s == p.From {
			return p.To
		}
		if strings.HasPrefix(s, p.From+"/") {
			return p.To + s[len(p.From):]
		}
	}
	return s
}
