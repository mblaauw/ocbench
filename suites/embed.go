// Package suites exposes the benchmark suites that ship inside the ocbench
// binary. Suite content lives under core/ as data files only.
package suites

import (
	"embed"
	"io/fs"
)

//go:embed core
var embedded embed.FS

// FS returns the embedded suite tree. Suite directories such as "core" are
// direct children of the returned filesystem.
func FS() fs.FS { return embedded }
