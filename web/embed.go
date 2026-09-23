// Package web exposes the dashboard HTML templates and static assets that ship
// inside the ocbench binary. Template and asset files live under templates/ and
// static/ as data only.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var embedded embed.FS

// FS returns the embedded template and static tree. "templates" and "static"
// are direct children.
func FS() fs.FS { return embedded }
