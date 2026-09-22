package cli

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/canon"
	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/runner"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
)

// ErrTaskFailure is returned by `ocbench run --exit-on-task-failure` when at
// least one validator failed, errored or timed out. exitCode maps it to 3, the
// exit code reserved for opted-in task failures (spec section 8).
var ErrTaskFailure = errors.New("run: one or more task validators failed")

// runOptions holds the effective `run` flag values.
type runOptions struct {
	repeat            int
	dryRun            bool
	suiteDir          string
	agent             string
	model             string
	variant           string
	inheritEnv        bool
	keepWorktree      bool
	asJSON            bool
	exitOnTaskFailure bool
}

func newRunCmd(d Deps) *cobra.Command {
	var opts runOptions
	cmd := &cobra.Command{
		Use:   "run [suite] [task...]",
		Short: "Run a benchmark suite against the resolved OpenCode profile",
		Long: "Resolve and persist the execution profile once, then run every selected task " +
			"in disposable worktrees. Tasks run in suite order; with --repeat they run " +
			"task-major (task1 x N, task2 x N, ...) and each execution is persisted with its " +
			"repeat index. The pipeline exits 0 when it completed, regardless of task pass/fail; " +
			"--exit-on-task-failure instead exits 3 when a validator failed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			return runSuite(cmd, resolved, opts, args)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.repeat, "repeat", 0, "run each task this many times (default: config defaults.repeat)")
	f.BoolVar(&opts.dryRun, "dry-run", false, "resolve the plan and exit without invoking the model")
	f.StringVar(&opts.suiteDir, "suite-dir", "", "load the suite from this directory")
	f.StringVar(&opts.agent, "agent", "", "agent override")
	f.StringVar(&opts.model, "model", "", "model override")
	f.StringVar(&opts.variant, "variant", "", "variant override")
	f.BoolVar(&opts.inheritEnv, "inherit-environment", false, "inherit the full process environment (deny-list mode)")
	f.BoolVar(&opts.keepWorktree, "keep-worktree", false, "keep the disposable worktree after each run")
	f.BoolVar(&opts.asJSON, "json", false, "output JSON")
	f.BoolVar(&opts.exitOnTaskFailure, "exit-on-task-failure", false, "exit 3 when any validator fails")
	return cmd
}

// runSuite is the `run` pipeline: snapshot the profile, create the experiment,
// execute every selected task in plan order, and render the report.
func runSuite(cmd *cobra.Command, d Deps, opts runOptions, args []string) error {
	ctx := cmd.Context()

	suiteName := "core"
	var taskIDs []string
	if len(args) > 0 {
		suiteName = args[0]
		taskIDs = args[1:]
	}
	if taskIDs == nil {
		// An absent task filter is recorded in the experiment spec as an empty
		// list rather than JSON null, so the schema is stable.
		taskIDs = []string{}
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

	sources, err := profile.Discover(ctx, d.Adapter, dir)
	if err != nil {
		return err
	}
	p, err := profile.Fingerprint(sources, profile.Options{
		Dir:         dir,
		Agent:       opts.agent,
		Model:       opts.model,
		Variant:     opts.variant,
		SandboxMode: sandboxMode,
		Auto:        true,
	})
	if err != nil {
		return err
	}
	if _, err := profile.Persist(ctx, st, d.Paths, p); err != nil {
		return err
	}

	expID, err := newRunUUID()
	if err != nil {
		return err
	}
	created := time.Now().UTC()
	spec, err := canon.JSON(map[string]any{
		"suite":                s.Name,
		"suite_version":        s.Version,
		"suite_dir":            opts.suiteDir,
		"tasks":                taskIDs,
		"repeat":               repeat,
		"dry_run":              opts.dryRun,
		"agent":                opts.agent,
		"model":                opts.model,
		"variant":              opts.variant,
		"inherit_environment":  inherit,
		"keep_worktree":        opts.keepWorktree,
		"exit_on_task_failure": opts.exitOnTaskFailure,
	})
	if err != nil {
		return err
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{
		ID:        expID,
		Name:      fmt.Sprintf("run %s@%s %s", s.Name, s.Version, created.Format(time.RFC3339)),
		SpecJSON:  string(spec),
		CreatedAt: created.Format(time.RFC3339),
	}); err != nil {
		return err
	}

	envPolicy := runner.EnvPolicy{Inherit: inherit, PassEnv: d.Config.Sandbox.PassEnv}
	records := make([]runRecord, 0, len(tasks)*repeat)
	for _, task := range tasks {
		for i := 0; i < repeat; i++ {
			res, err := runner.Run(ctx, d.Adapter, st, runner.Request{
				Suite:        s,
				Task:         task,
				Profile:      p,
				Paths:        d.Paths,
				EnvPolicy:    envPolicy,
				Agent:        opts.agent,
				Model:        opts.model,
				Variant:      opts.variant,
				Auto:         true,
				DryRun:       opts.dryRun,
				KeepWorktree: opts.keepWorktree,
				ExperimentID: expID,
				RepeatIndex:  i,
				MCPTools:     mcpToolNames(p),
			})
			if err != nil {
				return err
			}
			records = append(records, newRunRecord(res, i))
		}
	}

	// Aggregates only add information when a task ran more than once; for a
	// single execution the run itself is the summary. The JSON report still
	// emits the key as an empty list.
	var aggregates []runAggregate
	if repeat > 1 {
		aggregates = aggregateRuns(records, taskIDsOf(tasks))
	}
	if opts.asJSON {
		if err := renderRunJSON(cmd.OutOrStdout(), p.Hash, expID, records, aggregates); err != nil {
			return err
		}
	} else if err := renderRunHuman(cmd.OutOrStdout(), records, repeat, aggregates); err != nil {
		return err
	}

	if opts.exitOnTaskFailure {
		for _, r := range records {
			if r.ValidatorsFailed > 0 {
				return ErrTaskFailure
			}
		}
	}
	return nil
}

// selectTasks filters suite tasks to the requested ids while preserving suite
// order. An unknown id is a usage error (exit 2).
func selectTasks(s *suite.Suite, ids []string) ([]*suite.Task, error) {
	if len(ids) == 0 {
		return s.Tasks, nil
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, err := s.Task(id); err != nil {
			return nil, &UsageError{Err: err}
		}
		wanted[id] = true
	}
	out := make([]*suite.Task, 0, len(wanted))
	for _, t := range s.Tasks {
		if wanted[t.ID] {
			out = append(out, t)
		}
	}
	return out, nil
}

// taskIDsOf returns the task ids in plan order.
func taskIDsOf(tasks []*suite.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// mcpToolNames lists the configured MCP server names from the profile's mcp
// components. The runner classifies `<server>_<tool>` calls with them.
func mcpToolNames(p *profile.Profile) []string {
	var out []string
	for _, c := range p.Components {
		if c.Kind == "mcp" {
			out = append(out, c.Name)
		}
	}
	return out
}

// newRunRecord flattens a runner result into the report shape.
func newRunRecord(res runner.Result, repeatIndex int) runRecord {
	return runRecord{
		RunID:            res.RunID,
		TaskID:           res.TaskID,
		RepeatIndex:      repeatIndex,
		Status:           res.Status,
		DurationMS:       res.DurationMS,
		TokensTotal:      int64(res.Metrics["tokens_total"]),
		ToolCallsTotal:   int64(res.Metrics["tool_calls_total"]),
		ValidatorsFailed: validationFailures(res.Validations),
	}
}

// validationFailures counts validators that block a pass.
func validationFailures(vals []evaluation.ValidationResult) int {
	n := 0
	for _, v := range vals {
		switch v.Status {
		case "failed", "error", "timeout":
			n++
		}
	}
	return n
}

// newRunUUID returns a random RFC 4122 version 4 identifier formatted as
// 8-4-4-4-12 hex, mirroring the profile and runner generators.
func newRunUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate experiment id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
