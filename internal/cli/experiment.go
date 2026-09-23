package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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

	summary, err := experiment.Summarize(ctx, st, outcome.ExperimentID, opts.baseline, stats.DefaultAlpha)
	if err != nil {
		return err
	}
	decision := experiment.DecideRegression(summary, stats.DefaultAlpha)
	summary.Regression = &decision

	if opts.asJSON {
		if err := renderExperimentJSON(cmd.OutOrStdout(), summary); err != nil {
			return err
		}
	} else if err := renderExperimentHuman(cmd.OutOrStdout(), summary); err != nil {
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
