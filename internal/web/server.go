// Package web is the read-only ocbench dashboard. It serves embedded HTML
// templates and static assets from a store-backed history read model. It never
// writes to the store, never serves raw run artifacts or arbitrary filesystem
// paths, and never touches the network.
package web

import (
	"bytes"
	"html/template"
	"net/http"

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/store"
)

// defaultListLimit bounds the `/` page. It mirrors the CLI history default.
const defaultListLimit = 20

// contentSecurityPolicy is deliberately restrictive: no scripts, no external
// origins, no framing and no form submissions. Styles are the only same-origin
// resource the dashboard loads.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// handler serves the dashboard from an immutable store handle.
type handler struct {
	store *store.Store
}

// NewHandler returns the read-only dashboard handler backed by st. It exposes
// only `GET /`, the embedded `/static/` assets and a 404 for everything else.
func NewHandler(st *store.Store) http.Handler {
	h := &handler{store: st}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleList)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("/", http.NotFound)

	return securityHeaders(mux)
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
		http.Error(w, "failed to list runs", http.StatusInternalServerError)
		return
	}

	page := runsPage{Title: "Runs", Runs: make([]runSummary, 0, len(runs))}
	for _, detail := range runs {
		page.Runs = append(page.Runs, newRunSummary(detail))
	}
	render(w, listTmpl, page)
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
	Title string
	Runs  []runSummary
}

// runPage backs a single-run summary.
type runPage struct {
	Title       string
	Run         runSummary
	Metrics     []metricView
	Validations []validationView
	Profile     []componentView
}

// comparePage backs a run comparison.
type comparePage struct {
	Title          string
	Before         runSummary
	After          runSummary
	Metrics        []metricDeltaView
	Validations    []validationDeltaView
	ProfileChanges []changeView
	Warning        string
}

// profilePage backs a redacted profile view.
type profilePage struct {
	Title           string
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
