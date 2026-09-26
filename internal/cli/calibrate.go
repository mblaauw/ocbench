package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/history"
)

// newCalibrateCmd builds `ocbench calibrate`: how much each task can actually
// separate two configurations.
//
// It answers a different question from `variance`. Variance asks how large a
// difference the data could detect; calibration asks whether the task is
// capable of showing one at all. A task every configuration passes measures
// nothing about quality, and a task every configuration solves the same way
// measures nothing about strategy, however hard it looks.
//
// The command is read-only and never invokes OpenCode.
func newCalibrateCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "calibrate [suite]",
		Short: "Classify tasks by how much they can separate configurations",
		Long: "Classify every task by pass rate and by whether configurations actually behaved " +
			"differently on it.\n\n" +
			"A task whose runs all passed is saturated: it cannot show a quality difference because " +
			"there is none to show. A task whose runs all failed separates nothing either. A task " +
			"measured once cannot be classified at all. Separately, a task where every configuration " +
			"took the same route cannot separate them on strategy however hard it is.\n\n" +
			"This command is read-only and never invokes OpenCode.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := ""
			if len(args) > 0 {
				suite = args[0]
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

			report, err := history.Calibration(cmd.Context(), st, suite)
			if err != nil {
				return err
			}
			return renderCalibration(cmd.OutOrStdout(), report, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// calibrationJSON is the stable JSON shape of the report.
type calibrationJSON struct {
	Informative       int                      `json:"informative"`
	Saturated         int                      `json:"saturated"`
	AlwaysFails       int                      `json:"always_fails"`
	Unknown           int                      `json:"unknown"`
	NoProcessVariance int                      `json:"no_process_variance"`
	Tasks             []calibrationTaskJSONOut `json:"tasks"`
}

type calibrationTaskJSONOut struct {
	Suite              string  `json:"suite"`
	Task               string  `json:"task"`
	Runs               int     `json:"runs"`
	Profiles           int     `json:"profiles"`
	PassRate           float64 `json:"pass_rate"`
	ScoreMean          float64 `json:"score_mean"`
	Verdict            string  `json:"verdict"`
	DistinctSignatures int     `json:"distinct_signatures"`
	ProcessVariance    bool    `json:"process_variance"`
	ProcessKnown       bool    `json:"process_known"`
}

// renderCalibration writes the human or JSON calibration report.
func renderCalibration(w io.Writer, rep history.CalibrationReport, asJSON bool) error {
	if asJSON {
		out := calibrationJSON{
			Informative:       rep.Informative,
			Saturated:         rep.Saturated,
			AlwaysFails:       rep.AlwaysFails,
			Unknown:           rep.Unknown,
			NoProcessVariance: rep.NoProcessVariance,
			Tasks:             make([]calibrationTaskJSONOut, 0, len(rep.Tasks)),
		}
		for _, tc := range rep.Tasks {
			out.Tasks = append(out.Tasks, calibrationTaskJSONOut{
				Suite: tc.Suite, Task: tc.Task, Runs: tc.Runs, Profiles: tc.Profiles,
				PassRate: tc.PassRate, ScoreMean: tc.ScoreMean, Verdict: string(tc.Verdict),
				DistinctSignatures: tc.DistinctSignatures,
				ProcessVariance:    tc.ProcessVariance,
				ProcessKnown:       tc.ProcessKnown,
			})
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}

	if len(rep.Tasks) == 0 {
		_, err := fmt.Fprintln(w, "No runs recorded, so no task can be classified yet.")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tSUITE\tRUNS\tCONFIGS\tPASS\tVERDICT\tPROCESS")
	for _, tc := range rep.Tasks {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			tc.Task, tc.Suite, tc.Runs, tc.Profiles,
			fmt.Sprintf("%.0f%%", tc.PassRate*100), tc.Verdict, processCell(tc))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n%d task(s): %d informative, %d saturated, %d always-fail, %d unclassified.\n",
		len(rep.Tasks), rep.Informative, rep.Saturated, rep.AlwaysFails, rep.Unknown)
	if rep.Informative == 0 {
		fmt.Fprintln(w, "No task is in the informative band, so no task can show a quality "+
			"difference between two configurations.")
	}
	if rep.NoProcessVariance > 0 {
		fmt.Fprintf(w, "%d task(s) were measured under two or more configurations that all worked "+
			"identically, so they cannot separate them on strategy either.\n", rep.NoProcessVariance)
	}
	if rep.Unknown > 0 {
		fmt.Fprintf(w, "%d task(s) have fewer than two runs and cannot be classified; "+
			"run them again before drawing a conclusion.\n", rep.Unknown)
	}
	return nil
}

// processCell describes a task's process spread, distinguishing "everyone did
// the same thing" from "nobody measured it".
func processCell(tc history.TaskCalibration) string {
	switch {
	case !tc.ProcessKnown:
		return "not recorded"
	case tc.DistinctSignatures > 1:
		return fmt.Sprintf("%d distinct", tc.DistinctSignatures)
	case tc.Profiles < 2:
		return "identical (1 config)"
	default:
		return "identical across configs"
	}
}
