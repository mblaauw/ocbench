# Roadmap

What is done, what is next, and what is still unscheduled. The binding design is
[design.md](design.md); the reasoning behind past decisions is
[decisions.md](decisions.md).

## Status at a glance

| Slice | State | Key commits |
|---|---|---|
| Foundation — CLI, canon, config, store, profile, adapter | **done** | `3af3f79`..`82896d5` |
| Runner — suite loader, core suite, fixtures, worktrees, sandbox, metrics, validators, sessions, `run` | **done** | `2efb13c`..`48d41b9` |
| History and comparison — store read model, `history`, `compare` | **done** | `882b7e1`..`806f30b` |
| Dashboard — embedded web, safe routes, `serve` | **done** | `10b5a9d`..`42a8330` |
| Dashboard — profile-first Overview, Runs and Architecture pages | **done** | `d25fc42`..`bbc758a` |
| Measurement validity — noise floor, effect size, config attribution | **in progress** | `9e25b2b`..`277c80d` |
| Experiments — arms, overlays, statistics, regression gate, JSONL export | **done** | `2db8b81`..`a29748a` |
| Subagents — session parsing, child capture, per-agent metrics, `trace` | **done** | `840bbc9`..`f08645f` |
| Task infrastructure — metadata, hidden tests, references, new validators | **done** | `acf0f83`..`6472c50` |
| Agentic suite — delegation, restraint, project rules, buried fact | **done** | `fb2f025`, evidence `2026-09-24` |
| Difficulty tiers — `standard` and `hard` suites, Go/JS/SQL fixtures | **done** | `standard`, `hard` suites |

## Landed: task infrastructure and the agentic suite

Plan [2026-09-24-ocbench-task-infrastructure.md](plans/2026-09-24-ocbench-task-infrastructure.md)
is implemented: task metadata (`difficulty`, `capabilities`, `expected_tokens`,
suite `tier`), hidden tests copied in after the agent stops, reference solutions
with an automated fail-before/pass-after test over every task, `diff`/`grep`/
`process` validators, weighted `score` beside binary `success`, and process
metrics. Two plan items remain open: the plan's own verification task is
superseded by the evidence file below, and no task yet populates the `hard` tier.

`suites/agentic/` holds four tasks, one per agentic capability:

| Task | Capability | Proof |
|---|---|---|
| `delegation-sweep` | delegation, search | live run: three subagents, 37.6k subagent tokens against the parent's 24.4k, `process` and `answer` both passed |
| `restraint-test-edit` | restraint | `diff` forbids `tests/**`, requires `calc.py`, caps the change at 10 lines |
| `rules-compliance` | instruction-following | two command checks plus `grep` (no tabs) and `diff` (changelog untouched) |
| `buried-fact` | search, context | 30-file tree with the real `MAX_RETRIES = 7` among decoys |

Verification: [evidence/2026-09-24-agentic-suite-verification.md](evidence/2026-09-24-agentic-suite-verification.md).
Three defects were found while authoring it and are fixed: hidden tests landed
at the worktree root instead of `tests/`, stale Python bytecode made a fixed
task still fail, and a `grep` validator read a binary `.pyc` as text.

## Landed: difficulty tiers and multi-language fixtures

Two new suites, both covered by the honesty test (every task fails untouched
and passes with its reference):

**`standard`** (`tier: standard`, 600 s default) — tasks that need real
debugging rather than a one-line fix:

| Task | Language | Shape |
|---|---|---|
| `go-slice-bug` | Go | a chunking helper whose loop bound drops the last partial chunk; hidden edge tests for size 1, oversize, and exact multiples |
| `node-async-bug` | JavaScript | an un-awaited fan-out that loses input order and swallows rejections; hidden tests for rejection and ordering |
| `sql-report-fix` | SQL (sqlite3) | an inner join that hides customers with no orders; a hidden second dataset catches a fix tuned to the visible seed |

**`hard`** (`tier: hard`, 900 s default) — tasks whose boundary conditions a
straightforward implementation gets wrong:

| Task | Language | Shape |
|---|---|---|
| `go-pager-cursor` | Go | cursor pagination that loses rows when ranks tie; the visible test uses distinct ranks only, the hidden test walks every page size |
| `js-allocate-cents` | JavaScript | splitting an amount without losing a cent — largest remainder with ties to the left, validated over several totals |

**Live characterisation says none of it is hard yet.** A first pass with a cheap
model at its lowest variant passed ten of eleven runs — both `hard` tasks among
them — at 50–110k tokens and 13–42 seconds per task. The one failure was a task
defect (a prompt and its hidden test disagreed about the pager cursor), fixed in
`bd03618`. See
[evidence/2026-09-24-live-suite-characterisation.md](evidence/2026-09-24-live-suite-characterisation.md).

So `tier` is a declaration, not a measurement: `hard` currently means "an
implementation without care gets the boundary wrong", not "a strong model
usually fails". Making the tiers real is task authoring — the machinery
(hidden tests, reference proofs, `diff`/`grep`/`process` validators, partial
credit) is in place, but the fixtures are still small and mostly single-file.

## Landed: the profile-first dashboard

The dashboard answers one question — *did this configuration change help?* — from
the stored runs, without a live probe of the machine it renders on. Direction C
of the prototype: JetBrains Mono throughout, zero corner radius, dark only.

Two of the four planned pages are done, and they are a vertical slice rather than
a scaffold: Overview scores every profile that has runs, and Runs lists every run
with the selected run's validators and per-agent roll-up beside it.

| Page | Route | State |
|---|---|---|
| Overview | `/` | hero, profile leaderboard, suite matrix, score/cost scatter |
| Runs | `/runs`, `/runs/{id}` | status and profile filters, runs table, selected-run aside with validators and architecture |
| Architecture | `/arch`, `/arch/{hash}` | agents, prompts, instruction text, skills, MCP, permissions, subagent tree, profile comparison |
| Suites and tasks | `/suites`, `/suites/{name}` | planned — needs task metadata persisted with the run |

Scoring rules, all derived at read time: a task's score is the mean of its runs'
`score` metric (falling back to `success` for runs recorded before that metric
existed); a suite's score is the mean over its tasks *that have runs*; the
overall figure is weighted by task count (core 5 · standard 3 · hard 2 · agentic
4); cost per solved task divides total cost by runs with `success = 1`; and a
suite is scored only from runs carrying its most recent recorded `suite_hash`,
with the older runs counted and disclosed as excluded.

The Architecture page reads the capture files beside a profile
(`profiles/<hash>/agents.json`, `skills.json`, `instructions.json`) for the text
the fingerprint keeps only as a hash. Those files use a different shape from the
component JSON — the model is an object, not a string — so they are parsed
separately. Its comparison view renders component diffs as meaning
(`tools +lsp -remote_exec`) rather than as hashes.

Verification, including the defects the browser exposed:
[phases 1-2](evidence/2026-09-24-dashboard-phases-1-2.md),
[phase 3](evidence/2026-09-25-dashboard-phase-3.md).

## Landed: measurement validity (in progress)

Plan [2026-09-25-ocbench-measurement-validity.md](plans/2026-09-25-ocbench-measurement-validity.md).
A sanity check over the live store found the harness faithful but the corpus and
the attribution unable to answer the question it exists for: quality was
saturated (10 of 11 scored runs at 1.00), six of nine tasks had been measured
once, and every config change hashed into one `config` component whose diff read
only `"configuration"`.

Done so far:

- **`ocbench variance`** — the run-to-run noise floor of each task, measured from
  runs that repeated the same configuration on it, and the repeats needed to
  detect a 5% or 10% effect. Tasks without repeats are reported as having no
  estimate rather than being averaged. Thin spreads (fewer than five
  observations) are flagged, because a spread from two or three runs is itself
  uncertain by 35% or more. `stats` gained `MDE`, `RepeatsForSD` and `ZFor`.
- **The leaderboard states its own evidence** — each profile carries its task
  count and the smallest difference that sample could resolve, and the ranking
  verdict is gated on the effect size rather than on a p-value alone.
- **Config attribution** — `command`, `formatter`, `lsp`, `mode` and `provider`
  are split per entry; `autoupdate`, `compaction`, `share` and `tools` are
  promoted whole. A change to one setting is now named
  (`share: disabled→enabled`, `compaction: prune`) instead of reported as
  `configuration`. **This re-hashes every profile**, so the recorded corpus is
  now a legacy cohort and must be re-run before the leaderboard is meaningful.

Remaining from the plan: cache hit rate (D), the discriminative-power screen and
corpus calibration (E), harvesting tasks from real session history (F), and
confirmation by replication (G).

## Unscheduled review items

Numbering is from the original external review, kept so nothing is lost.

**Task quality** — remaining from the original review: `ast` and `json_schema`
validator kinds, an optional `llm_judge` reported separately from the
deterministic score, and tasks that exercise a specific skill or MCP server so
adding one shows up as a measurable difference. Hidden tests, reference
solutions, task metadata and the `diff`/`grep`/`process` validators are done.

**Capture depth** — the Architecture page consumes `agents.json`,
`instructions.json` and `skills.json`; the remaining item is to split the
catch-all `config` profile component into
`command/<name>`, `lsp/<lang>`, `formatter/<name>`, `provider/<id>`,
`mode/<name>`, `compaction`, `share`, `autoupdate`, `instructions` globs and
`tools` toggles (#16); capture the full agent definition including prompt text
(#17); render component diffs as meaning rather than hashes (#18); record each
MCP server's tool list and schema hash (#19); estimate the context budget a
profile spends before any work starts (#20); add `profile lint` for dangling
skill paths, unreachable subagents and conflicting permissions (#21).

**Architecture graph** — the Architecture page now lists the primary-agent to
subagent edges and renders a profile-against-profile diff. Still open from #22
and #23: an actual graph (rather than a list) with skills and MCP servers
attached to each node, a visual graph diff, a `/arch/{hash}/graph` route, and
`ocbench graph --format mermaid|dot`.

**Observed behaviour across runs** — an observed call graph ("`explore` called
3.2×/run, 41% of tokens") (#26) and a skill/MCP usage heatmap (#27), both built
on the per-agent metrics that now exist.

**Dashboard and reporting** — the Overview now carries a score/cost scatter and
a profile × suite matrix, and Runs carries the selected run's validators and
per-agent roll-up, which covers most of #28, #29 and #30. Still open: pass rate
over time, a skill/MCP usage heatmap, a task × profile (rather than suite)
matrix, an opt-in `--show-artifacts` view for raw material, and shareable
`ocbench report <experiment> --format md|html` (#31).

**Operations** — `ocbench suite list|export|add` and task scaffolding (#33);
parallel task execution with cost and token ceilings (#34); enforced network
isolation for tasks marked `network: none` (#35); `ocbench reprocess` to
re-derive metrics from stored events after a parser fix (#36); an OpenCode
version-drift warning in `doctor` (#37); JSONL/CSV export per run and experiment
(#38).

**Statistics follow-ups** — a suite-level scorecard with cost per solved task
(#12), baselines and a `history --by-profile` leaderboard (#14), and pinning
provider/model version and temperature where OpenCode exposes them (#13).

## Technical debt

Deferred during implementation, grouped by area. None blocks current use.

- **Suites and tasks** — every task now has an automated fail-before/pass-after proof; answer patterns are phrasing-sensitive; `task.Name`/`Tags` are excluded from the task hash; the embedded/on-disk parity test reads the source tree at `../../suites/core`; `//go:embed all:core` would also embed stray hidden files; `LoadFS` does not literally call `HashTasks`; a zero-task suite needs an empty `tasks/` directory; a missing `suite.yaml` error does not name the directory; `Source.Name` is the requested identifier rather than the loaded suite name; `Export` follows symlinks and can leave a partial tree; `ListSources` labels any filesystem as embedded; empty directories are not recreated on export; an empty YAML file reports `parse <file>: EOF`.
- **Runner** — `changeCounts` is sampled after validators while `changed`/`diff` are sampled before, so validator caches can inflate created/deleted counts; the drain-error path still persists no run row; `persist` issues several transactions rather than one; the near-deadline watchdog test is not actually near-deadline.
- **Store** — `ListRuns` orders by second-precision `started_at`, so runs in the same second fall back to UUID order.
- **Evaluation** — retry/compaction are dispatched on `part.type` regardless of envelope type; blank lines count as parse errors; an excerpt can split an ANSI escape sequence; hostile part shapes (null, array, wrong-typed fields) are handled but untested; the golden fixture's byte-identity to the live probe is asserted only by a report hash.
- **OpenCode adapter** — `Session.Wait` can pick the cancellation branch when a process exits cleanly in the same tick; `Kill` has a small PID-reuse window; the non-unix build-tag split is incomplete; temp-file removal on every path is unasserted; a directly constructed `Real` with a zero timeout gets a zero-deadline context.
- **Runner (further)** — an external signal that kills a validator is classified `error` rather than `failed`; the group-kill test is timing-sensitive; `BuildEnv` sorts full `KEY=VALUE` strings rather than keys; the allowlist test asserts membership rather than the exact set; ignored files are invisible to `ChangedFiles`; `git diff` is captured without `--binary`; fixture lock recovery and remote-clone locking are absent.
- **CLI** — an unknown suite exits `1` while an unknown task exits `2`; a cancelled run suppresses the partial report even though the rows were persisted; `experiment run --json` uses Go field names rather than snake_case.
- **Web** — no trace page yet, and the runs table renders every persisted run
  rather than paging; the dashboard has not been checked below ~1100px wide;
  `experiment list` issues one arm query per experiment.
- **Statistics** — the median-difference permutation is degenerate for a constant cost shift; "worst arm" ordering across incomparable pass-rate and cost p-values is untested.

## Known limitations

- **The tasks separate profiles only weakly.** The `standard` and `hard` tiers
  exist now, but 14 tasks is still few enough that a config change usually shows
  up as cost or time rather than as a quality difference.
- **`compare` is n=1.** One run against one run is mostly noise; use `experiment` with repeats for anything you intend to act on.
- **Network access is not enforced.** Tasks are trusted to be offline; the sandbox restricts the environment, not the network.
- **Subagent capture is proven for one level.** Grandchild sessions are unit-tested, not live-tested, and a `task` inside a child renders as a tool call rather than a nested span.
- **The OpenCode event format is not a stable API.** The parser and its golden fixture are pinned to OpenCode 1.18.32; a version change needs a re-probe.
- **`serve` is loopback-only and read-only by design.** There is no authentication, so it refuses to bind anywhere else.
- **The dashboard renders configuration, never run raw material.** Events, sessions, worktrees and untracked files stay on disk; the profile is shown in full, including instruction text and permission rules.
