# Experiments and Statistics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add `ocbench experiment run|list|show`: A/B config-overlay experiments with interleaved repeats, aggregate statistics, a regression exit code, and a versioned JSONL export.

**Architecture:** Three new units. `internal/stats` holds pure statistics (no deps, seeded and deterministic). `internal/experiment` owns arms, overlay resolution, the interleaved execution plan, orchestration over `runner.Run`, aggregation and the regression decision. `internal/cli/experiment.go` parses flags and renders. The store gains an `experiment_arms` table and `runs.arm_id` via an append-only migration.

**Tech Stack:** Go stdlib (`math/rand`, `math`, `crypto/sha256`), Cobra, modernc SQLite. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-22-ocbench-design.md` §12 (Experiments), plus the §6 schema, §8 exit codes, §9 metric naming and §10 command surface amendments.

## Global Constraints

- No new dependencies; no network in tests; temp SQLite and injected adapters only.
- Migrations are append-only: `0002_experiments.sql` must apply cleanly to a v1 database and must be re-runnable without effect.
- Statistics are derived at read time and never persisted.
- `schema_version: 1` is the export contract; additive changes keep it.
- Exit `3` is the opted-in-failure family: `--exit-on-task-failure` and `--exit-on-regression`.
- Overlay variables are the only benchmark-controlled child env vars; they are applied after the sandbox allowlist and only for arms that declare an overlay.
- Reports never claim causation; drift between arms is named, never attributed.

## Review Focus

- Fewer than three repeats per arm must yield "insufficient data", never a significance claim.
- A permutation result must be reproducible for a fixed seed across runs and machines.
- Overlay env vars must survive the sandbox for the child process, and must not leak into runs without an overlay.
- Interleaving must be task-major with arms alternating inside each repeat.
- The JSONL envelope must stay stable and must emit empty arrays, never null.
- Drift in OpenCode version, suite hash, task version or fixture SHA must suppress significance claims; the requested model/agent/variant are constant per invocation, and an overlay-driven change to the effective values is the experiment variable, recorded in the arm's profile hash.

---

### Task 1: Schema and store read model for arms

**Files:**
- Create: `internal/store/migrations/0002_experiments.sql`
- Modify: `internal/store/run.go`, `internal/store/run_test.go`, `internal/store/migrate_test.go`

**Interfaces (Produces):**
```go
type ExperimentArmRow struct {
    ID, ExperimentID, Label string
    ProfileID, OverlayPath, OverlaySHA256 *string
    ProfileHash, OverlayKind, CreatedAt string
}
func (s *Store) InsertExperimentArm(ctx context.Context, a ExperimentArmRow) error
func (s *Store) ListExperimentArms(ctx context.Context, experimentID string) ([]ExperimentArmRow, error) // ordered by label
func (s *Store) GetExperimentArm(ctx context.Context, id string) (*ExperimentArmRow, error)
func (s *Store) RunsForExperiment(ctx context.Context, experimentID string) ([]RunRow, error) // ordered by started_at, id
```
`RunRow` gains `ArmID *string`.

- [ ] **Step 1: Write the failing tests.** In `internal/store/run_test.go` add: arm round-trip via `GetExperimentArm`; `ListExperimentArms` returns label order; duplicate `(experiment_id,label)` returns an error; deleting the experiment cascades arms away; `RunsForExperiment` returns only that experiment's runs with `ArmID` populated. In `migrate_test.go` assert `experiment_arms` exists and that migrating twice is a no-op.
- [ ] **Step 2: Run and observe RED.** `go test ./internal/store -run 'TestExperimentArm|TestRunsForExperiment|TestMigrate' -count=1` → compile failure for missing types.
- [ ] **Step 3: Write the migration.**
```sql
CREATE TABLE experiment_arms (
  id TEXT PRIMARY KEY,
  experiment_id TEXT NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  profile_id TEXT REFERENCES profiles(id),
  profile_hash TEXT NOT NULL,
  overlay_kind TEXT NOT NULL,
  overlay_path TEXT,
  overlay_sha256 TEXT,
  created_at TEXT NOT NULL,
  UNIQUE (experiment_id, label)
);
ALTER TABLE runs ADD COLUMN arm_id TEXT REFERENCES experiment_arms(id);
CREATE INDEX runs_arm_idx ON runs (arm_id);
```
- [ ] **Step 4: Implement the store methods** with `runColumns`/`scanRunRow` extended for `arm_id`, and the arm scanner following the existing nullable-pointer convention.
- [ ] **Step 5: GREEN + commit.** `go test ./internal/store -count=1`, then `git commit -m "feat: add experiment arms schema and store read model"`.

### Task 2: Overlay resolution and environment plumbing

**Files:**
- Create: `internal/experiment/overlay.go`, `internal/experiment/overlay_test.go`
- Modify: `internal/runner/environment.go`, `internal/runner/environment_test.go`, `internal/runner/runner.go`

**Interfaces (Produces):**
```go
type OverlayKind string // "file" | "dir" | "none"
type Overlay struct { Kind OverlayKind; Path, SHA256 string; Env []string }
func ResolveOverlay(path string) (Overlay, error)
// runner
type Request struct { /* existing */; ExtraEnv []string }
func ApplyExtraEnv(env, extra []string) []string // last-write-wins by key, sorted
```
- `ResolveOverlay("")` → `{Kind:"none"}`; regular file → `OPENCODE_CONFIG=<abs>`; directory → `OPENCODE_CONFIG_DIR=<abs>`; anything else → error naming the path. `SHA256` is the file's bytes, or for a directory a lexical walk hashing relative path + contents.

- [ ] **Step 1: Failing tests.** `overlay_test.go`: none/file/dir resolution, absolute paths, missing path error, directory hash stable across two calls and changed by a file edit. `environment_test.go`: `ApplyExtraEnv` overrides a same-key entry, appends new keys, keeps output sorted, and returns the input unchanged for nil extra.
- [ ] **Step 2: RED.** `go test ./internal/experiment ./internal/runner -run 'Overlay|ApplyExtraEnv' -count=1`.
- [ ] **Step 3: Implement** `ResolveOverlay` (reuse `canon.SHA256Hex` for the file case) and `ApplyExtraEnv` next to the existing dedupe helper.
- [ ] **Step 4: Wire the runner.** In `runner.Run`, after `env := BuildEnv(os.Environ(), req.EnvPolicy)` apply `env = ApplyExtraEnv(env, req.ExtraEnv)` and pass that to `Start`; the same env is used for validators, so an overlay applies to validators too — assert that in a test.
- [ ] **Step 5: GREEN + commit.** `go test ./internal/runner ./internal/experiment -count=1`, then `git commit -m "feat: resolve config overlays and apply them past the sandbox"`.

### Task 3: Deterministic statistics

**Files:**
- Create: `internal/stats/stats.go`, `internal/stats/stats_test.go`

**Interfaces (Produces):**
```go
func Wilson(successes, n int, z float64) (lo, hi float64)
func PassAtK(outcomes []bool) int   // 1 when any repeat passed
func PassAllK(outcomes []bool) int  // 1 when every repeat passed
func Median(xs []float64) float64
func IQR(xs []float64) (q1, q3 float64)
func PermutationP(a, b []float64, seed int64, iters int) float64 // two-sided
const DefaultAlpha = 0.05
```
- Pure functions, no I/O. `Median` must copy before sorting so callers' slices are untouched (the CLI already relies on that property in `render_run.go`).
- `PermutationP` uses `math/rand.New(rand.NewSource(seed))`; identical inputs must return the same value for the same seed.

- [ ] **Step 1: Failing tests** with golden values: `Wilson(0,5,1.96)` ≈ `(0, 0.434)`; `Wilson(5,5,1.96)` ≈ `(0.566, 1)`; `PassAtK`/`PassAllK` for all-pass, mixed and all-fail; `Median` odd/even/unsorted/single/empty; `IQR` on a known quartile set; `PermutationP` on identical samples → `> 0.9`, on fully separated samples → `< 0.05`, and equal across two calls with the same seed but differing for a different seed.
- [ ] **Step 2: RED.** `go test ./internal/stats -count=1`.
- [ ] **Step 3: Implement** the functions above. Wilson uses the score interval
```go
// centre = (p + z²/2n) / (1 + z²/n); half = z/(1+z²/n) * sqrt(p(1-p)/n + z²/4n²)
```
with `p = successes/n`, clamped to `[0,1]`, and `n == 0` returning `(0,1)`. `PermutationP` pools `a` and `b`, resamples two groups of the original sizes `iters` times, counts how often `|mean(a*)-mean(b*)| >= |mean(a)-mean(b)|`, and returns `(count+1)/(iters+1)` so it can never be exactly zero. Document both in comments.
- [ ] **Step 4: GREEN + commit.** `go test ./internal/stats -count=1`, then `git commit -m "feat: add deterministic statistics helpers"`.

### Task 4: Experiment orchestration

**Files:**
- Create: `internal/experiment/experiment.go`, `internal/experiment/experiment_test.go`

**Consumes:** `runner.Run`, `profile.Discover`/`Fingerprint`/`Persist`, `store.InsertExperiment*`.

**Interfaces (Produces):**
```go
type ArmSpec struct { Label string; Overlay Overlay }
type Step struct { Task *suite.Task; Arm ArmSpec; RepeatIndex int }
type Request struct {
    Suite *suite.Suite; Tasks []*suite.Task; Arms []ArmSpec; Baseline string
    Paths config.Paths; Profile profile.Options; Adapter opencode.Adapter
    EnvPolicy runner.EnvPolicy; Repeat int; KeepWorktree bool
}
type Outcome struct { ExperimentID string; RunIDs []string }
func ParseArm(spec string) (ArmSpec, error)          // "label=path"; "label=" or bare "label" means no overlay
func BuildPlan(tasks []*suite.Task, arms []ArmSpec, repeat int) []Step
func Run(ctx context.Context, st *store.Store, req Request) (Outcome, error)
```
- `ParseArm` rejects an empty label; `label=` and a bare `label` both mean "no overlay" (`OverlayKindNone`), because comparing the current config against a modified one is the primary use case and spec §12.1 defines a `none` kind. Duplicate labels are rejected by the caller. `Run` requires at least two arms (spec §12.1) and rejects a baseline that names none of them.
- `BuildPlan` is task-major with arms alternating inside each repeat: `t1/A/0, t1/B/0, t1/A/1, t1/B/1, t2/A/0, …`.
- `Run` per arm: `profile.Discover` + `Fingerprint` (with the arm's overlay env) + `Persist`, then insert the arm row with `profile_id`, `profile_hash`, `overlay_kind`, `overlay_path`, `overlay_sha256`; then execute the plan with `runner.Request{ExperimentID, ArmID, RepeatIndex, ExtraEnv: arm.Overlay.Env}`. Each arm uses an adapter built from `opencode.Options{Env: append(os.Environ(), arm.Overlay.Env...)}` so discovery sees the overlay.

- [ ] **Step 1: Failing tests.** `ParseArm` valid/invalid; `BuildPlan` order for 2 tasks × 2 arms × 2 repeats; `Run` with a scripted fake adapter over a temp suite asserts one experiment row, two arm rows with distinct `profile_hash`, and runs carrying `(arm_id, repeat_index)` in plan order.
- [ ] **Step 2: RED.** `go test ./internal/experiment -run 'ParseArm|BuildPlan|TestRun' -count=1`.
- [ ] **Step 3: Implement** the three functions; keep `Run` free of rendering.
- [ ] **Step 4: GREEN + commit.** `go test ./internal/experiment -count=1`, then `git commit -m "feat: orchestrate interleaved A/B experiments"`.

### Task 5: Aggregation and the regression decision

**Files:**
- Create: `internal/experiment/aggregate.go`, `internal/experiment/aggregate_test.go`

**Consumes:** `stats.*`, `store.RunsForExperiment`, `store.ListRunValidations`, `store.GetRunMetrics`, `store.ListExperimentArms`.

**Supporting reader (add to `internal/store/run.go`, mirroring `GetExperimentArm`):**
```go
func (s *Store) GetExperiment(ctx context.Context, id string) (*ExperimentRow, error)
```

**Interfaces (Produces):**
```go
// StatTest carries one computed comparison so the decision stays pure.
// Observed is oriented so positive always means "the arm is worse".
type StatTest struct { Observed, P, BaselineValue, ArmValue float64; N int; Applicable bool }
type ArmTaskStats struct {
    Executions, Successes int
    PassRate, WilsonLo, WilsonHi float64
    PassAtK, PassAllK bool
    MedianTokens, Q1Tokens, Q3Tokens float64
    MedianCost, MedianDurationMS float64
}
type TaskSummary struct { TaskID string; PerArm map[string]ArmTaskStats }
type ExperimentSummary struct {
    Experiment store.ExperimentRow
    Arms []store.ExperimentArmRow
    Tasks []TaskSummary
    CostPerSolved map[string]float64
    DriftWarnings []string
    SignificanceSuppressed bool
    InsufficientData bool
    PassRateTests map[string]StatTest // keyed by arm label; pooled per-execution successes
    CostTests map[string]StatTest     // keyed by arm label; per-task cost per solved task
    Regression *RegressionDecision
}
type RegressionDecision struct { Arm string; Regressed bool; Reason string } // Arm empty when none regressed
func Summarize(ctx context.Context, st *store.Store, experimentID, baseline string) (ExperimentSummary, error)
func DecideRegression(s ExperimentSummary, alpha float64) RegressionDecision
```
The cost test uses a median-difference permutation (`stats.PermutationPMedian`), so add that helper to `internal/stats` in this task (pooled resample, two-sided, `(count+1)/(iters+1)`, seeded) with its own tests.
- Drift guard: differing `opencode_version`, `suite_hash`, `task_version` or `fixture_sha` across arms adds a `DriftWarnings` entry and sets `SignificanceSuppressed`, in which case `DecideRegression` returns `{false, "significance suppressed: …"}`. The requested `model`/`agent`/`variant` are checked too but are constant by construction.
- `InsufficientData` when any arm/task pair has fewer than three executions; `DecideRegression` then returns `{false, "insufficient data"}`.
- Regression per spec §12.4, decided from the two `StatTest`s: pass-rate regression when `PassRateTest.Observed > 0` and `P < alpha`; cost regression when the pass-rate test is not significant, `CostTest.Applicable`, `CostTest.P < alpha`, and the arm's median cost per solved task exceeds the baseline's by more than 25%.

- [ ] **Step 1: Failing tests** with synthetic run sets: identical arms → no regression; arm clearly worse → regression with the pass-rate reason; equal pass rates with a large cost gap → regression with the cost reason; two repeats → insufficient data; differing model strings → suppressed.
- [ ] **Step 2: RED.** `go test ./internal/experiment -run 'Summarize|DecideRegression' -count=1`.
- [ ] **Step 3: Implement**, reading metrics through the existing store readers and grouping by `(arm_id, task_id)`; runs with a nil `ArmID` are ignored.
  - `Summarize` takes the baseline label (no re-derivation later) and computes one `StatTest` **per non-baseline arm**: the pass-rate test permutes pooled per-execution successes (1/0); the cost test permutes per-task cost-per-solved values over the tasks **both** arms solved, where a task's value is `sum(cost over repeats) / successes`. `Observed` is `baseline - arm` for pass rate and `arm - baseline` for cost; `BaselineValue`/`ArmValue` carry the underlying statistics the test permuted, so the 25% gate reads the same numbers as the p-value. The cost test permutes the median difference (`stats.PermutationPMedian`), matching the reported statistic. `Applicable` is false when there are no comparable samples.
  - `DecideRegression` stays pure over the summary plus `alpha`: it never re-reads the store, evaluates **every** non-baseline arm, and returns the worst outcome with `Arm` set to the regressing arm (empty when none regressed).
- [ ] **Step 4: GREEN + commit.** `go test ./internal/experiment -count=1`, then `git commit -m "feat: aggregate experiment results and decide regressions"`.

### Task 6: CLI commands and the versioned export

**Files:**
- Create: `internal/cli/experiment.go`, `internal/cli/experiment_test.go`
- Modify: `internal/cli/root.go` (register `newExperimentCmd`, map the regression sentinel in `exitCode`)

**Interfaces (Produces):**
```go
var ErrRegression = errors.New("experiment: measured regression against the baseline arm")
```
- `experiment run [suite] [task...]` flags: `--profile` (repeatable, required ≥2), `--repeat` (default from config, ≥1), `--baseline`, `--exit-on-regression`, `--json`, `--suite-dir`, `--agent/--model/--variant`, `--inherit-environment`, `--keep-worktree`.
- Human output: per-arm table (executions, pass rate with interval, pass^k, median tokens/cost/duration), then cost per solved task, then drift warnings, then the regression line (`regression: none detected` / `regression: <reason>` / `insufficient data`).
- `experiment show <id> --format jsonl` writes the spec §12.5 envelope, one line per run, `schema_version: 1`, empty arrays as `[]`.
- Exit: usage problems → 2; regression with `--exit-on-regression` → 3; otherwise 0.

- [ ] **Step 1: Failing tests.** Missing/duplicate `--profile` → usage error; `--baseline` naming an unknown arm → usage error; regression with the flag → exit 3 and without it → exit 0; JSONL export has one line per run, `schema_version` 1, and no `null` arrays; empty experiment renders without panicking.
- [ ] **Step 2: RED.** `go test ./internal/cli -run TestExperiment -count=1`.
- [ ] **Step 3: Implement** the command, renderers and exit mapping.
- [ ] **Step 4: GREEN + commit.** `go test ./internal/cli -count=1`, then `git commit -m "feat: add experiment commands and versioned export"`.

### Task 7: Verification

**Files:** none (evidence only).

- [ ] **Step 1: Gates.** `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web`, `make cross`.
- [ ] **Step 2: Live A/B smoke** on the isolated OpenCode data dir: two arm files that differ only in `agent.build.temperature`, `--repeat 3`, one task, asserting interleaving order in the DB, two distinct `profile_hash` values, statistics rendered, and `experiment show --format jsonl` parsing linewise with `jq -e`.
- [ ] **Step 3: Regression path** exercised with a synthetic store fixture (no model spend) proving exit 3.
- [ ] **Step 4: Record evidence** under `docs/superpowers/evidence/` and report PASS/FAIL per check.
