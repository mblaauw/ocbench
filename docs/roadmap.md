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
| Experiments — arms, overlays, statistics, regression gate, JSONL export | **done** | `2db8b81`..`a29748a` |
| Subagents — session parsing, child capture, per-agent metrics, `trace` | **done** | `840bbc9`..`f08645f` |
| Task infrastructure — metadata, hidden tests, references, new validators | **done** | `acf0f83`..`6472c50` |
| Agentic suite — delegation, restraint, project rules, buried fact | **done** | `fb2f025`, evidence `2026-09-24` |
| Difficulty tiers — standard/hard suites, multi-language fixtures | **not started** | — |

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

## Next: difficulty tiers and multi-language fixtures

Slice B's remaining half. The current five tasks are all small stdlib Python and
an inexpensive model passes them in ten seconds, so every profile passes
everything and only cost and time separate them. To fix that:

- **Tiers** — `core` stays as the smoke suite; add `standard` and `hard` suites whose tasks a strong model fails 30–70% of the time.
- **Realistic fixtures** — medium repositories (20–50 files, several modules, misleading clues), bugs that need a reproduction first, cross-file refactors, dependency and API migrations, flaky-test diagnosis, and performance fixes with a timing validator.
- **More tooling** — Go, TypeScript/Node, shell and SQL fixtures, not only Python.
- **Capability coverage** — tasks where a specific skill or MCP server *should* help, so adding one shows up as a measurable difference.

## Unscheduled review items

Numbering is from the original external review, kept so nothing is lost.

**Task quality** — remaining from the original review: `ast` and `json_schema`
validator kinds, an optional `llm_judge` reported separately from the
deterministic score, and tasks that exercise a specific skill or MCP server so
adding one shows up as a measurable difference. Hidden tests, reference
solutions, task metadata and the `diff`/`grep`/`process` validators are done.

**Capture depth** — split the catch-all `config` profile component into
`command/<name>`, `lsp/<lang>`, `formatter/<name>`, `provider/<id>`,
`mode/<name>`, `compaction`, `share`, `autoupdate`, `instructions` globs and
`tools` toggles (#16); capture the full agent definition including prompt text
(#17); render component diffs as meaning rather than hashes (#18); record each
MCP server's tool list and schema hash (#19); estimate the context budget a
profile spends before any work starts (#20); add `profile lint` for dangling
skill paths, unreachable subagents and conflicting permissions (#21).

**Architecture graph** — a static graph of primary agents, the subagents they
may call, and the skills and MCP servers attached to each (#22), plus a graph
diff between two profiles (#23), served at `/profiles/{hash}/graph` and as
`ocbench graph --format mermaid|dot`.

**Observed behaviour across runs** — an observed call graph ("`explore` called
3.2×/run, 41% of tokens") (#26) and a skill/MCP usage heatmap (#27), both built
on the per-agent metrics that now exist.

**Dashboard and reporting** — inline SVG charts (pass rate over time, cost
against success, task × profile heatmap) (#28), a task × profile matrix (#29), a
richer run detail page with an opt-in `--show-artifacts` view (#30), and
shareable `ocbench report <experiment> --format md|html` (#31).

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
- **Web** — no charts or trace page yet; `experiment list` issues one arm query per experiment.
- **Statistics** — the median-difference permutation is degenerate for a constant cost shift; "worst arm" ordering across incomparable pass-rate and cost p-values is untested.

## Known limitations

- **The tasks are too easy to separate profiles.** Until the standard and hard tiers exist, a config change can only show up as cost or time, never as quality.
- **`compare` is n=1.** One run against one run is mostly noise; use `experiment` with repeats for anything you intend to act on.
- **Network access is not enforced.** Tasks are trusted to be offline; the sandbox restricts the environment, not the network.
- **Subagent capture is proven for one level.** Grandchild sessions are unit-tested, not live-tested, and a `task` inside a child renders as a tool call rather than a nested span.
- **The OpenCode event format is not a stable API.** The parser and its golden fixture are pinned to OpenCode 1.18.32; a version change needs a re-probe.
- **`serve` is loopback-only and read-only by design.** There is no authentication, so it refuses to bind anywhere else.
