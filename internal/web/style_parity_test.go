package web

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	webassets "mbl/ocbench/web"
)

// classAttr finds the classes a template puts on an element.
var classAttr = regexp.MustCompile(`class="([^"{}]+)"`)

// styleClass finds every class selector the stylesheet defines.
var styleClass = regexp.MustCompile(`\.([A-Za-z][A-Za-z0-9_-]*)`)

// TestEveryTemplateClassIsStyled is the guard against a template inventing a
// class the stylesheet never defines. The cohort status chip shipped as
// `class="badge"` with no such rule, so the eligibility badge rendered as
// unstyled inline text. A new class must be added to site.css in the same
// commit, or use one that already exists.
func TestEveryTemplateClassIsStyled(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "site.css")
	if err != nil {
		t.Fatalf("read site.css: %v", err)
	}
	defined := map[string]bool{}
	for _, m := range styleClass.FindAllStringSubmatch(string(css), -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("no class selectors found in site.css; the parse is wrong, not the templates")
	}

	var missing []string
	seen := map[string]bool{}
	templates := mustSub(webassets.FS(), "templates")
	err = fs.WalkDir(templates, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".html") {
			return err
		}
		body, err := fs.ReadFile(templates, name)
		if err != nil {
			return err
		}
		for _, m := range classAttr.FindAllStringSubmatch(string(body), -1) {
			for _, class := range strings.Fields(m[1]) {
				if class == "" || defined[class] || seen[class] {
					continue
				}
				seen[class] = true
				missing = append(missing, path.Base(name)+": "+class)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("templates use classes site.css never defines, so they render unstyled:\n  %s",
			strings.Join(missing, "\n  "))
	}
}
