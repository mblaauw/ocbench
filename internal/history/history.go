// Package history is the shared, read-only model for run history and run
// comparison. The CLI and the web dashboard both consume it. It performs no
// writes, never calls OpenCode, and never touches the network.
package history

import (
	"context"
	"errors"

	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

// SelectorLatest and SelectorPrevious are the reserved selector keywords.
// Any other selector is treated as a full run UUID.
const (
	SelectorLatest   = "latest"
	SelectorPrevious = "previous"
)

// ErrSelector marks a selector that is invalid or that selects runs which
// cannot be compared. Callers treat it as a usage error (CLI exit 2, HTTP 400).
// It is deliberately distinct from a missing run, whose store error wraps
// sql.ErrNoRows (CLI exit 2, HTTP 404).
var ErrSelector = errors.New("invalid selector")

// RunDetail is one run with everything a history or comparison view needs.
// Metrics holds only numeric metrics; a text-only metric is omitted. Metrics
// and Validations are normalised to non-nil empty values, and Profile is nil
// only when the run has no profile id.
type RunDetail struct {
	Run         store.RunRow
	Metrics     map[string]float64
	Validations []store.ValidationRow
	Profile     *profile.Profile
}

// List returns up to limit runs newest-first, optionally filtered by task. A
// limit <= 0 returns every match. The returned slice is never nil.
func List(ctx context.Context, st *store.Store, task string, limit int) ([]RunDetail, error) {
	if st == nil {
		return nil, errors.New("history: nil store")
	}
	runs, err := st.ListRuns(ctx, limit, task)
	if err != nil {
		return nil, err
	}
	out := make([]RunDetail, 0, len(runs))
	for _, run := range runs {
		detail, err := loadDetail(ctx, st, run)
		if err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, nil
}

// loadDetail hydrates one run, normalising nil slices and maps to empty ones so
// every RunDetail is safe to serialise as `[]`/`{}` rather than `null`.
func loadDetail(ctx context.Context, st *store.Store, run store.RunRow) (RunDetail, error) {
	metrics, err := st.GetRunMetrics(ctx, run.ID)
	if err != nil {
		return RunDetail{}, err
	}
	vals, err := st.ListRunValidations(ctx, run.ID)
	if err != nil {
		return RunDetail{}, err
	}

	detail := RunDetail{
		Run:         run,
		Metrics:     make(map[string]float64, len(metrics)),
		Validations: make([]store.ValidationRow, 0, len(vals)),
	}
	for _, m := range metrics {
		if m.ValueNum != nil {
			detail.Metrics[m.Name] = *m.ValueNum
		}
	}
	detail.Validations = append(detail.Validations, vals...)

	if run.ProfileID != "" {
		row, comps, err := st.GetProfileByID(ctx, run.ProfileID)
		if err != nil {
			return RunDetail{}, err
		}
		p, err := profile.FromRows(row, comps)
		if err != nil {
			return RunDetail{}, err
		}
		detail.Profile = p
	}
	return detail, nil
}
