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

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

// defaultListLimit bounds the `/` page. It mirrors the CLI history default.
const defaultListLimit = 20

// contentSecurityPolicy is deliberately restrictive: no scripts, no external
// origins, no framing and no form submissions. Styles are the only same-origin
// resource the dashboard loads.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// handler serves the dashboard from an immutable store handle.
type handler struct {
	store *store.Store
}

// NewHandler returns the read-only dashboard handler backed by st. It exposes
// `GET /` (the profile leaderboard), `GET /runs`, `GET /runs/{id}`,
// `GET /compare`, `GET /profiles/{hash}`, the embedded `/static/` assets and a
// 404 for everything else. It never serves raw artifacts or arbitrary
// filesystem paths.
func NewHandler(st *store.Store) http.Handler {
	h := &handler{store: st}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleOverview)
	mux.HandleFunc("GET /runs", h.handleList)
	mux.HandleFunc("GET /runs/{id}", h.handleRun)
	mux.HandleFunc("GET /compare", h.handleCompare)
	mux.HandleFunc("GET /profiles/{hash}", h.handleProfile)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("/", http.NotFound)

	return securityHeaders(dirtyPathGuard(mux))
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
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// handleList renders the recent-runs page.
func (h *handler) handleList(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	runs, err := history.List(r.Context(), h.store, "", defaultListLimit)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	page := runsPage{
		layout: h.page(r, "runs", "History", "Runs",
			"Every run persisted to the store, newest first."),
		Runs: make([]runSummary, 0, len(runs)),
	}
	for _, detail := range runs {
		page.Runs = append(page.Runs, newRunSummary(detail))
	}
	render(w, listTmpl, page)
}

// handleRun renders one run's safe result summary: metadata, numeric metrics,
// validation excerpts and redacted profile component hashes. It never serves
// artifacts_dir, session ids or raw file content.
func (h *handler) handleRun(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	detail, err := history.Get(r.Context(), h.store, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}

	render(w, runTmpl, runPage{
		layout:      h.page(r, "runs", "History", "Run "+detail.Run.ID, detail.Run.TaskID),
		Run:         newRunSummary(detail),
		Metrics:     metricViews(detail.Metrics),
		Validations: validationViews(detail.Validations),
		Profile:     componentViews(detail.Profile),
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

// handleProfile renders a redacted profile view: hash, version and the kind,
// name and hash of each component, never the canonical JSON.
func (h *handler) handleProfile(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	row, comps, err := h.store.GetProfileByHash(r.Context(), r.PathValue("hash"))
	if err != nil {
		writeStoreError(w, err)
		return
	}

	render(w, profileTmpl, profilePage{
		layout:          h.page(r, "profiles", "Profiles", "Profile "+shortHash(row.ProfileHash), row.OpenCodeVersion),
		ProfileHash:     row.ProfileHash,
		OpenCodeVersion: row.OpenCodeVersion,
		Components:      componentRowViews(comps),
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

// runsPage backs the `/` listing.
type runsPage struct {
	layout
	Runs []runSummary
}

// runPage backs a single-run summary.
type runPage struct {
	layout
	Run         runSummary
	Metrics     []metricView
	Validations []validationView
	Profile     []componentView
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

// profilePage backs a redacted profile view.
type profilePage struct {
	layout
	ProfileHash     string
	OpenCodeVersion string
	Components      []componentView
	Changes         []changeView
}

// runSummary is the explicit, safe projection of a run for every view. It
// deliberately omits artifacts_dir, session_id, raw error internals and any
// file content.
type runSummary struct {
	ID           string
	TaskID       string
	TaskVersion  string
	Status       string
	DryRun       bool
	StartedAt    string
	FinishedAt   string
	DurationMS   int64
	SuiteName    string
	SuiteVersion string
	Model        string
	Agent        string
	ProfileHash  string
	TokensTotal  float64
	ToolCalls    float64
	FilesChanged float64
	DiffAdded    float64
	DiffRemoved  float64
}

// metricView is one numeric metric.
type metricView struct {
	Name  string
	Value float64
}

// validationView is one validation with its excerpt, never its full output.
type validationView struct {
	Kind          string
	Name          string
	Status        string
	ExitCode      int
	DurationMS    int64
	OutputExcerpt string
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

// newRunSummary projects a history detail onto the safe list/detail fields.
func newRunSummary(detail history.RunDetail) runSummary {
	return runSummary{
		ID:           detail.Run.ID,
		TaskID:       detail.Run.TaskID,
		TaskVersion:  detail.Run.TaskVersion,
		Status:       detail.Run.Status,
		DryRun:       detail.Run.DryRun,
		StartedAt:    detail.Run.StartedAt,
		FinishedAt:   detail.Run.FinishedAt,
		DurationMS:   runDurationMS(detail.Run),
		SuiteName:    detail.Run.SuiteName,
		SuiteVersion: detail.Run.SuiteVersion,
		Model:        detail.Run.Model,
		Agent:        detail.Run.Agent,
		ProfileHash:  detail.Run.ProfileHash,
		TokensTotal:  detail.Metrics["tokens_total"],
		ToolCalls:    detail.Metrics["tool_calls_total"],
		FilesChanged: detail.Metrics["files_changed"],
		DiffAdded:    detail.Metrics["diff_lines_added"],
		DiffRemoved:  detail.Metrics["diff_lines_removed"],
	}
}

// runDurationMS dereferences the optional run duration, treating unset as zero.
func runDurationMS(r store.RunRow) int64 {
	if r.DurationMS == nil {
		return 0
	}
	return *r.DurationMS
}

// metricViews flattens a run's numeric metrics into a name-sorted slice so the
// rendered table is deterministic.
func metricViews(metrics map[string]float64) []metricView {
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]metricView, 0, len(names))
	for _, name := range names {
		out = append(out, metricView{Name: name, Value: metrics[name]})
	}
	return out
}

// validationViews projects validations onto the excerpt-only view.
func validationViews(vals []store.ValidationRow) []validationView {
	out := make([]validationView, 0, len(vals))
	for _, v := range vals {
		out = append(out, validationView{
			Kind:          v.Kind,
			Name:          v.Name,
			Status:        v.Status,
			ExitCode:      v.ExitCode,
			DurationMS:    v.DurationMS,
			OutputExcerpt: v.OutputExcerpt,
		})
	}
	return out
}

// componentViews projects a profile's components onto their redacted summary.
func componentViews(p *profile.Profile) []componentView {
	if p == nil {
		return nil
	}
	out := make([]componentView, 0, len(p.Components))
	for _, c := range p.Components {
		out = append(out, componentView{Kind: c.Kind, Name: c.Name, Hash: c.Hash})
	}
	return out
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
