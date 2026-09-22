package canon

import "strings"

// NormalizePaths returns a deep copy of v in which the home prefix is replaced
// by "~" and the run directory prefix by "<run-dir>". Only whole leading path
// segments are replaced: a string is rewritten when it equals the prefix or
// starts with prefix+"/", so paths embedded in prose are left untouched.
func NormalizePaths(v any, home, runDir string) (any, error) {
	replacements := make([][2]string, 0, 2)
	if home != "" {
		replacements = append(replacements, [2]string{home, "~"})
	}
	if runDir != "" {
		replacements = append(replacements, [2]string{runDir, "<run-dir>"})
	}
	return normalize(v, replacements)
}

func normalize(v any, replacements [][2]string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nv, err := normalize(val, replacements)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			nv, err := normalize(item, replacements)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case string:
		return normalizeString(t, replacements), nil
	default:
		return v, nil
	}
}

func normalizeString(s string, replacements [][2]string) string {
	for _, r := range replacements {
		from, to := r[0], r[1]
		if from == "" {
			continue
		}
		if s == from {
			s = to
			continue
		}
		if strings.HasPrefix(s, from+"/") {
			s = to + s[len(from):]
		}
	}
	return s
}
