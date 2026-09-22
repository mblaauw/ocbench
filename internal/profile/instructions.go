package profile

import (
	"os"
	"path/filepath"
)

const instructionsFile = "AGENTS.md"

// readInstructions collects the global OpenCode AGENTS.md and the nearest
// project AGENTS.md at or above dir. Missing files are skipped; any other read
// error is returned. The result is keyed by scope, e.g. "global:AGENTS.md" and
// "project:AGENTS.md".
func readInstructions(home, dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if home != "" {
		global := filepath.Join(home, ".config", "opencode", instructionsFile)
		b, ok, err := readOptional(global)
		if err != nil {
			return nil, err
		}
		if ok {
			out["global:"+instructionsFile] = b
		}
	}
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		for d := abs; ; {
			b, ok, err := readOptional(filepath.Join(d, instructionsFile))
			if err != nil {
				return nil, err
			}
			if ok {
				out["project:"+instructionsFile] = b
				break
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	return out, nil
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
