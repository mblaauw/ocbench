// Package experiment owns A/B config-overlay experiments: arm overlays, the
// interleaved execution plan, orchestration over the runner, aggregation and
// the regression decision.
package experiment

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mbl/ocbench/internal/canon"
)

// OverlayKind names the kind of config overlay an arm declares.
type OverlayKind string

const (
	// OverlayNone is the unmodified resolved profile.
	OverlayNone OverlayKind = "none"
	// OverlayFile is a custom config file merged above the global config.
	OverlayFile OverlayKind = "file"
	// OverlayDir is an agents/commands/modes/plugins directory merged above
	// .opencode.
	OverlayDir OverlayKind = "dir"
)

// Overlay is a resolved config overlay. Path and SHA256 are empty for
// OverlayNone; Env carries the ocbench-controlled variables to append after the
// sandbox allowlist.
type Overlay struct {
	Kind   OverlayKind
	Path   string
	SHA256 string
	Env    []string
}

// ResolveOverlay resolves path into an Overlay. An empty path means "none" and
// yields no environment. A regular file becomes OPENCODE_CONFIG=<abs>; a
// directory becomes OPENCODE_CONFIG_DIR=<abs>. Any other existing path (or a
// missing one) is an error naming the path. SHA256 is the file's bytes, or for
// a directory a lexical walk hashing relative path + contents.
func ResolveOverlay(path string) (Overlay, error) {
	if path == "" {
		return Overlay{Kind: OverlayNone}, nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Overlay{}, fmt.Errorf("overlay %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Overlay{}, fmt.Errorf("overlay %s: %w", path, err)
	}

	switch {
	case info.Mode().IsRegular():
		b, err := os.ReadFile(abs)
		if err != nil {
			return Overlay{}, fmt.Errorf("overlay %s: %w", path, err)
		}
		return Overlay{
			Kind:   OverlayFile,
			Path:   abs,
			SHA256: canon.SHA256Hex(b),
			Env:    []string{"OPENCODE_CONFIG=" + abs},
		}, nil
	case info.IsDir():
		sum, err := overlayDirHash(abs)
		if err != nil {
			return Overlay{}, fmt.Errorf("overlay %s: %w", path, err)
		}
		return Overlay{
			Kind:   OverlayDir,
			Path:   abs,
			SHA256: sum,
			Env:    []string{"OPENCODE_CONFIG_DIR=" + abs},
		}, nil
	default:
		return Overlay{}, fmt.Errorf("overlay %s: not a regular file or directory", abs)
	}
}

// overlayDirHash hashes a directory tree the way suite.manifestHash hashes a
// file map: SHA256 over the sorted "relpath\0sha256(contents)" entries joined
// by newlines. Only regular files contribute; relative slash paths keep the
// hash location independent.
func overlayDirHash(dir string) (string, error) {
	var entries []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel)+"\x00"+canon.SHA256Hex(b))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	return canon.SHA256Hex([]byte(strings.Join(entries, "\n"))), nil
}
