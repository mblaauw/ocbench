# ocbench

A local, single-binary benchmark harness for a resolved OpenCode setup. It
answers one question: **did this change to my agents, skills, MCP servers or
permissions actually make things better?**

It fingerprints your resolved OpenCode profile, runs fixed tasks in throwaway
git worktrees under a restricted environment, records the raw event stream,
checks results with deterministic validators, and stores everything in SQLite.
Then it lets you compare runs, run A/B experiments over config overlays, and see
what each agent and subagent actually did.

## Quickstart

```sh
make build                 # -> bin/ocbench
bin/ocbench doctor         # is the environment healthy?
bin/ocbench snapshot       # fingerprint the current profile
bin/ocbench run core repo-investigation --model <provider/model> --variant low
bin/ocbench history        # what has run
bin/ocbench compare latest previous
```

Requirements: Go 1.26 to build, `git`, and an OpenCode CLI on `PATH` (or
`opencode_bin` in `~/.config/ocbench/config.yaml`). The binary is CGO-free and
cross-compiles with `make cross`.

## Commands

| Command | What it does |
|---|---|
| `ocbench version` | Build identity |
| `ocbench doctor [--json]` | Checks OpenCode, git, the database, config, profile discovery, agents, skills, MCP, sandbox |
| `ocbench snapshot [--json] [--agent A] [--model M] [--variant V] [--dir D]` | Resolves and persists the execution profile, printing component changes |
| `ocbench run <suite> [task...] [--repeat N] [--dry-run] [--suite-dir P] [--json]` | Runs tasks in disposable worktrees and persists runs, metrics and validations |
| `ocbench experiment run <suite> [task...] --profile A=<path> --profile B=<path> [--repeat N] [--baseline A] [--exit-on-regression]` | Interleaved A/B over config overlays, with statistics and a regression gate |
| `ocbench experiment list\|show <id> [--format jsonl]` | Experiment summaries, and a versioned per-run export |
| `ocbench history [--task T] [--limit N] [--json]` | Persisted runs, newest first |
| `ocbench compare <a> <b> [--json]` | One run against another: metric deltas, validation changes, profile component changes |
| `ocbench trace <run-id> [--json]` | A run as a timeline: steps, tool calls, and subagent spans nested under the `task` call that produced them |
| `ocbench serve [--listen 127.0.0.1:8787]` | Read-only dashboard over the same data (loopback only) |

Exit codes: `0` completed, `1` infrastructure error, `2` usage/config error,
`3` the opted-in-failure family (`--exit-on-task-failure`,
`--exit-on-regression`).

## What it captures

- **The profile**: OpenCode version, agents (model, variant, tools, permissions,
  prompt), skills, MCP servers, plugins, instruction files, and everything else
  in the resolved config, canonicalised and hashed. Secrets are redacted; MCP
  environment values are recorded as names only.
- **The run**: raw JSONL events, `stderr`, the exported session, delegated child
  sessions, the diff against the baseline commit, changed and untracked files,
  validator output, and normalised metrics (tokens, cost, steps, tool calls,
  files and diff lines, per-agent roll-ups, process metrics).
- **What it does not do**: it never writes to your OpenCode data directory, and
  a benchmark run cannot see your shell environment beyond a small allowlist.

## How a run works

```
suite + task  →  fixture materialised to a deterministic git commit
              →  disposable worktree at that commit
              →  sandboxed environment (allowlist)
              →  opencode run --format json --dir <worktree>
              →  session export + delegated child sessions
              →  diff against the baseline commit
              →  validators (command / answer / diff / grep / process)
              →  metrics + result.json + SQLite rows
```

Fixtures are content-addressed: the same fixture produces the same baseline
commit SHA on any machine, so runs are comparable across time.

## Repository layout

```
cmd/ocbench          entry point
internal/canon       canonical JSON, hashing, redaction, path normalisation
internal/cli         command surface
internal/config      XDG paths and config
internal/doctor      environment checks
internal/evaluation  event parsing, metrics, validators
internal/experiment  arms, overlays, orchestration, aggregation
internal/history     shared read model for history and comparison
internal/opencode    CLI adapter, sessions, process-group handling
internal/profile     discovery, fingerprinting, persistence, diffs
internal/runner      fixtures, worktrees, pipeline, artifacts, persistence
internal/session     OpenCode session export parsing
internal/stats       Wilson intervals, permutation tests, medians
internal/store       SQLite schema, migrations, queries
internal/suite       suite/task loading, hashing, resolution, export
internal/trace       run timelines
internal/web         dashboard handlers
suites/core          the embedded smoke suite
web/                 embedded templates and styles
docs/                design, plans, evidence, roadmap, decisions
```

## Status

Implemented: profile fingerprinting, the run pipeline, the core suite, history
and comparison, experiments with statistics, subagent capture, the trace view,
and the read-only dashboard.

Outstanding work — harder tasks, an agentic suite, task metadata, hidden tests
and new validator kinds — is tracked in [docs/roadmap.md](docs/roadmap.md), and
every design decision with its trade-off is recorded in
[docs/decisions.md](docs/decisions.md).

Dependencies are deliberately minimal (Cobra, YAML, a pure-Go SQLite) and
vendored; there is no JavaScript build step and no CDN asset.
