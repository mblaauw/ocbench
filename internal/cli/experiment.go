package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/runner"
	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
)

// ErrRegression is returned by `ocbench experiment run --exit-on-regression`
// when the summary measures a regression against the baseline arm. exitCode
// maps it to 3, the exit code reserved for opted-in failures (spec section 8).
var ErrRegression = errors.New("experiment: measured regression against the baseline arm")

// experimentOptions holds the effective `experiment run` flag values.
type experimentOptions struct {
	profiles         []string
	repeat           int
	baseline         string
	exitOnRegression bool
	asJSON           bool
	suiteDir         string
	agent            string
	model            string
	variant          string
	inheritEnv       bool
	keepWorktree     bool
}

func newExperimentCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "experiment",
		Short: "Run A/B config-overlay experiments",
	}
	cmd.AddCommand(newExperimentRunCmd(d))
	cmd.AddCommand(newExperimentListCmd(d))
	cmd.AddCommand(newExperimentShowCmd(d))
	return cmd
}

func newExperimentRunCmd(d Deps) *cobra.Command {
	var opts experimentOptions
	cmd := &cobra.Command{
		Use:   "run [suite] [task...]",
		Short: "Run an interleaved A/B experiment over two or more arms",
		Long: "Resolve and persist one profile per labelled arm, then execute every " +
			"selected task against every arm in the interleaved plan. The experiment is " +
			"summarised per arm and the measured regression is reported; with " +
			"--exit-on-regression a measured regression exits 3.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Capture the injected adapter before resolve replaces a nil one,
			// so tests keep their scripted fake for every arm.
			injected := d.Adapter
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			adapterFor := func(arm experiment.ArmSpec) opencode.Adapter {
				if injected != nil {
					return injected
				}
				return opencode.NewReal(opencode.Options{
					Bin: resolved.Config.OpenCodeBin,
					Env: append(os.Environ(), arm.Overlay.Env...),
				})
			}
			return runExperiment(cmd, resolved, adapterFor, opts, args)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&opts.profiles, "profile", nil, "arm spec label=overlay-path (repeatable, at least two)")
	f.IntVar(&opts.repeat, "repeat", 0, "run each task this many times per arm (default: config defaults.repeat)")
	f.StringVar(&opts.baseline, "baseline", "", "baseline arm label (default: first --profile)")
	f.BoolVar(&opts.exitOnRegression, "exit-on-regression", false, "exit 3 when a regression is measured")
	f.BoolVar(&opts.asJSON, "json", false, "output JSON")
	f.StringVar(&opts.suiteDir, "suite-dir", "", "load the suite from this directory")
	f.StringVar(&opts.agent, "agent", "", "agent override")
	f.StringVar(&opts.model, "model", "", "model override")
	f.StringVar(&opts.variant, "variant", "", "variant override")
	f.BoolVar(&opts.inheritEnv, "inherit-environment", false, "inherit the full process environment (deny-list mode)")
	f.BoolVar(&opts.keepWorktree, "keep-worktree", false, "keep the disposable worktree after each run")
	return cmd
}

// renderExperimentSummary dispatches the shared experiment summary renderer:
// the JSON report with --json, the human report otherwise. Both `experiment
// run` and `experiment show` render through it so their output stays identical.
func renderExperimentSummary(w io.Writer, s experiment.ExperimentSummary, asJSON bool) error {
	if asJSON {
		return renderExperimentJSON(w, s)
	}
	return renderExperimentHuman(w, s)
}

func newExperimentListCmd(d Deps) *cobra.Command {
	var (
		limit  = defaultHistoryLimit
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List persisted experiments, newest first",
		Long: "List persisted experiments newest first with their arm counts. Bound the " +
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

			rows, err := st.ListExperiments(cmd.Context(), limit)
			if err != nil {
				return err
			}
			items := make([]experimentListItem, 0, len(rows))
			for _, row := range rows {
				arms, err := st.ListExperimentArms(cmd.Context(), row.ID)
				if err != nil {
					return err
				}
				items = append(items, experimentListItem{
					ID:        row.ID,
					CreatedAt: row.CreatedAt,
					Name:      row.Name,
					Arms:      len(arms),
				})
			}
			return renderExperimentList(cmd.OutOrStdout(), items, asJSON)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultHistoryLimit, "maximum number of experiments to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// experimentListItem is one row of `experiment list`: the experiment metadata
// plus its arm count.
type experimentListItem struct {
	ID        string
	CreatedAt string
	Name      string
	Arms      int
}

// renderExperimentList writes the human or JSON list report.
func renderExperimentList(w io.Writer, items []experimentListItem, asJSON bool) error {
	if asJSON {
		return renderExperimentListJSON(w, items)
	}
	return renderExperimentListHuman(w, items)
}

// renderExperimentListHuman prints the aligned ID/CREATED/NAME/ARMS table.
func renderExperimentListHuman(w io.Writer, items []experimentListItem) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "no experiments")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tCREATED\tNAME\tARMS"); err != nil {
		return err
	}
	for _, it := range items {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", it.ID, it.CreatedAt, it.Name, it.Arms); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// experimentListJSON is the stable machine-readable list report. Experiments is
// always emitted, as `[]` when empty, never null.
type experimentListJSON struct {
	Experiments []experimentListItemJSON `json:"experiments"`
}

// experimentListItemJSON mirrors the human table's columns.
type experimentListItemJSON struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Name      string `json:"name"`
	Arms      int    `json:"arms"`
}

// renderExperimentListJSON prints the stable list report.
func renderExperimentListJSON(w io.Writer, items []experimentListItem) error {
	out := experimentListJSON{Experiments: make([]experimentListItemJSON, 0, len(items))}
	for _, it := range items {
		out.Experiments = append(out.Experiments, experimentListItemJSON{
			ID:        it.ID,
			CreatedAt: it.CreatedAt,
			Name:      it.Name,
			Arms:      it.Arms,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func newExperimentShowCmd(d Deps) *cobra.Command {
	var (
		asJSON bool
		format string
	)
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one experiment's summary or export it as JSONL",
		Long: "Show one persisted experiment's per-arm summary, or export one JSON " +
			"object per run with --format jsonl (spec section 12.5). This command is " +
			"read-only and never invokes OpenCode.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && format != "jsonl" {
				return &UsageError{Err: fmt.Errorf("--format must be jsonl, got %q", format)}
			}
			if asJSON && format != "" {
				return &UsageError{Err: errors.New("--json and --format are mutually exclusive")}
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

			if format == "jsonl" {
				return renderExperimentJSONL(cmd.Context(), cmd.OutOrStdout(), st, args[0])
			}
			summary, err := experiment.Summarize(cmd.Context(), st, args[0], "")
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return &UsageError{Err: err}
				}
				return err
			}
			decision := experiment.DecideRegression(summary, stats.DefaultAlpha)
			summary.Regression = &decision
			return renderExperimentSummary(cmd.OutOrStdout(), summary, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().StringVar(&format, "format", "", "output format: jsonl (one JSON object per run)")
	return cmd
}

// experimentJSONLEnvelope is one line of the `experiment show --format jsonl`
// export (spec section 12.5). SchemaVersion is the contract: additive changes
// keep the version, breaking changes bump it.
type experimentJSONLEnvelope struct {
	SchemaVersion int                         `json:"schema_version"`
	Experiment    experimentJSONLRef          `json:"experiment"`
	Arm           experimentJSONLArm          `json:"arm"`
	Run           experimentJSONLRun          `json:"run"`
	Metrics       map[string]any              `json:"metrics"`
	Validations   []experimentJSONLValidation `json:"validations"`
}

// experimentJSONLRef names the exported experiment.
type experimentJSONLRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// experimentJSONLArm describes the run's arm. OverlaySHA256 is "" when the arm
// has no overlay, keeping the field a stable string rather than null.
type experimentJSONLArm struct {
	Label         string `json:"label"`
	ProfileHash   string `json:"profile_hash"`
	OverlayKind   string `json:"overlay_kind"`
	OverlaySHA256 string `json:"overlay_sha256"`
}

// experimentJSONLRun is the run metadata carried on each line.
type experimentJSONLRun struct {
	ID              string `json:"id"`
	TaskID          string `json:"task_id"`
	TaskVersion     string `json:"task_version"`
	RepeatIndex     int    `json:"repeat_index"`
	Status          string `json:"status"`
	SuiteName       string `json:"suite_name"`
	SuiteHash       string `json:"suite_hash"`
	FixtureSHA      string `json:"fixture_sha"`
	Model           string `json:"model"`
	OpenCodeVersion string `json:"opencode_version"`
	OCBenchVersion  string `json:"ocbench_version"`
}

// experimentJSONLValidation is one validator outcome on a line.
type experimentJSONLValidation struct {
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// renderExperimentJSONL writes the versioned JSONL export: one compact line per
// run, in RunsForExperiment order. Runs whose ArmID is nil (or names no arm of
// this experiment) are skipped, since they are not part of its arms. A run with
// no metrics or validations emits {} or [], never null.
func renderExperimentJSONL(ctx context.Context, w io.Writer, st *store.Store, id string) error {
	exp, err := st.GetExperiment(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &UsageError{Err: err}
		}
		return err
	}
	arms, err := st.ListExperimentArms(ctx, id)
	if err != nil {
		return err
	}
	armByID := make(map[string]store.ExperimentArmRow, len(arms))
	for _, a := range arms {
		armByID[a.ID] = a
	}
	runs, err := st.RunsForExperiment(ctx, id)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.ArmID == nil {
			continue
		}
		arm, ok := armByID[*run.ArmID]
		if !ok {
			continue
		}
		metricRows, err := st.GetRunMetrics(ctx, run.ID)
		if err != nil {
			return err
		}
		valRows, err := st.ListRunValidations(ctx, run.ID)
		if err != nil {
			return err
		}
		line := experimentJSONLEnvelope{
			SchemaVersion: 1,
			Experiment:    experimentJSONLRef{ID: exp.ID, Name: exp.Name},
			Arm: experimentJSONLArm{
				Label:         arm.Label,
				ProfileHash:   arm.ProfileHash,
				OverlayKind:   arm.OverlayKind,
				OverlaySHA256: derefString(arm.OverlaySHA256),
			},
			Run: experimentJSONLRun{
				ID:              run.ID,
				TaskID:          run.TaskID,
				TaskVersion:     run.TaskVersion,
				RepeatIndex:     run.RepeatIndex,
				Status:          run.Status,
				SuiteName:       run.SuiteName,
				SuiteHash:       run.SuiteHash,
				FixtureSHA:      run.FixtureSHA,
				Model:           run.Model,
				OpenCodeVersion: run.OpenCodeVersion,
				OCBenchVersion:  run.OCBenchVersion,
			},
			Metrics:     experimentMetricMap(metricRows),
			Validations: experimentValidationList(valRows),
		}
		b, err := json.Marshal(line)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// derefString returns the pointed-to string, or "" for a nil pointer.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// experimentMetricMap flattens a run's metrics to a JSON object keyed by name;
// a numeric metric uses its number, a text-only metric its text.
func experimentMetricMap(rows []store.MetricRow) map[string]any {
	out := make(map[string]any, len(rows))
	for _, m := range rows {
		if m.ValueNum != nil {
			out[m.Name] = *m.ValueNum
		} else {
			out[m.Name] = m.ValueText
		}
	}
	return out
}

// experimentValidationList maps a run's validations to the export shape. It is
// never nil so an empty result marshals as [] rather than null.
func experimentValidationList(rows []store.ValidationRow) []experimentJSONLValidation {
	out := make([]experimentJSONLValidation, 0, len(rows))
	for _, v := range rows {
		out = append(out, experimentJSONLValidation{Seq: v.Seq, Kind: v.Kind, Name: v.Name, Status: v.Status})
	}
	return out
}

// runExperiment is the `experiment run` pipeline: parse and validate the arms,
// resolve the suite, execute the interleaved plan, summarise it and render.
func runExperiment(cmd *cobra.Command, d Deps, adapterFor func(experiment.ArmSpec) opencode.Adapter, opts experimentOptions, args []string) error {
	ctx := cmd.Context()

	suiteName := "core"
	var taskIDs []string
	if len(args) > 0 {
		suiteName = args[0]
		taskIDs = args[1:]
	}
	if taskIDs == nil {
		taskIDs = []string{}
	}

	arms, err := parseArms(opts.profiles, opts.baseline)
	if err != nil {
		return err
	}

	repeat := opts.repeat
	if !cmd.Flags().Changed("repeat") {
		repeat = d.Config.Defaults.Repeat
	}
	if repeat < 1 {
		return &UsageError{Err: fmt.Errorf("--repeat must be at least 1, got %d", repeat)}
	}

	if err := config.EnsureDirs(d.Paths); err != nil {
		return err
	}
	st, err := store.Open(d.Paths.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		return err
	}

	s, _, err := suite.Resolve(d.SuiteFS, d.Paths, suiteName, opts.suiteDir)
	if err != nil {
		return err
	}
	tasks, err := selectTasks(s, taskIDs)
	if err != nil {
		return err
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	inherit := opts.inheritEnv || d.Config.Sandbox.InheritEnvironment
	sandboxMode := "default"
	if inherit {
		sandboxMode = "inherit"
	}

	outcome, err := experiment.Run(ctx, st, experiment.Request{
		Suite:    s,
		Tasks:    tasks,
		Arms:     arms,
		Baseline: opts.baseline,
		Paths:    d.Paths,
		Profile: profile.Options{
			Dir:         dir,
			Agent:       opts.agent,
			Model:       opts.model,
			Variant:     opts.variant,
			SandboxMode: sandboxMode,
			Auto:        true,
		},
		AdapterFor:   adapterFor,
		EnvPolicy:    runner.EnvPolicy{Inherit: inherit, PassEnv: d.Config.Sandbox.PassEnv},
		Repeat:       repeat,
		KeepWorktree: opts.keepWorktree,
	})
	if err != nil {
		return err
	}

	summary, err := experiment.Summarize(ctx, st, outcome.ExperimentID, opts.baseline)
	if err != nil {
		return err
	}
	decision := experiment.DecideRegression(summary, stats.DefaultAlpha)
	summary.Regression = &decision

	if err := renderExperimentSummary(cmd.OutOrStdout(), summary, opts.asJSON); err != nil {
		return err
	}

	if decision.Regressed && opts.exitOnRegression {
		return ErrRegression
	}
	return nil
}

// parseArms parses and validates the --profile specs: at least two arms,
// distinct labels and a --baseline that names a declared arm. Every failure is
// a usage error (exit 2).
func parseArms(profiles []string, baseline string) ([]experiment.ArmSpec, error) {
	if len(profiles) < 2 {
		return nil, &UsageError{Err: fmt.Errorf("--profile requires at least two arms, got %d", len(profiles))}
	}
	arms := make([]experiment.ArmSpec, 0, len(profiles))
	seen := make(map[string]bool, len(profiles))
	for _, spec := range profiles {
		arm, err := experiment.ParseArm(spec)
		if err != nil {
			return nil, &UsageError{Err: err}
		}
		if seen[arm.Label] {
			return nil, &UsageError{Err: fmt.Errorf("duplicate arm label %q", arm.Label)}
		}
		seen[arm.Label] = true
		arms = append(arms, arm)
	}
	if baseline != "" && !seen[baseline] {
		return nil, &UsageError{Err: fmt.Errorf("--baseline %q names no declared arm", baseline)}
	}
	return arms, nil
}

// experimentArmAggregate pools one arm's per-task statistics into the single
// per-arm block the human report prints.
type experimentArmAggregate struct {
	executions       int
	successes        int
	passRate         float64
	lo, hi           float64
	passAtK          bool
	passAllK         bool
	medianTokens     float64
	medianCost       float64
	medianDurationMS float64
}

// aggregateExperimentArm sums executions across tasks and reports the pooled
// pass rate with its Wilson interval. Pass@k is true only when every task
// passed at least once; the medians are the median of the per-task medians.
func aggregateExperimentArm(tasks []experiment.TaskSummary, label string) experimentArmAggregate {
	agg := experimentArmAggregate{passAtK: true, passAllK: true}
	var tokens, costs, durations []float64
	seen := false
	for _, ts := range tasks {
		st, ok := ts.PerArm[label]
		if !ok {
			continue
		}
		seen = true
		agg.executions += st.Executions
		agg.successes += st.Successes
		agg.passAtK = agg.passAtK && st.PassAtK
		agg.passAllK = agg.passAllK && st.PassAllK
		tokens = append(tokens, st.MedianTokens)
		costs = append(costs, st.MedianCost)
		durations = append(durations, st.MedianDurationMS)
	}
	if !seen {
		agg.passAtK = false
		agg.passAllK = false
	}
	if agg.executions > 0 {
		agg.passRate = float64(agg.successes) / float64(agg.executions)
	}
	agg.lo, agg.hi = stats.Wilson(agg.successes, agg.executions, 1.96)
	agg.medianTokens = stats.Median(tokens)
	agg.medianCost = stats.Median(costs)
	agg.medianDurationMS = stats.Median(durations)
	return agg
}

// renderExperimentHuman prints one block per arm, the cost per solved task per
// arm, any drift warnings and exactly one regression line.
func renderExperimentHuman(w io.Writer, s experiment.ExperimentSummary) error {
	for _, arm := range s.Arms {
		agg := aggregateExperimentArm(s.Tasks, arm.Label)
		if _, err := fmt.Fprintf(w, "arm %s\n", arm.Label); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  executions: %d\n", agg.executions); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  pass rate: %.4f [%.4f, %.4f]\n", agg.passRate, agg.lo, agg.hi); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  pass^k: pass@k=%t pass-all-k=%t\n", agg.passAtK, agg.passAllK); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  median tokens: %.4f\n", agg.medianTokens); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  median cost: %.4f\n", agg.medianCost); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  median duration: %.4f ms\n", agg.medianDurationMS); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "cost per solved task"); err != nil {
		return err
	}
	for _, arm := range s.Arms {
		cost, ok := s.CostPerSolved[arm.Label]
		if !ok {
			if _, err := fmt.Fprintf(w, "  %s: n/a\n", arm.Label); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s: %.4f\n", arm.Label, cost); err != nil {
			return err
		}
	}

	for _, warning := range s.DriftWarnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}

	line := "regression: none detected"
	switch {
	case s.InsufficientData:
		line = "regression: insufficient data"
	case s.Regression != nil && s.Regression.Regressed:
		// Reason already names the arm and begins with "regression: ".
		line = s.Regression.Reason
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// renderExperimentJSON prints the machine-readable summary. Nil slices and maps
// are normalised to empty ones so the report emits [] and {} rather than null,
// mirroring the compare report's stable shape.
func renderExperimentJSON(w io.Writer, s experiment.ExperimentSummary) error {
	if s.Arms == nil {
		s.Arms = []store.ExperimentArmRow{}
	}
	if s.Tasks == nil {
		s.Tasks = []experiment.TaskSummary{}
	}
	for i := range s.Tasks {
		if s.Tasks[i].PerArm == nil {
			s.Tasks[i].PerArm = map[string]experiment.ArmTaskStats{}
		}
	}
	if s.CostPerSolved == nil {
		s.CostPerSolved = map[string]float64{}
	}
	if s.DriftWarnings == nil {
		s.DriftWarnings = []string{}
	}
	if s.PassRateTests == nil {
		s.PassRateTests = map[string]experiment.StatTest{}
	}
	if s.CostTests == nil {
		s.CostTests = map[string]experiment.StatTest{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}
