// Package web is the read-only ocbench dashboard. It serves embedded HTML
// templates and static assets from a store-backed history read model. It never
// writes to the store, never serves raw run artifacts or arbitrary filesystem
// paths, and never touches the network.
package web

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

// defaultListLimit bounds the `/` page. It mirrors the CLI history default.
const defaultListLimit = 20

// contentSecurityPolicy permits the dashboard's single first-party deferred
// enhancement script while forbidding inline code, third-party origins and eval.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// prototypeContentSecurityPolicy governs the design prototype only. It is
// deliberately weaker than the dashboard's: the prototype is a React
// application whose template runtime compiles code with new Function, whose
// markup is built from inline styles, and whose fonts and React build come from
// a CDN. None of that is allowed on the dashboard's own pages, and none of it
// needs to be, because the prototype is a development reference served on
// loopback.
const prototypeContentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self' 'unsafe-eval' 'unsafe-inline' https://unpkg.com; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	"connect-src 'self' https://unpkg.com; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// handler serves the dashboard from an immutable store handle.
type handler struct {
	store *store.Store
	// paths locates the per-profile capture files. It is optional: without it
	// the dashboard renders everything except the captured text.
	paths config.Paths
}

// Option adjusts the handler the dashboard is served with.
type Option func(*handler)

// WithPaths tells the handler where the profile capture files live, which is
// what lets the architecture page show prompt and instruction text.
func WithPaths(paths config.Paths) Option {
	return func(h *handler) { h.paths = paths }
}

// NewHandler returns the read-only dashboard handler backed by st. It exposes
// the profile overview, controlled cohorts, runs, architecture, suites and the
// embedded static assets. It never serves raw artifacts or arbitrary filesystem
// paths.
func NewHandler(st *store.Store, opts ...Option) http.Handler {
	h := &handler{store: st}
	for _, opt := range opts {
		opt(h)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /overview", h.handleOverview)
	mux.HandleFunc("GET /runs", h.handleList)
	mux.HandleFunc("GET /runs/{id}", h.handleRun)
	mux.HandleFunc("GET /compare", h.handleCompare)
	mux.HandleFunc("GET /arch", h.handleProfiles)
	mux.HandleFunc("GET /arch/{hash}", h.handleProfile)
	mux.HandleFunc("GET /suites", h.handleSuites)
	mux.HandleFunc("GET /cohorts", h.handleCohorts)
	mux.HandleFunc("GET /cohorts/{id}", h.handleCohort)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	// The design prototype, for comparing the dashboard against the canvas it
	// was built from. It is a development reference, not part of the dashboard.
	mux.Handle("GET /prototype/", http.StripPrefix("/prototype/",
		http.FileServerFS(prototypeFS)))
	mux.HandleFunc("/", http.NotFound)

	return securityHeaders(dirtyPathGuard(mux))
}

// handleRoot keeps the cohort landing page canonical while preserving legacy
// root overview scope links. A scope has meaning only for exploratory history,
// so legacy root links redirect instead of silently changing meaning.
func (h *handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("scope") != "" {
		http.Redirect(w, r, "/overview?"+r.URL.Query().Encode(), http.StatusFound)
		return
	}
	h.handleCohorts(w, r)
}

// dirtyPathGuard rejects any request whose path contains a "." or ".." segment
// before ServeMux can redirect it to a cleaned path. Without this, requests like
// /runs/../ocbench.db would be answered with a 307 to /ocbench.db instead of a
// 404, leaking the existence of routes and never matching a handler.
func dirtyPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasDirtySegment(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasDirtySegment reports whether any slash-separated segment of p is "." or
// "..". Run ids and profile hashes never contain those segments.
func hasDirtySegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// writeStoreError maps a service error to the dashboard's status: a missing
// row is 404, an invalid/incompatible selector is 400, a SQLite lock is a
// retryable 503, and anything else is a generic 500. No error detail is echoed
// to the client.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, history.ErrSelector):
		http.Error(w, "invalid comparison selector", http.StatusBadRequest)
	case store.IsBusy(err):
		http.Error(w, "store busy, try again", http.StatusServiceUnavailable)
	default:
		http.Error(w, "failed to read history", http.StatusInternalServerError)
	}
}

// securityHeaders applies the response hardening required for every dashboard
// response, including 404s.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/prototype/") {
			w.Header().Set("Content-Security-Policy", prototypeContentSecurityPolicy)
		} else {
			w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// handleCompare mirrors the CLI comparison: both selectors are required, and
// the shared service rejects missing runs (404) and invalid or incompatible
// selectors (400).
func (h *handler) handleCompare(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	a := r.URL.Query().Get("a")
	b := r.URL.Query().Get("b")
	if a == "" || b == "" {
		http.Error(w, "both a and b selectors are required", http.StatusBadRequest)
		return
	}

	cmp, err := history.Compare(r.Context(), h.store, a, b)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Name the likely cause: a selector matched no run, or `previous`
			// found no compatible earlier run for the same task and suite
			// version, which is common right after a task is edited.
			http.Error(w, fmt.Sprintf(
				"no run matched %q or %q: a selector must name a run id, `latest` or `previous`, and `previous` needs an earlier run of the same task, suite version and fixture",
				a, b), http.StatusNotFound)
			return
		}
		writeStoreError(w, err)
		return
	}

	render(w, compareTmpl, comparePage{
		layout:         h.page(r, "runs", "Compare", "Compare two runs", cmp.Before.Run.TaskID+" → "+cmp.After.Run.TaskID),
		Before:         newRunSummary(cmp.Before),
		After:          newRunSummary(cmp.After),
		Metrics:        metricDeltaViews(cmp.Metrics),
		Validations:    validationDeltaViews(cmp.Before.Validations, cmp.After.Validations),
		ProfileChanges: changeViews(cmp.ProfileChanges),
		Warning:        cmp.ControlledRunWarning,
	})
}

// render executes a page template into a buffer first, so a template error
// yields a clean 500 instead of a partially written body.
func render(w http.ResponseWriter, tmpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = buf.WriteTo(w)
}

// comparePage backs a run comparison.
type comparePage struct {
	layout
	Before         runSummary
	After          runSummary
	Metrics        []metricDeltaView
	Validations    []validationDeltaView
	ProfileChanges []changeView
	Warning        string
}

// runSummary is the explicit, safe projection of a run for every view. It
// deliberately omits artifacts_dir, session_id, raw error internals and any
// file content.
type runSummary struct {
	ID          string
	TaskID      string
	Status      string
	ProfileHash string
}

// componentView is the redacted summary of one profile component: kind, name
// and content hash only, never the canonical JSON.
type componentView struct {
	Kind string
	Name string
	Hash string
}

// metricDeltaView is one metric's change between two runs.
type metricDeltaView struct {
	Name    string
	Before  float64
	After   float64
	Delta   float64
	Percent *float64
}

// validationDeltaView is one validator whose status differs between two runs.
type validationDeltaView struct {
	Kind   string
	Name   string
	Before string
	After  string
}

// changeView is one profile component change between two runs.
type changeView struct {
	Kind   string
	Name   string
	Change string
	From   string
	To     string
}

// newRunSummary projects a history detail onto the safe comparison fields.
func newRunSummary(detail history.RunDetail) runSummary {
	return runSummary{
		ID:          detail.Run.ID,
		TaskID:      detail.Run.TaskID,
		Status:      detail.Run.Status,
		ProfileHash: detail.Run.ProfileHash,
	}
}

// componentRowViews projects store component rows onto their redacted summary.
func componentRowViews(comps []store.ComponentRow) []componentView {
	out := make([]componentView, 0, len(comps))
	for _, c := range comps {
		out = append(out, componentView{Kind: c.Kind, Name: c.Name, Hash: c.Hash})
	}
	return out
}

// changeViews projects profile changes onto the hash-only view.
func changeViews(changes []profile.Change) []changeView {
	out := make([]changeView, 0, len(changes))
	for _, c := range changes {
		out = append(out, changeView{
			Kind:   c.Kind,
			Name:   c.Name,
			Change: c.Change,
			From:   c.FromHash,
			To:     c.ToHash,
		})
	}
	return out
}

// metricDeltaViews projects service metric deltas onto the view model.
func metricDeltaViews(deltas []history.MetricDelta) []metricDeltaView {
	out := make([]metricDeltaView, 0, len(deltas))
	for _, d := range deltas {
		out = append(out, metricDeltaView{
			Name:    d.Name,
			Before:  d.Before,
			After:   d.After,
			Delta:   d.Delta,
			Percent: d.Percent,
		})
	}
	return out
}

// validationDeltaViews lists validators whose status changed, keyed by
// (kind, name) and sorted by kind then name. A validator absent from one side
// is shown with an empty status.
func validationDeltaViews(before, after []store.ValidationRow) []validationDeltaView {
	type key struct{ kind, name string }
	beforeStatus := make(map[key]string, len(before))
	afterStatus := make(map[key]string, len(after))
	keys := make(map[key]struct{}, len(before)+len(after))
	for _, v := range before {
		k := key{v.Kind, v.Name}
		beforeStatus[k] = v.Status
		keys[k] = struct{}{}
	}
	for _, v := range after {
		k := key{v.Kind, v.Name}
		afterStatus[k] = v.Status
		keys[k] = struct{}{}
	}

	out := make([]validationDeltaView, 0, len(keys))
	for k := range keys {
		b, a := beforeStatus[k], afterStatus[k]
		if b == a {
			continue
		}
		out = append(out, validationDeltaView{Kind: k.kind, Name: k.name, Before: b, After: a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}
