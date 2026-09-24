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
| Task infrastructure — metadata, hidden tests, references, new validators | **specified, not started** | `531a303` (spec only) |
| Agentic suite — delegation, planning, context, skill use, restraint | **designed, not planned** | design §14 |
| Difficulty tiers — standard/hard suites, multi-language fixtures | **not started** | — |

## In flight: task infrastructure

Planned in [plans/2026-09-24-ocbench-task-infrastructure.md](plans/2026-09-24-ocbench-task-infrastructure.md),
seven tasks, none implemented:

1. **Task metadata** — `difficulty`, `capabilities`, `expected_tokens`, suite `tier`, with hashing rules.
2. **Hidden tests** — `evaluator/tests/` copied into the worktree only after the agent stops.
3. **Reference solutions** — `evaluator/reference/` plus a suite test proving every task fails untouched and passes with its reference.
4. **`diff` and `grep` validators** — required/forbidden paths, a changed-line ceiling, present/absent patterns.
5. **`process` validator, weights and `score`** — check that a stated behaviour happened (for example that the tests were run) and grade partial credit without changing `success`.
6. **Process metrics** — time to first edit, tool calls before it, redundant reads, verification commands.
7. **Verification** — gates plus a live smoke on `py-bugfix`.

The minimum needed before the agentic suite is tasks 1, 4 and 5: metadata for
grouping, and the validators that make delegation, restraint and
instruction-following checkable at all.

## Next: the agentic suite

Design settled in [design.md §14](design.md); a plan still has to be written.
The suite (`suites/agentic/`) targets the behaviours that separate one profile
from another, using the capability vocabulary `delegation, planning, tool-use,
context, skill-use, instruction-following, restraint, debugging, multi-file,
search`:

| Task | What it exercises |
|---|---|
| `parallel-investigation` | Three independent areas in a fixture too large to read serially; the prompt asks for delegation, so a `process` validator may check it |
| `plan-then-implement` | An ordered change (schema → loader → caller) where the plan is an artifact and the tests must pass afterwards |
| `buried-fact` | A 30+ file fixture with one fact and deliberate near-misses; measures search cost |
| `skill-application` | Knowledge that lives in a skill rather than the fixture; `skill_loads` shows whether it was consulted |
| `project-rules` | A fixture shipping `AGENTS.md` rules the prompt restates; `grep`/`diff` validators check compliance |
| `tempting-shortcut` | A failing test that could be "fixed" by editing the test; `diff` forbids the shortcut |
| `multi-module-feature` | A feature spanning modules with a shared interface |

Honesty rules from §14.3 apply: a process validator may only check something the
prompt asks for, and a task must be solvable without the capability it names.

## Then: difficulty tiers and multi-language fixtures

Slice B's remaining half. The current five tasks are all small stdlib Python and
an inexpensive model passes them in ten seconds, so every profile passes
everything and only cost and time separate them. To fix that:

- **Tiers** — `core` stays as the smoke suite; add `standard` and `hard` suites whose tasks a strong model fails 30–70% of the time.
- **Realistic fixtures** — medium repositories (20–50 files, several modules, misleading clues), bugs that need a reproduction first, cross-file refactors, dependency and API migrations, flaky-test diagnosis, and performance fixes with a timing validator.
- **More tooling** — Go, TypeScript/Node, shell and SQL fixtures, not only Python.
- **Capability coverage** — tasks where a specific skill or MCP server *should* help, so adding one shows up as a measurable difference.

## Unscheduled review items

Numbering is from the original external review, kept so nothing is lost.

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

- **Suites and tasks** — only `config-yaml-fix` has an automated fail-before/pass-after proof (the other four were proven by hand when authored); answer patterns are phrasing-sensitive; `task.Name`/`Tags` are excluded from the task hash; the embedded/on-disk parity test reads the source tree at `../../suites/core`; `//go:embed all:core` would also embed stray hidden files; `LoadFS` does not literally call `HashTasks`; a zero-task suite needs an empty `tasks/` directory; a missing `suite.yaml` error does not name the directory; `Source.Name` is the requested identifier rather than the loaded suite name; `Export` follows symlinks and can leave a partial tree; `ListSources` labels any filesystem as embedded; empty directories are not recreated on export; an empty YAML file reports `parse <file>: EOF`.
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
