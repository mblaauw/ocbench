# Task Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Make a task able to declare what it measures, hide its tests until the agent stops, prove itself with a reference solution, and be graded on process as well as outcome.

**Architecture:** The suite loader gains task metadata (`difficulty`, `capabilities`, `expected_tokens`, suite `tier`) and two new hidden subtrees (`evaluator/tests/`, `evaluator/reference/`). The runner copies hidden tests into the worktree after the changed-file set is computed and before validators run. The validator engine gains a context struct so `diff`, `grep` and `process` kinds can see the worktree, the change set and the event stream, plus per-validator `weight` feeding a new `score` metric.

**Tech Stack:** Go stdlib only (existing deps unchanged), modernc SQLite.

**Spec:** `docs/superpowers/specs/2026-09-22-ocbench-design.md` §7 (task layout, metadata, validator kinds, hidden tests, reference solutions), §9 (process metrics, `score`), §14 (capability vocabulary and honesty rules).

## Global Constraints

- No new dependencies; no network in tests; temp dirs and injected adapters only.
- Strict YAML decoding stays on: every new key must be declared in the loader's raw structs.
- `success` stays binary (every validator passed); `score` is the weighted fraction and never gates.
- Hidden tests are the task's own tests, copied only after the agent stops and after the changed-file set is computed, so they never appear as agent changes.
- A process validator may only check something the prompt asks for (spec §14.3).
- Existing suites and runs must keep working: a task without the new metadata or hidden subtrees behaves exactly as before.

## Review Focus

- Hidden tests must not appear in `changed.json`, `diff.patch`, `files_unexpected` or the worktree during the run — the ordering is the whole point.
- `score` must not become a second pass/fail: `success` stays binary and skipped validators must not distort the fraction.
- A reference solution that accidentally overwrites hidden tests, or a fail-before check that passes on the untouched fixture, must fail the suite test loudly.
- `diff`/`grep`/`process` validators must not be able to read outside the worktree, hang on a huge file, or match a pattern against the wrong corpus.
- Process metrics must be derived from the stored event stream so a parser fix can recompute them.

---

### Task 1: Task metadata and suite tier

**Files:**
- Modify: `internal/suite/suite.go`, `internal/suite/load.go`, `internal/suite/hash.go`, `internal/suite/suite_test.go`
- Modify: `suites/core/suite.yaml` and every `suites/core/tasks/*/task.yaml` to declare `tier: smoke`, `difficulty`, and `capabilities`

**Interfaces (Produces):**
```go
type Task struct { /* existing */; Difficulty string; Capabilities []string; ExpectedTokens int }
type Suite struct { /* existing */; Tier string }
```
- Accepted values: `difficulty` ∈ {`easy`,`medium`,`hard`} (empty allowed); `tier` ∈ {`smoke`,`standard`,`hard`} (empty allowed); `capabilities` ⊆ the §14.1 vocabulary; `expected_tokens` ≥ 0. An unknown value is a load error naming the field and the offending value.
- `hash.go`: include `difficulty` and `capabilities` in the task spec hash; **exclude `expected_tokens`**, which is an estimate that may be retuned without invalidating comparability.

- [ ] **Step 1: Failing tests**: valid metadata round-trips; unknown difficulty, unknown capability, unknown tier and negative `expected_tokens` are load errors naming the field; changing `capabilities` changes the suite hash while changing `expected_tokens` does not; a task with no metadata hashes and loads exactly as before.
- [ ] **Step 2: RED.** `go test ./internal/suite -run 'TestMetadata|TestHash' -count=1`.
- [ ] **Step 3: Implement** the struct fields, validation and hashing; annotate the five core tasks (`difficulty: easy` for all five, `capabilities` from `debugging`, `multi-file`, `search`, `instruction-following`, `restraint` as appropriate) and `suites/core/suite.yaml` with `tier: smoke`.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: add task metadata and suite tiers"`.

### Task 2: Hidden tests

**Files:**
- Modify: `internal/suite/load.go` (expose `Task.HiddenTests fs.FS` from `evaluator/tests/`), `internal/runner/runner.go`, `internal/runner/runner_test.go`
- Add `suites/core/tasks/py-bugfix/evaluator/tests/` as the first real hidden test

**Interfaces (Produces):**
```go
// suite
type Task struct { /* existing */; HiddenTests fs.FS }  // nil when absent
// runner
func copyHiddenTests(task *suite.Task, worktree string) (copied []string, err error)
```
- `copyHiddenTests` walks the subtree and writes each regular file under `worktree` at its relative path, creating directories. It refuses a path containing `..`, refuses to overwrite an existing file, and returns the copied paths.
- Called after `ChangedFiles`/`DiffAgainstBaseline`/`captureUntracked` and before validators, so the copied files cannot appear in the change set. A copy error fails the run as an infrastructure error (it is ocbench's own step, unlike a child-session export).
- `py-bugfix`'s current visible `fixture/tests/test_calc.py` moves to `evaluator/tests/` so the task genuinely hides its tests; its validator command already runs `python3 -m unittest discover -s tests`, which now finds them only because they were copied in.

- [ ] **Step 1: Failing tests**: hidden tests are present in the worktree when validators run; they are absent from `changed.json`, `diff.patch` and `files_unexpected`; a task with no hidden tests is unaffected; a hidden tree containing `../escape` is rejected without writing; an existing file is not overwritten.
- [ ] **Step 2: RED.** `go test ./internal/runner -run TestHiddenTests -count=1`.
- [ ] **Step 3: Implement** the loader exposure, the copier and the runner call; move `py-bugfix`'s tests to `evaluator/tests/`.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: hide task tests until the agent stops"`.

### Task 3: Reference solutions and the fail-before/pass-after suite test

**Files:**
- Modify: `internal/suite/load.go` (expose `Task.Reference fs.FS` from `evaluator/reference/`)
- Create: `internal/suite/honesty_test.go`
- Add reference trees: `suites/core/tasks/{config-yaml-fix,py-bugfix,multi-file-feature}/evaluator/reference/` and `suites/core/tasks/{repo-investigation,code-review}/evaluator/reference/answer.txt`

**Interfaces (Produces):**
```go
func applyReference(task *suite.Task, dir string) (applied []string, err error)  // test helper
```
- The suite test iterates every task of every embedded suite and, for each: copies `fixture/` into `t.TempDir()`, runs the task's validators through the real `evaluation.RunValidator` and asserts the task **fails**; applies `evaluator/reference/` (or feeds `answer.txt` to the answer validator) and asserts it **passes**. Command validators that `requires` something absent from the machine are skipped, not counted as failure.
- Reference trees are ordinary files copied over the fixture (no patch tooling), so they are reviewable in a diff.

- [ ] **Step 1: Write the failing test** with one task already covered (`config-yaml-fix`) and assert the harness reports a task whose validators pass untouched as a failure of the *test*, with a message naming the task.
- [ ] **Step 2: RED.** `go test ./internal/suite -run TestEveryTaskFailsUntouchedPassesWithReference -count=1`.
- [ ] **Step 3: Author the references** for the four remaining core tasks: `config-yaml-fix` (fix the YAML key), `py-bugfix` (`a*b` → `a/b`), `multi-file-feature` (implement `summarize` across the three files), and `answer.txt` for `repo-investigation` (naming `maximum` and the correction) and `code-review` (all three defects).
- [ ] **Step 4: GREEN + commit.** `go test ./internal/suite -count=1`, then `git commit -m "test: prove every core task fails untouched and passes with its reference"`.

### Task 4a: `diff` and `grep` validators

**Files:**
- Modify: `internal/evaluation/validator.go`, `internal/evaluation/validator_test.go`, `internal/runner/runner.go`
- Modify: `internal/suite/load.go` (accept the new validator keys)

**Interfaces (Produces):**
```go
type ValidatorContext struct {
    Worktree string
    Changed  []string
    Diff     []byte
    Events   []Event
    FinalAnswer string
}
func RunValidator(ctx context.Context, seq int, spec ValidatorSpec, vctx ValidatorContext, env []string, timeout time.Duration) ValidationResult
type ValidatorSpec struct { /* existing */; RequiredPaths, ForbiddenPaths, Present, Absent []string; MaxLines int; Weight float64 }
```
- This replaces the current `(spec, dir, env, timeout, finalAnswer)` parameter list; every call site and test updates. `command` and `answer` behave exactly as before.
- `diff`: every `required_paths` glob must match at least one changed path; no `forbidden_paths` glob may match one; `max_lines` (when > 0) bounds `diff_lines_added + diff_lines_removed`. Failure output names the offending path or the counts.
- `grep`: `present` patterns must each match at least one file in the worktree; `absent` patterns must match none. The scan skips `.git`, files over 1 MiB and non-regular files, and is bounded to the worktree root (rejecting symlinked escapes the same way `captureUntracked` does).
- `Runner` builds one `ValidatorContext` per run and passes it to every validator.

- [ ] **Step 1: Failing tests**: required/forbidden/max-lines pass and fail cases; a `grep` `absent` pattern that appears in a comment fails; a `grep` scan ignores `.git` and a 2 MiB file; a symlink pointing outside the worktree is not followed; `command`/`answer` results are unchanged under the new signature.
- [ ] **Step 2: RED.** `go test ./internal/evaluation -run 'TestDiffValidator|TestGrepValidator' -count=1`.
- [ ] **Step 3: Implement** the context struct, the two kinds, the loader keys, and the runner wiring.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: add diff and grep validators"`.

### Task 4b: `process` validator, weights and `score`

**Files:**
- Modify: `internal/evaluation/validator.go`, `internal/evaluation/validator_test.go`, `internal/runner/artifacts.go`, `internal/runner/runner.go`, `internal/runner/runner_test.go`
- Modify: `internal/suite/load.go` (accept `weight` and `tool_pattern`)

**Interfaces (Produces):**
```go
// ValidatorSpec gains
ToolPattern string   // process
Weight      float64  // all kinds, default 1
// artifacts.go
func scoreOf(validations []evaluation.ValidationResult) (float64, bool) // value, present
```
- `process`: passes when at least one tool call in `vctx.Events` has a `tool` or `state.input.command`/`state.input.description` matching `ToolPattern` (Go regexp). Failure output lists the tool calls that were seen.
- `scoreOf`: weighted passed ÷ weighted total over validators whose status is not `skipped`; returns `present=false` when there is nothing to score, and `derivedMetrics` then omits `score` rather than writing zero.
- `success` remains binary.

- [ ] **Step 1: Failing tests**: a `process` validator passes when the pattern matched a tool call and fails when it did not, with the seen tool calls in the output; a malformed pattern is `error`; weights change `score` but not `success`; all-skipped yields no `score` key; `score` appears in `result.json` and `run_metrics`.
- [ ] **Step 2: RED.** `go test ./internal/evaluation ./internal/runner -run 'TestProcessValidator|TestScore' -count=1`.
- [ ] **Step 3: Implement** the kind, the weight plumbing, `scoreOf` and the metric.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: add process validators, weights and score"`.

### Task 5: Process metrics

**Files:**
- Modify: `internal/evaluation/metrics.go`, `internal/evaluation/metrics_test.go`, `internal/runner/artifacts.go`, `internal/runner/runner_test.go`

**Interfaces (Produces):**
```go
// evaluation
type Metrics struct { /* existing */; FirstEditMS int64; ToolCallsBeforeFirstEdit, RedundantReads, VerificationCommands int }
```
- `time_to_first_edit_ms`: from the first event's timestamp to the first `edit`/`write`/`patch` tool call; omitted (not zero) when no edit occurred.
- `tool_calls_before_first_edit`: tool calls seen before that edit.
- `redundant_reads`: `read` tool calls whose `state.input.filePath` was already read in this run.
- `verification_commands`: tool calls whose `state.input.command` matches `(unittest|pytest|go test|npm test|cargo test|make test|lint)`.
- All four are emitted through `MetricsMap` so they land in `run_metrics` with no schema change.

- [ ] **Step 1: Failing tests** over synthetic streams: no edit omits `time_to_first_edit_ms`; two reads of the same path count one redundant read; a `go test` command counts as verification; tool calls before the first edit are counted correctly.
- [ ] **Step 2: RED.** `go test ./internal/evaluation -run TestProcessMetrics -count=1`.
- [ ] **Step 3: Implement** the counters and emit them.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: record process metrics"`.

### Task 6: Verification

**Files:** none (evidence only).

- [ ] **Step 1: Gates.** `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web`, `make cross`.
- [ ] **Step 2: Live smoke on `py-bugfix`** with the isolated OpenCode data dir: confirm the hidden test is absent from the worktree during the run and present at validation time (assert from the run directory and the validator log), and that `score` is recorded.
- [ ] **Step 3: Confirm the honesty test runs for every core task** and prints one line per task.
- [ ] **Step 4: Record evidence** under `docs/superpowers/evidence/` with a PASS/FAIL table.
