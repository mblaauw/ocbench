# Evidence — dashboard phases 1 and 2 (Overview and Runs)

Date: 2026-09-24. Branch: `plan-8-overview` (`d25fc42`..`7d4b4fb`).

This records what the read-only dashboard was observed to do against the live
store, not what it is intended to do. Screenshots live in `.playwright-mcp/`
(ignored by git); the figures below are copied from the rendered pages.

## What was built

| Phase | Page | Commits |
|---|---|---|
| 1 | `/` Overview — hero, profile leaderboard, suite matrix, score/cost scatter | `321a34b`, `2b75e91`, `76fb77c`, `db41716` |
| 2 | `/runs` Runs — filters, runs table, selected-run aside, architecture panel | `7d4b4fb` |

Supporting read models: `internal/profile/view.go` (composition decode,
`Architecture()`, `Summary()`), `internal/profile/compare.go` (`DiffNotes`),
`internal/history/overview.go` (`Overview`, seeded bootstrap intervals,
suite-hash exclusion), `internal/history/usage.go` (`AgentUsages`),
`internal/stats` (`BootstrapCI`), `store.ListProfiles`, `store.CountRuns`.

## Live smoke test

Store: `~/.local/share/ocbench/ocbench.db`, 13 runs over 5 profiles, 4 suites.
Served with `ocbench serve` on `127.0.0.1:8787`.

**Overview** (`/`) rendered: 5 profiles · 2 scored · 13 runs · 4 suites · 2 runs
excluded as stale-suite-hash. Leader `build · deepseek-v4.1-flash high · +6 sub`
at score 1.00, pass 100%, cost per solved task $0.003, median tokens 71k over 9
runs, verdict "not distinguishable".

**Runs** (`/runs`) rendered all 13 runs newest-first across 8 columns
(Status · Task · Profile · Score · Time · Tokens · Diff ± · Started). The
`failed` filter narrowed the table to 1 of 13 and selected that run, whose aside
showed the failing `command` validator with its Go test excerpt
(`--- FAIL: TestPageExactBoundary (0.00s) … next=<nil>`), score 0.50, 108k
tokens, 35.9s, $0.0084.

Screenshots: `overview-1.png`..`overview-3.png`, `runs-1.png`..`runs-4.png`,
`runs-filtered.png`.

## Findings fixed during the smoke test

Each of these was a real defect the browser exposed, not a hypothetical:

- **CSP was violated by the page itself.** Inline `style=` attributes, the
  fonts and the favicon were all blocked. Dynamic geometry now uses SVG
  attributes and discrete tint classes; fonts are same-origin under
  `font-src 'self'`. A clean load of `/` and `/runs` reports **zero console
  messages**.
- **Runs that predate the `score` metric scored 0.00.** The read model fell back
  to `success`, which is what made `ec99cadb` show 1.00 in the matrix.
- **The `Started` column was clipped** because the table outgrew its grid
  column. The stamp is now a two-line date/time cell.
- **Scatter labels collided** where profiles tie on score; they are staggered.
- **An unknown run id rendered a different run.** `/runs/{id}` and `?run=` now
  return 404 instead of silently answering with the newest run.

## Test coverage

`internal/history/usage_test.go` — metric-name parsing and heaviest-first order,
ignoring run-scoped names.

`internal/web/server_test.go` — excerpt escaping and no raw-artifact leakage on
the runs page; 404 for an unknown or filtered-out run; and the per-agent panel
for a delegating run. The last one exists because **the live store holds only
single-agent runs**, so the subagent share bars would otherwise never be
exercised by real data; the test seeds `build`/`explore`/`general` usage and
asserts the 75%/25% shares are emitted as SVG geometry.

## Gates

`go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web` (empty) and
`make cross` (linux amd64 + arm64, CGO_ENABLED=0) all pass at `7d4b4fb`.

## Not yet verified

- The Runs page has not been exercised against a run with real subagent usage;
  that path is covered by test only.
- Pagination does not exist: the runs table renders every persisted run.
- The dashboard has not been viewed at widths below ~1100px.
