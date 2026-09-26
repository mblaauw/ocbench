# Evidence — measurement validity (slices B–E)

Date: 2026-09-26. Commits `9e25b2b`, `acecb15`, `277c80d`, `445353e`, `2c790e9`.

The plan is [2026-09-25-ocbench-measurement-validity.md](../plans/2026-09-25-ocbench-measurement-validity.md).
This records what the new instruments reported against the live store, not what
they are intended to report.

## What the corpus looks like

`ocbench calibrate` over 13 runs, 9 tasks:

| Task | Suite | Runs | Pass | Verdict |
|---|---|---|---|---|
| go-pager-cursor | hard | 2 | 50% | **informative** |
| repo-investigation | core | 2 | 100% | saturated |
| go-slice-bug | standard | 3 | 100% | saturated |
| buried-fact, restraint-test-edit, rules-compliance, js-allocate-cents, node-async-bug, sql-report-fix | — | 1 each | 100% | unclassified |

**One task of nine can show a quality difference.** Two are saturated, six have
been measured once and cannot be classified at all. Nothing is always-fail.

`go-pager-cursor` is also the only task with more than one process signature
(2 distinct). Every other task reads `identical (1 config)` — not because
configurations behaved the same way, but because only one configuration was ever
measured on it. The command distinguishes those two cases, and no live task
trips the "identical across configs" case.

## What the noise floor looks like

`ocbench variance` over the same store:

| Task | Axis | N | Rel SD | MDE @ 5 | Repeats for 10% |
|---|---|---|---|---|---|
| go-slice-bug | tokens_total | 3 | 0.7% | 1.3% | 1 |
| repo-investigation | tokens_total | 2 | 0.1% | 0.2% | 1 |
| repo-investigation | duration_ms | 2 | 2.6% | 4.7% | 2 |
| go-slice-bug | duration_ms | 3 | 6.5% | 11.5% | 7 |
| go-pager-cursor | tokens_total | 2 | 17.7% | 31.4% | 50 |
| go-pager-cursor | duration_ms | 2 | 11.6% | 20.6% | 22 |
| go-pager-cursor | **score** | 2 | **47.1%** | 83.5% | **349** |

Three findings, all of which the dashboard now states rather than hides:

1. **Token accounting is precise.** 0.1–0.7% spread on the repeatable tasks, so a
   ~1% token difference is detectable at five repeats per arm. Token and cache
   tuning is viable today.
2. **Wall-clock is a weak metric.** It is 3–9x noisier than tokens on the same
   runs (2.6% vs 0.1%, 6.5% vs 0.7%), which is why it stays tertiary.
3. **Quality on the one informative task is unmeasurable at any sane budget.**
   `go-pager-cursor`'s score spread is 47.1%, so detecting a 10% quality
   difference would take **349 repeats per arm**. The task that can show a
   quality difference is also the task too noisy to measure it on.

Every row is flagged `*`: the whole noise estimate rests on two or three
observations, so the spreads are themselves uncertain by 35–70%. They are a
reason to run more, not a result.

## Config attribution (the re-baseline)

`ocbench snapshot` after the config split, against the previous profile:

```
autoupdate  added
command/diagram  added
command/imagine  added
command/pentest  added
command/sysinfo  added
compaction  added
config  changed  b47c4d4 -> 69fd435
environment  changed  23ac8ce -> 4e77a5a
formatter  added
lsp  added
mcp/playwright  changed  f9d95d6 -> 195e5a3
primary  changed  092062c -> 00d7713
provider/opencode-go  added
share  added
```

Before the split, every one of those rows read `config changed` and nothing more.
`config changed` still appears because `watcher`, `tool_output`, `attachment`,
`enabled_providers` and `subagent_depth` remain in the catch-all, which is the
designed residual.

**The re-baseline cost is real and is now visible in the store:** the five
recorded profiles are a legacy cohort, and the snapshot added two new profiles
with no runs. The leaderboard is not meaningful again until the corpus is re-run.

Two rows deserve attention before attributing anything to the split: `primary
changed` and `mcp/playwright changed` mean the real configuration had already
drifted since `ac83af1e` was recorded. The untracked `opencode.json` in this
repository affects MCP discovery.

## Cache hit rate

The leaderboard separates two profiles that are identical on the two metrics it
used to lead with:

| Profile | Score | Pass | Cache hit | Tasks | Runs |
|---|---|---|---|---|---|
| ac83af1e | 1.00 | 100% | **80%** | 7 | 9 |
| ec99cadb | 1.00 | 100% | **65%** | 1 | 2 |

Same score, same pass rate, a 15-point difference in the share of prompt tokens
served from cache. That is the axis where these two configurations actually
differ, and it was invisible before.

`tokens_cache_write` is **0 in every raw event**: OpenCode emits
`"cache":{"write":0,…}`. The parser is correct and the provider does not populate
it, so the dashboard states that cache writes are not reported instead of showing
a zero it never measured.

## The leaderboard states its own evidence

The verdict now reads:

> **not distinguishable** — no detectable difference between *build ·
> deepseek-v4.1-flash high · +6 sub* and *build · deepseek-v4.1-flash high · +6
> sub*: the task scores did not vary, so no effect size can be estimated from
> them
> gap 0.00 · smallest resolvable — over 7 task(s)

The ranking is gated on the effect the sample could detect, not on a p-value
alone, and each row carries its task count and what that sample could resolve.

## Test coverage added

- `internal/stats/power_test.go` — normal quantiles against textbook values,
  sample standard deviation refusing to estimate from one observation, MDE and
  `RepeatsFor` agreeing in both directions, `MDEForRelSD` matching `MDE`.
- `internal/history/variance_test.go` — the noise floor from repeats, pooled
  within-group normalisation (a 2x level difference is not noise), zero spread
  promising nothing, suite and task filters, thin spreads marked indicative.
- `internal/history/calibration_test.go` — pass-rate classification, process
  variance from identical versus differing signatures, suite filtering.
- `internal/history/cache_test.go` — the hit rate including the
  no-prompt-tokens case.
- `internal/history/overview_test.go` — task count and detectable effect, refusal
  to rank from one task each, the effect gate, and pooled cache hit rate.
- `internal/cli/variance_test.go`, `internal/cli/calibrate_test.go` — human and
  JSON rendering, and the statements each makes when the data supports nothing.

**A test caught a real bug.** The first variance implementation concatenated
normalised samples across groups and took their standard deviation, which
understates the spread because duplicating each group's deviations inflates the
denominator without adding information. It reported 0.0894 where the pooled
within-group spread is 0.10. The fix pools variances weighted by degrees of
freedom.

## Gates

`go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web` (empty) and
`make cross` (linux amd64 + arm64, CGO_ENABLED=0) all pass at `2c790e9`.

## Not yet done

- **The corpus has not been re-run** under the new fingerprint.
- **No `regression` tier exists.** Saturated tasks are classified but not yet
  marked as guards rather than evidence; that needs a suite-layout decision.
- **Slices F and G** — harvesting tasks from real session history, and
  confirmation by replication — are not started.
- The calibrate report is CLI-only; the dashboard does not surface verdicts.
