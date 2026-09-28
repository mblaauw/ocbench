# Deep-Scan Remediation Plan

## Goal

Remove bloat and stale code left by the cohort-first refactor, fix the bugs the
deep scan confirmed, and share the helpers that are currently duplicated. The
binding design is [design.md](../design.md); decisions with trade-offs go to
[decisions.md](../decisions.md).

## Findings and decisions

- **Cost absence must mean "ineligible", not zero.** `history` treated a missing
  `cost` metric as 0, which deflated cost-per-solved and disagreed with
  `experiment`, which requires cost on every execution. History now uses the
  same rule.
- **Dry runs are diagnostics, never evidence.** `variance` and `calibration`
  now skip them like every other read model.
- **The overview hero is deleted, not restored.** Its template was removed in
  the cohort-first refactor; the remaining view-model and the significance
  permutation test ran on every `/overview` request for nothing. The prototype
  reference and git history retain the design.
- **Counts are inventory; statistics are evidence.** `CountRuns` and
  `runCountsByProfile` keep counting dry runs deliberately.
- **`handleProfiles` keeps its silent decode fallback.** A row that cannot be
  decoded still links to the profile and shows blank rather than a wrong
  summary.
- **`yesNo` stays duplicated** in `report` and `cli`: a shared package for one
  bool formatter costs more than it saves.
- **`Persist` keeps its ID mutation** for now, with a clarifying comment; the
  snapshot flow relies on the returned profile carrying the stored id.

## Batch 1 — bugs

1. Cost-absence parity in `internal/history/overview.go` (`scoreProfile`).
2. Dry-run filtering in `internal/history/variance.go` and `calibration.go`.
3. `--split` validation before the harvest engine call in `internal/cli/harvest.go`.
4. Remove the dead per-arm adapter `Env` in `internal/cli/experiment.go`.
5. Compute the architecture stat strip once per request in `internal/web/profiles.go`.
6. Remove the dead `OverviewReport.OpenCodeVersion` field and assignment.

Every item is written test-first; the focused test must fail for the right
reason before the fix.

## Batch 2 — dead code

Remove the orphaned overview hero (view model, helpers, `history.finish` and
its fields), dead `server.go` helpers and `runSummary` fields, unused
`profilePageView` fields, dead CSS/JS hooks, stale comments, and unused
exports (`store.ListRunsByProfile`, `stats.RepeatsFor`, `report.markdownCell`,
`profile/view.go itoa`, the `Descripion` typo; unexport `Real.Run` and
`profile.Latest`).

## Batch 3 — shared helpers

One UUID generator (`internal/id`), one cohort-arm predicate, one CLI
`writeJSON`, a parametrised permutation test plus `stats.MedianOK`, and the
smaller dedups (env name, run score/duration, profile chips, stamp formatting).

## Verification

Each batch: `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web`,
`make cross`. After Batch 3 the three-commit diff gets an independent review and
verification pass before it is pushed.
