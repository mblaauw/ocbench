# History and Comparison Implementation Plan

> **Historical record.** This plan was executed under a workflow that is no longer in use, so the checkboxes below are not a live tracker — see `docs/roadmap.md` for outstanding work. To execute a plan like this one: work through the tasks in order, write the failing test first, run the gates each task names, and commit each task separately.

**Goal:** Add deterministic read-only `ocbench history` and `ocbench compare` commands over persisted runs.

**Architecture:** Add query-only store APIs and an `internal/history` service that owns selector resolution and comparison semantics. The CLI only parses flags and renders typed results, leaving the service reusable by the Plan 4 dashboard.

**Tech Stack:** Go stdlib, Cobra, modernc SQLite; no new dependencies.

**Spec:** `docs/design.md` (§§5, 6, 9, 10).

## Global Constraints

- Keep SQLite schema v1 unchanged; query-only work adds no migration.
- Use explicit JSON structs and emit empty arrays as `[]`, never `null`.
- Full run UUIDs, `latest`, and `previous` are the only comparison selectors.
- `previous` resolves to the prior non-dry compatible run (same suite/task/version/fixture) relative to the other selector.
- Explicit runs with incompatible suite/task/version/fixture are usage errors; never present them as benchmark deltas.
- Compare reports deltas and profile-component changes, warns when more than one component changed, and never claims causation.
- Tests use temp SQLite stores and injected CLI dependencies only; never call OpenCode or the network.

## Review Focus

- Empty history, missing selectors, and a `previous` selector with no compatible predecessor must be usage-safe and deterministic.
- Numeric metric union must preserve metrics missing from either side and avoid percentage division by zero.
- Same-second run timestamps must not affect selector resolution; use deterministic store ordering.
- Profile differences must be exact component diffs and warning language must avoid causal wording.
- CLI JSON must retain stable keys and empty arrays.

### Task 1: Store read model

**Files:**
- Modify: `internal/store/run.go`, `internal/store/profile.go`
- Modify: `internal/store/run_test.go`, `internal/store/profile_test.go`

**Produces:** `MetricRow`, read methods for run metrics/validations, `GetProfileByID`, `LatestRun`, and `PreviousCompatibleRun`.

- [ ] Write failing tests that seed temp SQLite rows and assert sorted metric/validation reads, profile reconstruction by ID, newest non-dry selection, and previous compatibility filtering.
- [ ] Run `go test ./internal/store -run 'Test(GetRunMetrics|ListRunValidations|LatestRun|PreviousCompatibleRun|GetProfileByID)' -count=1` and observe compile/behavior failures.
- [ ] Implement query methods using `QueryContext`, scanning nullable fields exactly as existing `GetRun` does. `PreviousCompatibleRun` must use `(started_at,id) < (?,?)`, match suite name/version/hash plus task/version/fixture (a changed suite hash under the same name/version is not a controlled comparison), exclude `dry_run=1`, and order descending.
- [ ] Re-run focused store tests, then `go test ./internal/store -count=1`.
- [ ] Commit: `feat: add store read models for history`.

### Task 2: Shared history service

**Files:**
- Create: `internal/history/history.go`, `internal/history/compare.go`, `internal/history/history_test.go`

**Consumes:** Store read APIs and `profile.FromRows`/`profile.Diff`.

**Produces:**
```go
type RunDetail struct { Run store.RunRow; Metrics map[string]float64; Validations []store.ValidationRow; Profile *profile.Profile }
type MetricDelta struct { Name string; Before, After, Delta float64; Percent *float64 }
type Comparison struct { Before, After RunDetail; Metrics []MetricDelta; ProfileChanges []profile.Change; ControlledRunWarning string }
func List(ctx context.Context, st *store.Store, task string, limit int) ([]RunDetail, error)
func Compare(ctx context.Context, st *store.Store, left, right string) (Comparison, error)
```

- [ ] Write failing tests for UUID/latest/previous resolution, incompatible explicit runs, metric union, zero baseline percent (`nil`), and multiple profile-change warning text.
- [ ] Run `go test ./internal/history -count=1` and observe failures.
- [ ] Implement selectors: resolve `latest` first; resolve `previous` against the resolved other run; reject both selectors being `previous`. Sort metric names lexically. Set `Percent=nil` when before is zero.
- [ ] Use `profile.Diff` for component changes. Warning must state the exact count and recommend a controlled run without causal language.
- [ ] Re-run `go test ./internal/history -count=1`.
- [ ] Commit: `feat: add shared history comparison service`.

### Task 3: History CLI

**Files:**
- Create: `internal/cli/history.go`, `internal/cli/history_test.go`
- Modify: `internal/cli/root.go`

**Consumes:** `history.List` and existing `Deps`/error conventions.

- [ ] Write failing CLI tests using temp paths/store fixtures: default history, `--task`, `--limit`, empty result, and `--json` with `runs: []`.
- [ ] Run `go test ./internal/cli -run TestHistory -count=1` and observe failures.
- [ ] Implement `ocbench history [--task T] [--limit N] [--json]`. Reject `limit < 1` as `UsageError`; use tabwriter human output with run ID, time, task, status, profile hash, duration, tokens, and tools.
- [ ] Re-run focused tests and `go test ./internal/cli -count=1`.
- [ ] Commit: `feat: add run history command`.

### Task 4: Compare CLI

**Files:**
- Create: `internal/cli/compare.go`, `internal/cli/compare_test.go`
- Modify: `internal/cli/root.go`

- [ ] Write failing tests for `latest previous`, full UUID comparison, missing/incompatible selectors (exit 2), zero-baseline display, profile warning, and JSON shape.
- [ ] Run `go test ./internal/cli -run TestCompare -count=1` and observe failures.
- [ ] Implement `ocbench compare <a> <b> [--json]`. Render before/after metadata, metric deltas, validation status changes, and `profile.RenderChanges`; print the controlled-run warning only when more than one component changed.
- [ ] Re-run focused tests and `go test ./internal/cli -count=1`.
- [ ] Commit: `feat: add run comparison command`.

### Task 5: Verification and documentation

**Files:**
- Modify: `docs/design.md` only if implementation reveals an ambiguity resolved above.

- [ ] Run `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal`, and `make cross`.
- [ ] Run a local temp-home smoke: seed or create two compatible runs, execute `history --json` and `compare latest previous --json`, and verify no network/OpenCode process starts.
- [ ] Review diff for causal claims, non-empty-array consistency, and read-only behavior.
