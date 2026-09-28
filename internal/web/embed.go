package web

import (
	"html/template"
	"io/fs"

	webassets "mbl/ocbench/web"
)

// baseTemplate is the shared layout file. Every page is parsed into its own
// isolated template set together with baseTemplate, so a page's "content" block
// can never collide with another page's.
const baseTemplate = "templates/base.html"

// overviewTmpl renders the profile leaderboard at `/`.
var overviewTmpl = mustParsePage("templates/overview.html")

// listTmpl renders the `/runs` listing.
var listTmpl = mustParsePage("templates/runs.html")

// suitesTmpl renders the persisted suite catalogue at `/suites`.
var suitesTmpl = mustParsePage("templates/suites.html")

// cohortsTmpl and cohortTmpl render controlled experiment cohorts and their
// evidence-gated efficiency standings.
var (
	cohortsTmpl = mustParsePage("templates/cohorts.html")
	cohortTmpl  = mustParsePage("templates/cohort.html")
)

// runTmpl, compareTmpl and profileTmpl render the detail views that the
// dashboard routes task wires up. Parsing them here means a malformed template
// fails at startup rather than per request.
var (
	compareTmpl  = mustParsePage("templates/compare.html")
	profileTmpl  = mustParsePage("templates/profile.html")
	profilesTmpl = mustParsePage("templates/profiles.html")
)

// staticFS is the embedded static/ subtree.
var staticFS = mustSub(webassets.FS(), "static")

// prototypeFS is the design prototype, kept for comparing the dashboard against
// the canvas it was built from. It is served only at /prototype/ and under its
// own content security policy: it is a React application that needs a CDN, an
// eval-based template runtime and inline styles, none of which the dashboard
// itself may use.
var prototypeFS = mustSub(webassets.FS(), "prototype")

// mustParsePage parses baseTemplate plus one page file into an isolated
// template set.
func mustParsePage(page string) *template.Template {
	return template.Must(template.ParseFS(webassets.FS(), baseTemplate, page))
}

// mustSub returns fsys rooted at dir, panicking on failure because the embedded
// tree is fixed at build time.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("web: sub " + dir + ": " + err.Error())
	}
	return sub
}
