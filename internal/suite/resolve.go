package suite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"mbl/ocbench/internal/config"
)

// Source describes where a resolved suite came from.
type Source struct {
	Name     string
	Embedded bool
	Dir      string
}

// ListSources returns the suites found directly under fsys, sorted by name.
// It is used to enumerate the embedded suites.
func ListSources(fsys fs.FS) ([]Source, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list suites: %w", err)
	}
	sources := make([]Source, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(fsys, path.Join(entry.Name(), "suite.yaml")); err != nil {
			continue
		}
		sources = append(sources, Source{Name: entry.Name(), Embedded: true})
	}
	return sources, nil
}

// Resolve loads the named suite following the documented precedence: an
// explicit --suite-dir, then $OCBENCH_HOME/suites/<name>, then the embedded
// fsys. When nothing is found the error names every location tried.
func Resolve(fsys fs.FS, paths config.Paths, name, suiteDirFlag string) (*Suite, Source, error) {
	if name == "" {
		return nil, Source{}, fmt.Errorf("resolve suite: name is required")
	}

	if suiteDirFlag != "" {
		abs, err := filepath.Abs(suiteDirFlag)
		if err != nil {
			return nil, Source{}, fmt.Errorf("suite dir %q: %w", suiteDirFlag, err)
		}
		s, err := LoadDir(abs)
		if err != nil {
			return nil, Source{}, fmt.Errorf("resolve suite %q from --suite-dir %s: %w", name, abs, err)
		}
		return s, Source{Name: name, Dir: abs}, nil
	}

	var tried []string
	if paths.Suites != "" {
		dir := filepath.Join(paths.Suites, name)
		tried = append(tried, dir)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			s, err := LoadDir(dir)
			if err != nil {
				return nil, Source{}, fmt.Errorf("resolve suite %q from %s: %w", name, dir, err)
			}
			return s, Source{Name: name, Dir: dir}, nil
		}
	}

	tried = append(tried, fmt.Sprintf("embedded suite %q", name))
	if fsys != nil {
		if _, err := fs.Stat(fsys, path.Join(name, "suite.yaml")); err == nil {
			s, err := LoadFS(fsys, name)
			if err != nil {
				return nil, Source{}, fmt.Errorf("resolve suite %q from embedded: %w", name, err)
			}
			return s, Source{Name: name, Embedded: true}, nil
		}
	}
	return nil, Source{}, fmt.Errorf("suite %q not found; tried %s", name, strings.Join(tried, ", "))
}

// Export writes every file of s into dest, preserving relative paths. dest must
// not exist or must be an empty directory; a non-empty destination is refused
// so an export can never silently mix with existing files.
func Export(s *Suite, dest string) error {
	if s == nil || s.FS == nil {
		return errors.New("export: suite has no filesystem")
	}
	if err := prepareDest(dest); err != nil {
		return err
	}
	return fs.WalkDir(s.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(s.FS, p)
		if err != nil {
			return fmt.Errorf("export %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// prepareDest ensures dest is an empty directory (creating it if absent) and
// errors when it is a file or already holds entries.
func prepareDest(dest string) error {
	if dest == "" {
		return errors.New("export: destination is empty")
	}
	info, err := os.Stat(dest)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("export: destination %s is not a directory", dest)
		}
		entries, err := os.ReadDir(dest)
		if err != nil {
			return fmt.Errorf("export: read destination %s: %w", dest, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("export: destination %s is not empty", dest)
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("export: create destination %s: %w", dest, err)
		}
		return nil
	default:
		return fmt.Errorf("export: stat destination %s: %w", dest, err)
	}
}
