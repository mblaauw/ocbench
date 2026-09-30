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

// templateAction matches one Go template action. It is replaced by a space
// before classes are read, so a conditional class attribute contributes every
// alternative it can render: `class="tag {{if .Ok}}good{{else}}warn{{end}}"`
// yields the classes tag, good and warn. Without this the guard would miss the
// very bug it exists for, because the unstyled class lived inside an action.
var templateAction = regexp.MustCompile(`\{\{[^}]*\}\}`)

// classAttr finds the classes a template puts on an element. It runs on a copy
// of the template with the actions already replaced.
var classAttr = regexp.MustCompile(`class="([^"]*)"`)

// styleClass finds every class selector the stylesheet defines.
var styleClass = regexp.MustCompile(`\.([A-Za-z][A-Za-z0-9_-]*)`)

// hasClassWithPrefix reports whether the stylesheet defines any class the given
// token is a prefix of.
func hasClassWithPrefix(defined map[string]bool, prefix string) bool {
	for class := range defined {
		if class != prefix && strings.HasPrefix(class, prefix) {
			return true
		}
	}
	return false
}

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
		plain := templateAction.ReplaceAll(body, []byte(" "))
		for _, m := range classAttr.FindAllStringSubmatch(string(plain), -1) {
			for _, class := range strings.Fields(m[1]) {
				if class == "" || defined[class] || seen[class] {
					continue
				}
				// A token ending in a hyphen is the static half of a composed
				// name, like `tint-` in `class="cell tint-{{.Tint}}"` where the
				// stylesheet defines tint-0..tint-4. Only skip it when such a
				// family really exists.
				if strings.HasSuffix(class, "-") && hasClassWithPrefix(defined, class) {
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
