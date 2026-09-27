// Package web exposes the dashboard HTML templates and static assets that ship
// inside the ocbench binary. Template and asset files live under templates/,
// static/ and prototype/ as data only.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates static prototype
var embedded embed.FS

// FS returns the embedded template, static and prototype tree. "templates",
// "static" and "prototype" are direct children.
func FS() fs.FS { return embedded }
