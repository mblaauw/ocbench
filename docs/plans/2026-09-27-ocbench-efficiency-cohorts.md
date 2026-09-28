# Efficiency Cohorts and Portable Reports Implementation Plan

## Goal

Make ocbench an evidence-gated **cost-and-context optimiser** for OpenCode
configurations. It must answer: *which configuration delivers the same
validated outcomes with fewer tokens and lower cost?* The immediate sharing
mechanism is a portable report; a hosted hub and importing other users' data
are deliberately deferred.

## Decisions taken

- A persisted **armed experiment** is the benchmark cohort. Unarmed experiment
  rows are single-profile run sessions, not comparisons, and are excluded. An
  armed experiment already pins the suite hash, task selection, arms, baseline
  and repeat count, and its runs are interleaved. A second, overlapping cohort
  table would only create two sources of truth.
- A profile remains identified by its resolved `profile_hash`. Repeats of the
  same hash stack inside a cohort; runs from different cohorts do not silently
  form one ranking.
- Efficiency is ranked only after deterministic validators establish an
  acceptable outcome. A configuration that fails cheaply is not efficient.
- Tokens and provider cost may be compared within an identical cohort. Wall
  clock remains diagnostic and is never used for a cross-runner ranking.
- Runner environment is recorded as a privacy-preserving execution stratum
  (OS, architecture and CPU count), not as hostname or a machine identifier.
- The dashboard refuses to name a winner from legacy/unscoped history or fewer
  than three repeats per task. It tells the user which controlled cohort to
  inspect instead.
- `ocbench report <experiment> --format md|html` is the manual exchange bridge.
  It contains only stored, redacted facts and no raw events, prompts, session
  exports or artifact paths.

## Global constraints

- Standard library first; no new dependency.
- Statistics remain derived at read time and are never persisted.
- Tests precede every behaviour change.
- Every task passes `go test ./... -count=1`, `go vet ./...`,
  `gofmt -l cmd internal web`, and `make cross`.
- One commit per task. Update README, design/decisions and roadmap with the
  implementation; do not commit local configuration or benchmark artifacts.

## Tasks

### 1. Record runner environments

- Add an append-only migration and `RunRow` field for a canonical runner
  environment (`GOOS`, `GOARCH`, logical CPU count).
- Have the runner capture it once per run, including dry runs.
- Preserve old rows as an explicit unknown environment.

*Acceptance:* runs from a current binary carry a stable, non-host-identifying
environment string; a store round-trip and runner unit test pin it.

### 2. Treat experiments as controlled cohorts

- Add a cohort-oriented read model over one persisted experiment, with one
  profile/arm summary per task and its repeat coverage.
- Gate cost/token standings until every compared arm has at least three runs
  for every selected task and each arm satisfies deterministic validation.
- Do not use wall-clock duration in the standing.

*Acceptance:* a one-repeat experiment renders an explicit no-ranking state;
three successful repeats per task produces an eligible efficiency standing;
a cheaply failing arm is excluded.

### 3. Add the portable report

- Add `ocbench report <experiment-id> --format md|html` over the cohort read
  model. HTML is standalone and has no remote assets.
- Include cohort identity, arms/profile hashes, runner-environment strata,
  per-task repeats and aggregate token/cost/pass facts, plus the evidence gate.
- Keep `experiment show --format jsonl` as the machine-oriented export.

*Acceptance:* report output is deterministic, escapes stored values, refuses an
unknown id as a usage error and writes no rows.

### 4. Make the dashboard cohort-first

- Add a Cohorts page listing persisted experiments and evidence status.
- Add a cohort detail page that renders the same cost-efficiency standing as
  the report. Make this the route linked by the dashboard rather than pooling
  all historic runs into a winner.
- Keep the existing profile Overview as exploratory history, but replace its
  winner claim with an instruction to choose a controlled cohort when no cohort
  is selected.

*Acceptance:* the dashboard never names a global winner from mixed history;
the selected cohort shows coverage, eligibility and runner environments.

### 5. Deferred deliberately

- Importing external reports, a hosted registry, user identity and public
  leaderboards.
- Cross-runner latency ranking.
- Quality-discriminating task authoring. Saturated deterministic tasks remain
  valid efficiency workloads but do not support quality claims.
- Power-derived repeat defaults and model/provider pricing capture; these
  remain the next validity slice after the cohort workflow is usable.
