package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/store"
)

// defaultHistoryLimit bounds `history` when --limit is not supplied. The
// service treats limit <= 0 as "all", but the CLI requires an explicit positive
// bound so output stays predictable.
const defaultHistoryLimit = 20

// openHistoryStore opens the persisted store read-only in spirit: it creates
// the configured directories and applies the schema to a fresh database, but
// never inserts runs or profiles. It never touches the network.
func openHistoryStore(ctx context.Context, d Deps) (*store.Store, error) {
	if err := config.EnsureDirs(d.Paths); err != nil {
		return nil, err
	}
	st, err := store.Open(d.Paths.DB)
	if err != nil {
		return nil, err
	}
	if _, err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func newHistoryCmd(d Deps) *cobra.Command {
	var (
		task   string
		limit  int
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List persisted runs, newest first",
		Long: "List persisted benchmark runs newest first. Filter with --task and bound the " +
			"result with --limit. This command is read-only and never invokes OpenCode.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 {
				return &UsageError{Err: fmt.Errorf("--limit must be at least 1, got %d", limit)}
			}
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			st, err := openHistoryStore(cmd.Context(), resolved)
			if err != nil {
				return err
			}
			defer st.Close()

			runs, err := history.List(cmd.Context(), st, task, limit)
			if err != nil {
				return err
			}
			return renderHistory(cmd.OutOrStdout(), runs, asJSON)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "only show runs for this task id")
	cmd.Flags().IntVar(&limit, "limit", defaultHistoryLimit, "maximum number of runs to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// renderHistory writes the human or JSON history report.
func renderHistory(w io.Writer, runs []history.RunDetail, asJSON bool) error {
	if asJSON {
		return renderHistoryJSON(w, runs)
	}
	return renderHistoryHuman(w, runs)
}

// renderHistoryHuman prints an aligned table with run id, time, task, status,
// profile hash, duration, tokens and tools.
func renderHistoryHuman(w io.Writer, runs []history.RunDetail) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintln(w, "no runs")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "RUN\tTIME\tTASK\tSTATUS\tPROFILE\tDURATION\tTOKENS\tTOOLS"); err != nil {
		return err
	}
	for _, r := range runs {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n",
			r.Run.ID, r.Run.StartedAt, r.Run.TaskID, statusLabel(r.Run.Status),
			shortHash(r.Run.ProfileHash), formatRunDuration(runDurationMS(r.Run)),
			int64(r.Metrics["tokens_total"]), int64(r.Metrics["tool_calls_total"])); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// historyRunJSON is one run in the stable history report. The keys mirror the
// human table so tooling and the terminal show the same fields.
type historyRunJSON struct {
	RunID          string `json:"run_id"`
	StartedAt      string `json:"started_at"`
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	ProfileHash    string `json:"profile_hash"`
	DurationMS     int64  `json:"duration_ms"`
	TokensTotal    int64  `json:"tokens_total"`
	ToolCallsTotal int64  `json:"tool_calls_total"`
}

// historyJSON is the stable machine-readable history report. Runs is always
// emitted, as `[]` when empty.
type historyJSON struct {
	Runs []historyRunJSON `json:"runs"`
}

// renderHistoryJSON prints the stable history report; an empty result is
// `"runs": []`, never null.
func renderHistoryJSON(w io.Writer, runs []history.RunDetail) error {
	out := historyJSON{Runs: make([]historyRunJSON, 0, len(runs))}
	for _, r := range runs {
		out.Runs = append(out.Runs, historyRunJSON{
			RunID:          r.Run.ID,
			StartedAt:      r.Run.StartedAt,
			TaskID:         r.Run.TaskID,
			Status:         r.Run.Status,
			ProfileHash:    r.Run.ProfileHash,
			DurationMS:     runDurationMS(r.Run),
			TokensTotal:    int64(r.Metrics["tokens_total"]),
			ToolCallsTotal: int64(r.Metrics["tool_calls_total"]),
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// runDurationMS dereferences the optional run duration, treating unset as zero.
func runDurationMS(r store.RunRow) int64 {
	if r.DurationMS == nil {
		return 0
	}
	return *r.DurationMS
}
