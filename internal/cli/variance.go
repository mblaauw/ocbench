package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/history"
)

// defaultVarianceRepeats is the per-arm repeat count the report projects onto
// when --repeats is not supplied. It matches the canonical cheap experiment
// (two arms, five repeats), so the printed detectable effect is the one an
// experiment at that size would actually have.
const defaultVarianceRepeats = 5

// newVarianceCmd builds `ocbench variance`: the run-to-run noise floor of each
// task, measured from the runs that repeated the same configuration on it, and
// the effect size those repeats could detect.
//
// It exists because a difference smaller than the noise is not a finding. The
// command is read-only and never invokes OpenCode.
func newVarianceCmd(d Deps) *cobra.Command {
	var (
		repeats int
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "variance [suite] [task...]",
		Short: "Measure the run-to-run noise floor and the detectable effect size",
		Long: "Measure how much a task varies between runs of the same configuration, and " +
			"therefore how large a difference an experiment can detect.\n\n" +
			"A spread is measured only from repeated runs of one configuration on one task: " +
			"running three different configurations once each is not repetition and yields no " +
			"estimate. Tasks without repeats are reported as such rather than averaged.\n\n" +
			"With no arguments the whole store is summarised. Pass a suite name and any number " +
			"of task ids to narrow it. This command is read-only and never invokes OpenCode.",
		Args: usageArgs(cobra.ArbitraryArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if repeats < 1 {
				return &UsageError{Err: fmt.Errorf("--repeats must be at least 1, got %d", repeats)}
			}
			suite := ""
			var tasks []string
			if len(args) > 0 {
				suite = args[0]
				tasks = args[1:]
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

			report, err := history.Variance(cmd.Context(), st, suite, tasks)
			if err != nil {
				return err
			}
			return renderVariance(cmd.OutOrStdout(), report, repeats, asJSON)
		},
	}
	cmd.Flags().IntVar(&repeats, "repeats", defaultVarianceRepeats,
		"repeats per arm to project the detectable effect onto")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// varianceJSON is the stable JSON shape of the report.
type varianceJSON struct {
	TotalRuns      int                `json:"total_runs"`
	SingleRunTasks int                `json:"single_run_tasks"`
	RepeatedTasks  int                `json:"repeated_tasks"`
	Repeats        int                `json:"projected_repeats_per_arm"`
	Tasks          []varianceTaskJSON `json:"tasks"`
}

type varianceTaskJSON struct {
	Suite          string             `json:"suite"`
	Task           string             `json:"task"`
	Runs           int                `json:"runs"`
	Profiles       int                `json:"profiles"`
	RepeatedGroups int                `json:"repeated_groups"`
	Axes           []varianceAxisJSON `json:"axes"`
}

type varianceAxisJSON struct {
	Metric         string  `json:"metric"`
	Groups         int     `json:"groups"`
	Samples        int     `json:"samples"`
	RelativeSD     float64 `json:"relative_sd"`
	DetectableAt   float64 `json:"detectable_at_repeats"`
	RepeatsFor5Pc  int     `json:"repeats_for_5pc"`
	RepeatsFor10Pc int     `json:"repeats_for_10pc"`
}

// renderVariance writes the human or JSON noise report.
func renderVariance(w io.Writer, rep history.VarianceReport, repeats int, asJSON bool) error {
	if asJSON {
		out := varianceJSON{
			TotalRuns:      rep.TotalRuns,
			SingleRunTasks: rep.SingleRunTasks,
			RepeatedTasks:  rep.RepeatedTasks,
			Repeats:        repeats,
			Tasks:          make([]varianceTaskJSON, 0, len(rep.Tasks)),
		}
		for _, tv := range rep.Tasks {
			jt := varianceTaskJSON{
				Suite: tv.Suite, Task: tv.Task, Runs: tv.Runs, Profiles: tv.Profiles,
				RepeatedGroups: tv.RepeatedGroups,
				Axes:           make([]varianceAxisJSON, 0, len(tv.Axes)),
			}
			for _, av := range tv.Axes {
				jt.Axes = append(jt.Axes, varianceAxisJSON{
					Metric: av.Metric, Groups: av.Groups, Samples: av.Samples,
					RelativeSD:     av.RelSD,
					DetectableAt:   av.MDEAt(repeats),
					RepeatsFor5Pc:  av.Repeats5,
					RepeatsFor10Pc: av.Repeats10,
				})
			}
			out.Tasks = append(out.Tasks, jt)
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}

	if rep.TotalRuns == 0 {
		_, err := fmt.Fprintln(w, "No runs recorded. There is no noise to measure yet.")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tAXIS\tGROUPS\tN\tREL SD\tMDE @ "+fmt.Sprint(repeats)+"\tREPEATS 5%\tREPEATS 10%")
	for _, tv := range rep.Tasks {
		if len(tv.Axes) == 0 {
			// Say why there is no estimate rather than printing an empty row:
			// runs spread across configurations are not repeats.
			note := "no repeats"
			if tv.Runs > 1 {
				note = fmt.Sprintf("no repeats (%d configurations, %d runs)", tv.Profiles, tv.Runs)
			}
			fmt.Fprintf(tw, "%s\t%s\t.\t%d\t.\t.\t.\t.\n", tv.Task, note, tv.Runs)
			continue
		}
		for i, av := range tv.Axes {
			task := ""
			if i == 0 {
				task = tv.Task
			}
			marker := ""
			if av.Indicative {
				marker = " *"
			}
			fmt.Fprintf(tw, "%s\t%s%s\t%d\t%d\t%s\t%s\t%s\t%s\n",
				task, av.Metric, marker, av.Groups, av.Samples,
				percent(av.RelSD), percent(av.MDEAt(repeats)),
				repeatsOrUnknown(av.Repeats5), repeatsOrUnknown(av.Repeats10))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n%d run(s) across %d task(s): %d repeated, %d measured once.\n",
		rep.TotalRuns, len(rep.Tasks), rep.RepeatedTasks, rep.SingleRunTasks)
	if rep.RepeatedTasks == 0 {
		fmt.Fprintln(w, "No task has been repeated, so no difference between configurations can "+
			"be distinguished from run-to-run noise. Re-run a configuration before comparing.")
	} else if rep.SingleRunTasks > 0 {
		fmt.Fprintf(w, "%d task(s) have no noise estimate; a difference measured on them is not "+
			"distinguishable from noise.\n", rep.SingleRunTasks)
	}
	fmt.Fprintf(w, "REL SD is the pooled within-configuration spread; MDE @ %d is the smallest "+
		"relative difference %d repeats per arm could detect.\n", repeats, repeats)
	fmt.Fprintln(w, "A '.' means the metric never varied, so no effect size can be estimated from it.")
	if indicative(rep) {
		fmt.Fprintln(w, "A '*' means the spread rests on fewer than five observations, so it is "+
			"itself uncertain by 35% or more: treat it as a reason to run more, not as a result.")
	}
	return nil
}

// indicative reports whether any axis in the report rests on a thin spread.
func indicative(rep history.VarianceReport) bool {
	for _, tv := range rep.Tasks {
		for _, av := range tv.Axes {
			if av.Indicative {
				return true
			}
		}
	}
	return false
}

// percent renders a fraction as a percentage, or "." when there is no estimate.
func percent(fraction float64) string {
	if fraction <= 0 {
		return "."
	}
	return fmt.Sprintf("%.1f%%", fraction*100)
}

// repeatsOrUnknown renders a repeat count, or "." when the observed spread
// cannot support an estimate.
func repeatsOrUnknown(n int) string {
	if n <= 0 {
		return "."
	}
	return fmt.Sprint(n)
}
