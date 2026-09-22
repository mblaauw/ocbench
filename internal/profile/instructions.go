package profile

import (
	"os"
	"path/filepath"
)

const instructionsFile = "AGENTS.md"

// readInstructions collects the global OpenCode AGENTS.md and the nearest
// project AGENTS.md at or above dir. Missing files are skipped; any other read
// error is returned. The result is keyed by scope, e.g. "global:AGENTS.md" and
// "project:AGENTS.md"; the parallel paths map holds the absolute source path of
// each scope for the canonical `path` field.
func readInstructions(home, dir string) (map[string][]byte, map[string]string, error) {
	out := map[string][]byte{}
	paths := map[string]string{}
	if home != "" {
		global := filepath.Join(home, ".config", "opencode", instructionsFile)
		b, ok, err := readOptional(global)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			scope := "global:" + instructionsFile
			out[scope] = b
			paths[scope] = global
		}
	}
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, nil, err
		}
		for d := abs; ; {
			candidate := filepath.Join(d, instructionsFile)
			b, ok, err := readOptional(candidate)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				scope := "project:" + instructionsFile
				out[scope] = b
				paths[scope] = candidate
				break
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	return out, paths, nil
}

// readOptional returns (bytes, true, nil) when path exists, (nil, false, nil)
// when it does not, and an error otherwise.
func readOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}
