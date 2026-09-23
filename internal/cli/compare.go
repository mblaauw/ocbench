package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

func newCompareCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "compare <a> <b>",
		Short: "Compare two runs by id, latest or previous",
		Long: "Compare two persisted runs. Each selector is a full run UUID, \"latest\" or " +
			"\"previous\" (the prior compatible run). Explicit runs must share suite, task and " +
			"fixture. The report shows metric deltas, validation status changes and exact profile " +
			"component changes; when more than one component changed it recommends a controlled " +
			"comparison and never claims causation. This command is read-only.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			st, err := openHistoryStore(cmd.Context(), resolved)
			if err != nil {
				return err
			}
			defer st.Close()

			cmp, err := history.Compare(cmd.Context(), st, args[0], args[1])
			if err != nil {
				// An invalid/incompatible selector and a missing run are both
				// usage mistakes (exit 2); a missing run is distinguished from
				// other store failures by its wrapped sql.ErrNoRows.
				if errors.Is(err, history.ErrSelector) || errors.Is(err, sql.ErrNoRows) {
					return &UsageError{Err: err}
				}
				return err
			}
			return renderCompare(cmd.OutOrStdout(), cmp, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// validationDelta is one validator whose status differs between the compared
// runs, keyed by (kind, name). Before or After is "" when the validator is
// absent from that side.
type validationDelta struct {
	Kind   string
	Name   string
	Before string
	After  string
}

// validationDeltas unions the validators of both runs by (kind, name), sorted
// by kind then name, and keeps only the entries whose status differs. An absent
// validator is treated as the empty status.
func validationDeltas(before, after []store.ValidationRow) []validationDelta {
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

	out := make([]validationDelta, 0, len(keys))
	for k := range keys {
		b, a := beforeStatus[k], afterStatus[k]
		if b == a {
			continue
		}
		out = append(out, validationDelta{Kind: k.kind, Name: k.name, Before: b, After: a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// renderCompare writes the human or JSON comparison report.
func renderCompare(w io.Writer, cmp history.Comparison, asJSON bool) error {
	if asJSON {
		return renderCompareJSON(w, cmp)
	}
	return renderCompareHuman(w, cmp)
}

// renderCompareHuman prints before/after metadata, metric deltas, validation
// status changes, the profile component diff and the controlled-run warning.
func renderCompareHuman(w io.Writer, cmp history.Comparison) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SIDE\tRUN\tTIME\tTASK\tSTATUS\tPROFILE"); err != nil {
		return err
	}
	for _, side := range []struct {
		label string
		run   store.RunRow
	}{{"before", cmp.Before.Run}, {"after", cmp.After.Run}} {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			side.label, side.run.ID, side.run.StartedAt, side.run.TaskID,
			statusLabel(side.run.Status), shortHash(side.run.ProfileHash)); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\nmetrics"); err != nil {
		return err
	}
	if err := renderMetricDeltas(w, cmp.Metrics); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\nvalidations"); err != nil {
		return err
	}
	if err := renderValidationDeltas(w, validationDeltas(cmp.Before.Validations, cmp.After.Validations)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\nprofile changes"); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, profile.RenderChanges(cmp.ProfileChanges)); err != nil {
		return err
	}

	if cmp.ControlledRunWarning != "" {
		if _, err := fmt.Fprintf(w, "\n%s\n", cmp.ControlledRunWarning); err != nil {
			return err
		}
	}
	return nil
}

// renderMetricDeltas prints the aligned metric table. A nil percent (zero
// baseline) is shown as "n/a" rather than dividing by zero.
func renderMetricDeltas(w io.Writer, deltas []history.MetricDelta) error {
	if len(deltas) == 0 {
		_, err := fmt.Fprintln(w, "  no metrics")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "  METRIC\tBEFORE\tAFTER\tDELTA\tPERCENT"); err != nil {
		return err
	}
	for _, d := range deltas {
		percent := "n/a"
		if d.Percent != nil {
			percent = strconv.FormatFloat(*d.Percent, 'f', 1, 64) + "%"
		}
		if _, err := fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
			d.Name, formatMetricValue(d.Before), formatMetricValue(d.After),
			formatMetricValue(d.Delta), percent); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderValidationDeltas prints one line per changed validator. An absent side
// is shown as "-".
func renderValidationDeltas(w io.Writer, deltas []validationDelta) error {
	if len(deltas) == 0 {
		_, err := fmt.Fprintln(w, "  no validation changes")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, d := range deltas {
		before, after := d.Before, d.After
		if before == "" {
			before = "-"
		}
		if after == "" {
			after = "-"
		}
		if _, err := fmt.Fprintf(tw, "  %s\t%s -> %s\n", validationLabel(d.Kind, d.Name), before, after); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// validationLabel is the human-facing validator key: "kind/name" for named
// validators, "kind" for singletons.
func validationLabel(kind, name string) string {
	if name == "" || name == kind {
		return kind
	}
	return kind + "/" + name
}

// formatMetricValue renders a metric value without trailing zeros.
func formatMetricValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// compareRunJSON is one side's run metadata.
type compareRunJSON struct {
	RunID          string `json:"run_id"`
	StartedAt      string `json:"started_at"`
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	ProfileHash    string `json:"profile_hash"`
	DurationMS     int64  `json:"duration_ms"`
	TokensTotal    int64  `json:"tokens_total"`
	ToolCallsTotal int64  `json:"tool_calls_total"`
}

// compareMetricJSON is one metric delta. Percent is null for a zero baseline.
type compareMetricJSON struct {
	Name    string   `json:"name"`
	Before  float64  `json:"before"`
	After   float64  `json:"after"`
	Delta   float64  `json:"delta"`
	Percent *float64 `json:"percent"`
}

// compareValidationJSON is one validation status change; an absent side is "".
type compareValidationJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// compareProfileChangeJSON is one exact profile component change.
type compareProfileChangeJSON struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Change   string `json:"change"`
	FromHash string `json:"from_hash"`
	ToHash   string `json:"to_hash"`
}

// compareJSON is the stable machine-readable comparison report. Every slice is
// emitted, as `[]` when empty; Warning is always present (empty when there is
// no controlled-run warning).
type compareJSON struct {
	Before         compareRunJSON             `json:"before"`
	After          compareRunJSON             `json:"after"`
	Metrics        []compareMetricJSON        `json:"metrics"`
	Validations    []compareValidationJSON    `json:"validations"`
	ProfileChanges []compareProfileChangeJSON `json:"profile_changes"`
	Warning        string                     `json:"warning"`
}

// renderCompareJSON prints the stable comparison report.
func renderCompareJSON(w io.Writer, cmp history.Comparison) error {
	out := compareJSON{
		Before:         compareRunDetailJSON(cmp.Before),
		After:          compareRunDetailJSON(cmp.After),
		Metrics:        make([]compareMetricJSON, 0, len(cmp.Metrics)),
		Validations:    make([]compareValidationJSON, 0),
		ProfileChanges: make([]compareProfileChangeJSON, 0, len(cmp.ProfileChanges)),
		Warning:        cmp.ControlledRunWarning,
	}
	for _, d := range cmp.Metrics {
		out.Metrics = append(out.Metrics, compareMetricJSON{
			Name: d.Name, Before: d.Before, After: d.After, Delta: d.Delta, Percent: d.Percent,
		})
	}
	for _, d := range validationDeltas(cmp.Before.Validations, cmp.After.Validations) {
		out.Validations = append(out.Validations, compareValidationJSON{
			Kind: d.Kind, Name: d.Name, Before: d.Before, After: d.After,
		})
	}
	for _, c := range cmp.ProfileChanges {
		out.ProfileChanges = append(out.ProfileChanges, compareProfileChangeJSON{
			Kind: c.Kind, Name: c.Name, Change: c.Change, FromHash: c.FromHash, ToHash: c.ToHash,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// compareRunDetailJSON flattens one side's run metadata.
func compareRunDetailJSON(d history.RunDetail) compareRunJSON {
	return compareRunJSON{
		RunID:          d.Run.ID,
		StartedAt:      d.Run.StartedAt,
		TaskID:         d.Run.TaskID,
		Status:         d.Run.Status,
		ProfileHash:    d.Run.ProfileHash,
		DurationMS:     runDurationMS(d.Run),
		TokensTotal:    int64(d.Metrics["tokens_total"]),
		ToolCallsTotal: int64(d.Metrics["tool_calls_total"]),
	}
}
