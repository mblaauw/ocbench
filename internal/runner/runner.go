package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"mbl/ocbench/internal/canon"
	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
	"mbl/ocbench/internal/suite"
	"mbl/ocbench/internal/version"
)

// Request is one benchmark run: a task from a suite, against a profile, with
// the sandbox policy and the model selection.
type Request struct {
	Suite        *suite.Suite
	Task         *suite.Task
	Profile      *profile.Profile
	Paths        config.Paths
	EnvPolicy    EnvPolicy
	Agent        string
	Model        string
	Variant      string
	Auto         bool
	Pure         bool
	DryRun       bool
	KeepWorktree bool
	// ExperimentID is written to runs.experiment_id verbatim. The experiments
	// row is created by the CLI (Task 9); the runner only sets the column, so an
	// unknown id is a foreign-key error and must already exist when non-empty.
	ExperimentID string
	RepeatIndex  int
	// MCPTools lists configured MCP server names used to classify
	// `<server>_<tool>` tool calls in the metrics.
	MCPTools []string
	// ExtraEnv is overlaid onto the sandboxed child environment after BuildEnv,
	// last-write-wins by key. It is how an experiment arm's config overlay
	// (OPENCODE_CONFIG / OPENCODE_CONFIG_DIR) reaches both the child process and
	// the validators.
	ExtraEnv []string
}

// Result is the outcome of one Run.
type Result struct {
	RunID           string
	Status          string // dry_run|passed|failed|error|timeout
	TaskID          string
	SessionID       string
	ExitCode        *int // nil when no session ran (dry run, start failure)
	Metrics         map[string]float64
	Validations     []evaluation.ValidationResult
	ChangedFiles    []string
	UnexpectedFiles []string
	DurationMS      int64
	ArtifactsDir    string
	Error           string
}

// Run executes the spec §8 pipeline for a single task: materialise the fixture
// baseline, create a disposable worktree, run one OpenCode session under the
// sandbox environment, capture every artifact, run the validators, persist the
// run and remove the worktree unless KeepWorktree.
//
// Run returns a non-nil error only for infrastructure failures that prevent a
// run from being recorded (bad request, fixture/worktree/git/artifact/store
// errors). A session that fails to start, times out, or produces failing
// validators is a recorded run outcome: the status field carries it and the
// error is nil.
func Run(ctx context.Context, a opencode.Adapter, st *store.Store, req Request) (Result, error) {
	started := time.Now().UTC()
	if req.Suite == nil || req.Task == nil || req.Profile == nil {
		return Result{}, errors.New("run: suite, task and profile are required")
	}
	if st == nil {
		return Result{}, errors.New("run: nil store")
	}
	if a == nil {
		return Result{}, errors.New("run: nil adapter")
	}
	if req.Profile.ID == "" {
		return Result{}, errors.New("run: profile id is required (persist the profile first)")
	}

	runID, err := newUUID()
	if err != nil {
		return Result{}, err
	}
	runDir := filepath.Join(req.Paths.Runs, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("run %s: create artifacts dir: %w", runID, err)
	}

	baseline, err := MaterializeFixture(ctx, req.Paths.Cache, req.Task.Fixture, req.Task.FixtureHash)
	if err != nil {
		return Result{}, err
	}
	worktree := filepath.Join(runDir, "worktree")
	if err := CreateWorktree(ctx, baseline, worktree); err != nil {
		return Result{}, err
	}
	env := BuildEnv(os.Environ(), req.EnvPolicy)
	env = ApplyExtraEnv(env, req.ExtraEnv)
	timeout := req.Task.EffectiveTimeout(req.Suite)

	// Cleanup must still release the worktree after a cancelled run, so it uses
	// a cancellation-free context. The bounded context is created here, at
	// deferred-cleanup execution time, so a long model run cannot consume the
	// budget before cleanup starts.
	defer func() {
		if req.KeepWorktree {
			return
		}
		cleanupCtx, cleanupCancel := postRunContext(ctx, timeout)
		defer cleanupCancel()
		_ = RemoveWorktree(cleanupCtx, baseline, worktree)
	}()

	res := Result{RunID: runID, TaskID: req.Task.ID, ArtifactsDir: runDir}

	if req.DryRun {
		if err := writePlan(runDir, req, runID, env, baseline, worktree); err != nil {
			return Result{}, err
		}
		res.Status = "dry_run"
		res.DurationMS = time.Since(started).Milliseconds()
		postCtx, postCancel := postRunContext(ctx, timeout)
		defer postCancel()
		if err := persist(postCtx, st, req, res, baseline, started, "", nil, nil); err != nil {
			return Result{}, err
		}
		return res, nil
	}

	if err := copySuiteInputs(runDir, req.Suite, req.Task); err != nil {
		return Result{}, err
	}

	metrics := evaluation.NewMetrics(req.MCPTools)
	session, err := a.Start(ctx, opencode.RunRequest{
		Dir:     worktree,
		Prompt:  req.Task.Prompt,
		Agent:   req.Agent,
		Model:   req.Model,
		Variant: req.Variant,
		// Auto and Pure are mutually exclusive in OpenCode; Pure wins.
		Auto:    req.Auto && !req.Pure,
		Pure:    req.Pure,
		Env:     env,
		Timeout: timeout,
	})
	if err != nil {
		res.Status = "error"
		res.Error = err.Error()
		// No session ran, so the event, file and validator metrics are not
		// computable: record an empty metric set rather than misleading zeroes.
		res.Metrics = map[string]float64{}
		res.DurationMS = time.Since(started).Milliseconds()
		finished := time.Now().UTC().Format(time.RFC3339)
		if werr := writeResult(runDir, req, res, baseline, started.Format(time.RFC3339), finished); werr != nil {
			return Result{}, werr
		}
		// A cancellation observed at Start must still persist the row, so
		// persistence uses a cancellation-free context.
		postCtx, postCancel := postRunContext(ctx, timeout)
		defer postCancel()
		if perr := persist(postCtx, st, req, res, baseline, started, finished, res.Metrics, nil); perr != nil {
			return Result{}, perr
		}
		return res, nil
	}

	// The adapter only kills a timed-out process group when Wait runs, but Wait
	// must not run until Events is fully drained (its discard path is lossy).
	// This watchdog kills at the same deadline while the drain is in progress.
	// It records a timeout only when it actually kills a live process: a process
	// that finishes just before the deadline must not be misclassified. The
	// events channel closes only after the child has exited, so drainDone is a
	// reliable "process already finished" signal.
	var timedOut, cancelled, drainDone atomic.Bool
	watchdog := time.AfterFunc(timeout, func() {
		if drainDone.Load() {
			return // the process already exited; nothing to kill
		}
		timedOut.Store(true)
		session.Kill()
	})
	defer watchdog.Stop()

	// The session only observes context cancellation inside Wait, but Wait
	// cannot run until the event drain completes. Kill the process group as
	// soon as the command context ends so Ctrl-C tears the child down promptly
	// instead of leaving it running until the task deadline.
	stopCancelWatch := context.AfterFunc(ctx, func() {
		if drainDone.Load() {
			return
		}
		cancelled.Store(true)
		session.Kill()
	})
	defer stopCancelWatch()

	if err := drainEvents(filepath.Join(runDir, "events.jsonl"), session.Events(), metrics); err != nil {
		// The drain failed, so the normal Wait path below will not run. Kill and
		// reap the session before returning, otherwise its process group keeps
		// running, the tailer stays blocked and the temp files leak. The drain
		// error stays authoritative even if cleanup itself errors.
		session.Kill()
		session.Wait()
		return Result{}, err
	}
	drainDone.Store(true)
	// The drain is finished, so Wait now observes ctx cancellation directly;
	// stopping the watcher prevents a late callback from racing the outcome.
	stopCancelWatch()

	exitCode, waitErr := session.Wait()
	watchdog.Stop()
	res.SessionID = session.ID()
	res.ExitCode = &exitCode
	cancelledRun := cancelled.Load() || errors.Is(waitErr, context.Canceled)

	if err := os.WriteFile(filepath.Join(runDir, "stderr.txt"), []byte(session.Stderr()), 0o644); err != nil {
		return Result{}, fmt.Errorf("write stderr.txt: %w", err)
	}

	// A cancelled command context must not stop the interrupted run from being
	// recorded: artifact capture and persistence use a cancellation-free,
	// task-timeout-bounded context with a fresh deadline so the session's own
	// budget does not consume it.
	postCtx, postCancel := postRunContext(ctx, timeout)
	defer postCancel()

	// Export is best-effort: a failure is recorded but never fails the run.
	if res.SessionID == "" {
		appendNote(&res.Error, "export skipped: no session id")
	} else if data, err := a.Export(postCtx, res.SessionID); err != nil {
		appendNote(&res.Error, "export: "+err.Error())
	} else if err := os.WriteFile(filepath.Join(runDir, "session.json"), data, 0o644); err != nil {
		return Result{}, fmt.Errorf("write session.json: %w", err)
	}

	changed, err := ChangedFiles(postCtx, worktree, baseline.SHA)
	if err != nil {
		return Result{}, err
	}
	diff, err := DiffAgainstBaseline(postCtx, worktree, baseline.SHA)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "diff.patch"), diff, 0o644); err != nil {
		return Result{}, fmt.Errorf("write diff.patch: %w", err)
	}
	if err := writeJSON(filepath.Join(runDir, "changed.json"), changed); err != nil {
		return Result{}, err
	}
	if _, err := captureUntracked(postCtx, runDir, worktree); err != nil {
		return Result{}, err
	}

	res.ChangedFiles = changed
	res.UnexpectedFiles = unexpectedFiles(req.Task.AllowChanges, changed)

	// Cancellation can also arrive after Wait while post-session artifacts are
	// captured; once the drain watcher is stopped the request context is the
	// source of truth. Re-check it here so a late Ctrl-C skips validators and
	// records error, instead of running them on a dead context and reading as
	// failed.
	if ctx.Err() != nil {
		cancelledRun = true
	}

	// Validators are skipped after cancellation: the session was interrupted,
	// so there is no completed answer to validate and running them would delay
	// the prompt return.
	var validations []evaluation.ValidationResult
	var validationRows []store.ValidationRow
	if !cancelledRun {
		// Each validator gets the full task timeout, not the remaining budget:
		// the budget is shared by the agent session and validators are cheap,
		// so a fixed per-validator bound is simpler and keeps a single validator
		// from being starved by an earlier one.
		validations, validationRows, err = runValidators(ctx, req, runDir, worktree, env, timeout, metrics.FinalAnswer)
		if err != nil {
			return Result{}, err
		}
	}
	res.Validations = validations

	// A cancellation can also land while a validator is already running. The
	// validators that started are still recorded, but cancellation is the
	// authoritative outcome: re-check the request context so a validator
	// error/timeout cannot downgrade the run to failed. Deadline timeout still
	// wins in the switch below.
	if ctx.Err() != nil {
		cancelledRun = true
	}

	switch {
	case timedOut.Load() || errors.Is(waitErr, context.DeadlineExceeded):
		res.Status = "timeout"
		appendNote(&res.Error, "task timed out")
	case cancelledRun:
		res.Status = "error"
		appendNote(&res.Error, "run cancelled")
	case waitErr != nil:
		res.Status = "error"
		appendNote(&res.Error, waitErr.Error())
	case exitCode != 0:
		// A non-zero OpenCode exit is an error even when the validators pass:
		// the agent did not complete normally.
		res.Status = "error"
		appendNote(&res.Error, fmt.Sprintf("opencode exited with code %d", exitCode))
	case validatorsFailed(validations):
		res.Status = "failed"
	default:
		res.Status = "passed"
	}

	res.DurationMS = time.Since(started).Milliseconds()
	res.Metrics = metrics.MetricsMap()
	if res.Metrics == nil {
		res.Metrics = map[string]float64{}
	}

	// Validators can consume the whole task budget (each gets the full timeout),
	// so the pre-validator postCtx may already be expired by the time they
	// return. A fresh cancellation-free bounded context guarantees the change
	// counts, result and persisted row are still produced after cancellation or
	// timeout; otherwise the run would be lost after its validators ran.
	finalCtx, finalCancel := postRunContext(ctx, timeout)
	defer finalCancel()
	created, deleted, err := changeCounts(finalCtx, worktree, baseline.SHA)
	if err != nil {
		return Result{}, err
	}
	for name, value := range derivedMetrics(res, changed, diff, validations, created, deleted) {
		res.Metrics[name] = value
	}
	finished := time.Now().UTC().Format(time.RFC3339)

	if err := writeResult(runDir, req, res, baseline, started.Format(time.RFC3339), finished); err != nil {
		return Result{}, err
	}
	if err := persist(finalCtx, st, req, res, baseline, started, finished, res.Metrics, validationRows); err != nil {
		return Result{}, err
	}
	return res, nil
}

// postRunContext returns the context used for post-run work: artifact capture,
// result writing, persistence and cleanup. It preserves the request context's
// values but ignores its cancellation so a run interrupted by Ctrl-C can still
// record its outcome.
//
// The deadline is max(task timeout, 5s), not the task timeout alone. The model
// execution budget is distinct from deterministic post-run work: a run that
// already consumed its whole task timeout (or timed out early) would otherwise
// start this phase with an already-expired context, and the local Export and
// git artifact capture could fail to persist or clean up. Each call still gets
// a fresh deadline, so the session's own budget is never consumed here.
func postRunContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), max(timeout, 5*time.Second))
}

// openEventsFile opens the events artifact for writing. It is a package-private
// seam so tests can inject a deterministic drain failure; production uses
// os.Create.
var openEventsFile = os.Create

// drainEvents writes every raw event line to path as newline-terminated JSONL
// while feeding the metrics. It ranges until the channel closes, which the
// adapter does before Wait returns; the drain therefore completes before Wait.
func drainEvents(path string, events <-chan []byte, metrics *evaluation.Metrics) (err error) {
	f, err := openEventsFile(path)
	if err != nil {
		return fmt.Errorf("create events.jsonl: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close events.jsonl: %w", cerr)
		}
	}()
	for line := range events {
		buf := make([]byte, 0, len(line)+1)
		buf = append(buf, line...)
		buf = append(buf, '\n')
		if _, err := f.Write(buf); err != nil {
			return fmt.Errorf("write events.jsonl: %w", err)
		}
		metrics.ObserveLine(line)
	}
	return nil
}

// runValidators executes every task validator, writing each result's full
// output to validation/<seq>-<name>.log. When a task requirement is missing all
// validators are skipped with the reason in Output.
func runValidators(ctx context.Context, req Request, runDir, worktree string, env []string, timeout time.Duration, finalAnswer string) ([]evaluation.ValidationResult, []store.ValidationRow, error) {
	missing := evaluation.Requirements(req.Task.Requires)
	results := make([]evaluation.ValidationResult, 0, len(req.Task.Validators))
	rows := make([]store.ValidationRow, 0, len(req.Task.Validators))

	if err := os.MkdirAll(filepath.Join(runDir, "validation"), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create validation dir: %w", err)
	}
	for i, v := range req.Task.Validators {
		seq := i + 1
		var res evaluation.ValidationResult
		switch {
		case len(missing) > 0:
			reason := "skipped: missing requirements: " + strings.Join(missing, ", ")
			res = evaluation.ValidationResult{
				Seq: seq, Kind: v.Kind, Name: v.Name,
				Status: "skipped", ExitCode: -1, Output: reason, Excerpt: reason,
			}
		case ctx.Err() != nil:
			// Cancellation arrived while an earlier validator was running (or
			// between validators): later validators must not start. Record them
			// as skipped with an explicit reason so the interruption is visible.
			reason := "skipped: run cancelled"
			res = evaluation.ValidationResult{
				Seq: seq, Kind: v.Kind, Name: v.Name,
				Status: "skipped", ExitCode: -1, Output: reason, Excerpt: reason,
			}
		default:
			res = evaluation.RunValidator(ctx, seq, evaluation.ValidatorSpec{
				Kind: v.Kind, Name: v.Name, Command: v.Command, Patterns: v.Patterns, Mode: v.Mode,
			}, worktree, env, timeout, finalAnswer)
		}
		rel := validationRelPath(seq, v.Name)
		if err := os.WriteFile(filepath.Join(runDir, filepath.FromSlash(rel)), []byte(res.Output), 0o644); err != nil {
			return nil, nil, fmt.Errorf("write %s: %w", rel, err)
		}
		results = append(results, res)
		rows = append(rows, store.ValidationRow{
			Seq:           res.Seq,
			Kind:          res.Kind,
			Name:          res.Name,
			Command:       strings.Join(v.Command, " "),
			Status:        res.Status,
			ExitCode:      res.ExitCode,
			DurationMS:    res.DurationMS,
			OutputPath:    rel,
			OutputExcerpt: res.Excerpt,
		})
	}
	return results, rows, nil
}

// unexpectedFiles returns the changed paths that match no allow_changes glob.
func unexpectedFiles(allow, changed []string) []string {
	out := make([]string, 0, len(changed))
	for _, p := range changed {
		if !matchAny(allow, p) {
			out = append(out, p)
		}
	}
	return out
}

// validatorsFailed reports whether any validator status blocks a pass.
func validatorsFailed(results []evaluation.ValidationResult) bool {
	for _, r := range results {
		switch r.Status {
		case "failed", "error", "timeout":
			return true
		}
	}
	return false
}

// persist writes the suite, task and run (plus metrics and validations) in one
// transaction per store method. Metrics and validations are optional.
func persist(ctx context.Context, st *store.Store, req Request, res Result, baseline Baseline, started time.Time, finished string, metricsMap map[string]float64, validationRows []store.ValidationRow) error {
	suiteID := req.Suite.Hash
	manifest, err := canon.JSON(map[string]any{
		"name": req.Suite.Name, "version": req.Suite.Version,
		"hash": req.Suite.Hash, "description": req.Suite.Description,
	})
	if err != nil {
		return err
	}
	if err := st.InsertSuite(ctx, store.SuiteRow{
		ID:           suiteID,
		Name:         req.Suite.Name,
		Version:      req.Suite.Version,
		Hash:         req.Suite.Hash,
		Source:       suiteSource(req.Suite),
		ManifestJSON: string(manifest),
		CreatedAt:    started.Format(time.RFC3339),
	}); err != nil {
		return err
	}

	tags, err := canon.JSON(req.Task.Tags)
	if err != nil {
		return err
	}
	spec, err := canon.JSON(map[string]any{
		"id": req.Task.ID, "version": req.Task.Version,
		"spec_hash": req.Task.SpecHash, "fixture_hash": req.Task.FixtureHash,
	})
	if err != nil {
		return err
	}
	if err := st.InsertTask(ctx, store.TaskRow{
		SuiteID:        suiteID,
		TaskID:         req.Task.ID,
		Version:        req.Task.Version,
		Name:           req.Task.Name,
		TagsJSON:       string(tags),
		TimeoutSeconds: int(req.Task.EffectiveTimeout(req.Suite).Seconds()),
		FixtureSHA:     baseline.SHA,
		SpecJSON:       string(spec),
	}); err != nil {
		return err
	}

	duration := res.DurationMS
	if err := st.InsertRun(ctx, store.RunRow{
		ID:              res.RunID,
		ExperimentID:    req.ExperimentID,
		RepeatIndex:     req.RepeatIndex,
		ProfileID:       req.Profile.ID,
		ProfileHash:     req.Profile.Hash,
		SuiteID:         suiteID,
		SuiteName:       req.Suite.Name,
		SuiteVersion:    req.Suite.Version,
		SuiteHash:       req.Suite.Hash,
		TaskID:          req.Task.ID,
		TaskVersion:     req.Task.Version,
		FixtureSHA:      baseline.SHA,
		OpenCodeVersion: req.Profile.OpenCodeVersion,
		OCBenchVersion:  version.Info().Version,
		Model:           req.Model,
		Agent:           req.Agent,
		Variant:         req.Variant,
		Status:          res.Status,
		DryRun:          req.DryRun,
		ExitCode:        res.ExitCode,
		SessionID:       res.SessionID,
		StartedAt:       started.Format(time.RFC3339),
		FinishedAt:      finished,
		DurationMS:      &duration,
		ArtifactsDir:    res.ArtifactsDir,
		Error:           res.Error,
	}); err != nil {
		return err
	}
	if err := st.InsertRunMetrics(ctx, res.RunID, metricsMap); err != nil {
		return err
	}
	return st.InsertRunValidations(ctx, res.RunID, validationRows)
}

// suiteSource names where a suite was loaded from.
func suiteSource(s *suite.Suite) string {
	if s.Dir != "" {
		return s.Dir
	}
	return "embedded"
}

// appendNote appends a semicolon-separated note to an error string.
func appendNote(dst *string, note string) {
	if note == "" {
		return
	}
	if *dst == "" {
		*dst = note
		return
	}
	*dst += "; " + note
}

// newUUID returns a random RFC 4122 version 4 identifier formatted as
// 8-4-4-4-12 hex, mirroring the profile package's generator.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
