# ocbench Design Specification

Status: approved for implementation
Date: 2026-09-22
Module: `mbl/ocbench`
Repo: `/Users/mich/dev/mbl-ocbench` (local git only, no remote, no CI)

## 1. Purpose

`ocbench` is a local benchmark appliance for OpenCode. It answers one question:
"did this change to my OpenCode configuration actually make it better?"

It runs immutable benchmark suites against the *resolved execution profile* of
the local OpenCode installation, in disposable git worktrees, captures the raw
event stream, runs deterministic validators, stores everything in a local
SQLite database, and compares results between profiles and runs.

Non-goals for v0.1: central server, multi-user accounts, PostgreSQL, Grafana,
Helm chart, Kubernetes operator, Prometheus scraping. Deployment is a single
statically linked Linux binary copied into an existing Workbench pod. Workbench
home-directory persistence makes history survive restarts; `OCBENCH_HOME`
allows redirecting state to a persistent workspace path.

## 2. Verified environment facts (2026-09-22)

These were established by direct inspection and are load-bearing for the design:

- OpenCode 1.18.32 is installed at `~/.opencode/bin/opencode`.
- `opencode run` supports `--agent`, `--model`, `--variant`, `--format json`,
  `--dir`, `--auto`, `--pure`, `--print-logs`, `--log-level`, `-c/--session`.
- `opencode debug config` prints the fully resolved configuration as JSON,
  including custom agent definitions *with prompt text*, permissions, MCP
  server configs, `skills.paths`, `plugin`, `plugin_origins`, `command`.
- `opencode debug skill` prints a JSON array of every discovered skill with
  `name`, `description`, `location` and the full SKILL.md `content`.
- `opencode debug agent <name>` prints resolved per-agent detail including
  `mode`, `native`, `tools`, `options`, `permission`.
- `opencode agent list` lists agents with their mode.
- `opencode mcp list` lists MCP servers with enabled/disabled status and
  command/URL. It may attempt connections; only `doctor` may call it.
- `opencode export <sessionID>` prints the full session JSON:
  `{info:{...,tokens:{input,output,reasoning,cache:{read,write}},cost,model,agent,version,...},messages:[{info,parts}]}`.
- **`opencode run --format json` emits a JSONL stream, one object per line, with
  the envelope `{"type": "<snake_case>", "timestamp": <unix-ms>,
  "sessionID": "ses_…", "part": {…}}`** (observed live on 1.18.32; this differs
  from the server API's `{type, properties}` union below). Observed `type`
  values: `step_start`, `tool_use`, `step_finish`, `text`; `part.type` uses the
  SDK's kebab-case names (`step-start`, `tool`, `step-finish`, `text`).
  `step_finish.part` carries `reason`, `tokens{total,input,output,reasoning,cache{read,write}}`
  and `cost`; `tool_use.part` carries `tool`, `callID`, `state{status,input,output,error}`.
  There is **no idle/terminal event**: run completion is signalled by process
  exit, and the session ID is read from any event's `sessionID`.
  Parsers must tolerate unknown `type`/`part.type` values and skip malformed
  lines without aborting the run.
- Event stream schema of the server API (from the locally installed
  `@opencode-ai/sdk` types): each event is `{"type": "...", "properties": {...}}`; the `Event` union is
  `server.instance.disposed`, `installation.updated`,
  `installation.update-available`, `lsp.client.diagnostics`, `lsp.updated`,
  `message.updated`, `message.removed`, `message.part.updated`,
  `message.part.removed`, `permission.updated`, `permission.replied`,
  `session.status`, `session.idle`, `session.compacted`, `file.edited`,
  `todo.updated`, `command.executed`, `session.created`, `session.updated`,
  `session.deleted`, `session.diff`, `session.error`, `file.watcher.updated`,
  `vcs.branch.updated`, `tui.prompt.append`, `tui.command.execute`,
  `tui.toast.show`, `pty.created`, `pty.updated`, `pty.exited`,
  `pty.deleted`, `server.connected`.
- Part union: `text`, `subtask` (has `prompt`,`description`,`agent`),
  `reasoning`, `file`, `tool` (`callID`,`tool`,`state`), `step-start`,
  `step-finish` (tokens+cost), `snapshot`, `patch` (hash+files), `agent`
  (subagent name), `retry` (attempt+error), `compaction` (auto).
- `ToolState`: `pending|running|completed|error`; completed carries
  `input`, `output`, `title`, `time.start/end`; error carries `error`.
- `AssistantMessage` carries `tokens{input,output,reasoning,cache{read,write}}`,
  `cost`, `finish`, `error`, `modelID`, `providerID`, `mode`, `path{cwd,root}`.
- MCP tools are exposed to the model as `<server>_<tool>` tool names.
- `timeout(1)` is not available on macOS; the Go runner must implement its own
  process-group kill logic.

## 3. Architecture

```
ocbench (single Go binary)
├── cmd/ocbench            CLI entry (cobra)
├── internal/version       build metadata
├── internal/config        XDG paths, config.yaml, OCBENCH_HOME
├── internal/store         SQLite (modernc.org/sqlite), embedded migrations
├── internal/profile       discovery, canonicalisation, redaction, hashing, diff
├── internal/opencode      Adapter interface + real CLI adapter + test helper
├── internal/suite         suite/task schema, loader, embedded core suite
├── internal/runner        fixtures, worktrees, sandbox, pipeline, artifacts
├── internal/evaluation    event normalisation, metrics, validators
├── internal/web           embedded UI (serve)
├── suites/core            embedded benchmark suite
└── web/                   HTML templates + static assets (embedded)
```

Dependency policy: stdlib first; `spf13/cobra` (CLI), `modernc.org/sqlite`
(CGO-free SQLite), `gopkg.in/yaml.v3` (config + suite definitions). All
dependencies vendored. No JS build step, no CDN assets. Tests use only stdlib
`testing`.

### Runtime paths

```
$XDG_CONFIG_HOME/ocbench/config.yaml        (default ~/.config/ocbench)
$OCBENCH_HOME/ocbench.db                    (default $XDG_DATA_HOME/ocbench,
$OCBENCH_HOME/profiles/<profile-hash>/      raw adapter captures + snapshot.json
$OCBENCH_HOME/runs/<run-uuid>/              per-run artifacts
$OCBENCH_HOME/suites/<name>/                external suites (override embedded)
$XDG_CACHE_HOME/ocbench/fixtures/<hash>/    deterministic baseline git repos
$XDG_CACHE_HOME/ocbench/worktrees/          temporary run worktrees
```

`OCBENCH_HOME` overrides only the data directory.

## 4. OpenCode adapter

The runner never reads OpenCode's internal database. All observation goes
through the CLI. The adapter is an interface so tests and CI never touch
inference.

```go
package opencode

type Adapter interface {
    Version(ctx context.Context) (string, error)
    ResolvedConfig(ctx context.Context, dir string) ([]byte, error)
    Skills(ctx context.Context, dir string) ([]SkillInfo, error)
    Agent(ctx context.Context, dir, name string) (AgentInfo, error)
    MCPStatus(ctx context.Context, dir string) ([]MCPStatus, error) // doctor only
}

// Plan 2 adds: Start(ctx, RunRequest) (Session, error); Export(ctx, sessionID) ([]byte, error)
```

`SkillInfo{Name, Description, Location, Content string}`.
`AgentInfo{Name, Mode, Native, Tools []string, Options, Permission json.RawMessage, ...}`.
Wait: the concrete JSON shapes are defined by OpenCode; the adapter returns
already-decoded, minimally-typed structs plus the raw JSON when lossless
retention is needed.

`RealAdapter` executes the configured binary (`opencode_bin`, default
`opencode` from PATH, overridable via `OCBENCH_OPENCODE_BIN`) with a fixed
environment and a per-call timeout (default 60s). Stdout/stderr are captured
fully; non-zero exit codes become errors carrying stderr excerpts.

Tests and `--dry-run` never invoke the real binary: `helper_test.go` uses the
`GO_WANT_HELPER_PROCESS` pattern to make the test binary impersonate
`opencode`, serving canned JSON per subcommand.

## 5. The resolved execution profile

A profile is an immutable, content-addressed record of everything that can
plausibly change agent behaviour.

### 5.1 Canonical structure

```json
{
  "schema": 1,
  "opencode_version": "1.18.32",
  "components": {
    "primary":       { "default_agent": "...", "model": "...", "small_model": "..." },
    "agent/<name>":  { "mode": "...", "model": "...", "variant": "...", "temperature": 0.1,
                       "steps": 20, "tools": {...}, "options": {...}, "native": false,
                       "prompt_sha256": "..." },
    "skill/<name>":  { "description": "...", "content_sha256": "...",
                       "files_sha256": "...", "source": "~/.agents/skills/x" },
    "mcp/<name>":    { "type": "local|remote", "enabled": true, "command": "...",
                       "url": "...", "environment_keys": ["FOO"] },
    "plugin/<spec>": { "origin": "...", "scope": "global", "local_sha256": "..." },
    "instructions/<scope>": { "path": "~/.../AGENTS.md", "sha256": "..." },
    "permissions":   { "global": {...}, "by_agent": {...} },
    "config":        { ...resolved config minus componentised keys... },
    "environment":   { "sandbox": "default|inherit", "env_names": ["HOME","PATH",...],
                       "auto": true, "pure": false,
                       "overrides": {"agent": null, "model": null, "variant": null} }
  }
}
```

Component boundaries are disjoint by construction. Keys consumed by dedicated
components (`model`, `small_model`, `agent`, `mcp`, `plugin`, `plugin_origins`,
`skills`, `permission`, `username`, `$schema`) are removed from `config`, so a
single change never shows up as an unexplained duplicate diff. Unknown/future
resolved-config keys stay in `config`, which is the catch-all safety net.

### 5.2 Hashing

- `canonical_json(x)`: JSON with object keys sorted lexicographically, no
  insignificant whitespace, deterministic number formatting, UTF-8.
- `profile_hash = SHA256(canonical_json(whole structure))`.
- Per-component hash: `SHA256(canonical_json(component subtree))`, keyed by
  `(kind, name)` for named components or `(kind, kind)` for singletons.
- Identical canonical profiles MUST resolve to the identical `profile_hash`.
  Re-snapshotting an unchanged environment never creates a second profile row
  (`profiles.profile_hash` is UNIQUE).

### 5.3 Normalisation (applied before hashing and before persistence)

1. Redaction: any object key matching
   `(?i)(api[_-]?key|token|secret|password|passwd|credential|authorization|cookie)`
   is replaced by `"<redacted>"` recursively. Redaction happens before
   hashing, so rotating a secret does not change the profile hash.
2. Path normalisation: the `$HOME` prefix becomes `~`; run directory and
   worktree paths become `<run-dir>` and `<worktree>`. This makes a config
   that is identical on two machines hash identically.
3. Skill identity is `(name, content)`, never location. Locations are stored
   for humans but excluded from hashes after normalisation.
4. Env: only variable *names* are recorded, never values.
5. Volatile/noise keys are not part of any component: `time`, `cost`, and
   anything derived at runtime.

### 5.4 Skill hashing

Skills are discovered through `opencode debug skill` (authoritative, respects
global/project/plugin/`skills.paths` discovery). Identity is the skill name.
Contents: `content_sha256` over the SKILL.md content from the debug output,
plus `files_sha256` over every regular file under the skill's directory
(relative path + bytes, sorted), so `references/` and `scripts/` changes count.
Raw skill content is stored once per profile under `profiles/<hash>/skills.json`
and never duplicated into the database.

### 5.5 Instructions

Discovered as: global `~/.config/opencode/AGENTS.md`, then `AGENTS.md` at every
ancestor of the run directory (fixture worktrees contain none by default, so in
practice only global instructions apply to benchmark runs). Content hashed; raw
text stored with the profile capture.

### 5.6 Profile persistence

- `profiles` row: `id` (uuid), unique `profile_hash`, `canonical_json`
  (redacted), `opencode_version`, `ocbench_version`, `created_at`.
- `profile_components` rows: `(profile_id, kind, name, hash, canonical_json)`.
- Raw captures (`resolved-config.json`, `skills.json`, per-agent JSON) written
  to `$OCBENCH_HOME/profiles/<profile-hash>/`.
- A changed profile automatically creates (or reuses, if seen before) a new
  immutable profile row and records `profile_changes` rows against the
  comparison profile.

## 6. SQLite schema v1

```sql
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE profiles (
  id TEXT PRIMARY KEY,
  profile_hash TEXT NOT NULL UNIQUE,
  opencode_version TEXT NOT NULL,
  ocbench_version TEXT NOT NULL,
  canonical_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE profile_components (
  profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  hash TEXT NOT NULL,
  canonical_json TEXT NOT NULL,
  PRIMARY KEY (profile_id, kind, name)
);

CREATE TABLE suites (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  hash TEXT NOT NULL,
  source TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE (name, version, hash)
);

CREATE TABLE tasks (
  suite_id TEXT NOT NULL REFERENCES suites(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL,
  version TEXT NOT NULL,
  name TEXT NOT NULL,
  tags_json TEXT NOT NULL,
  timeout_seconds INTEGER NOT NULL,
  fixture_sha TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  PRIMARY KEY (suite_id, task_id, version)
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  experiment_id TEXT REFERENCES experiments(id),
  arm_id TEXT REFERENCES experiment_arms(id),
  repeat_index INTEGER NOT NULL DEFAULT 0,
  profile_id TEXT NOT NULL REFERENCES profiles(id),
  profile_hash TEXT NOT NULL,
  suite_id TEXT REFERENCES suites(id),
  suite_name TEXT NOT NULL,
  suite_version TEXT NOT NULL,
  suite_hash TEXT NOT NULL,
  task_id TEXT NOT NULL,
  task_version TEXT NOT NULL,
  fixture_sha TEXT NOT NULL,
  opencode_version TEXT NOT NULL,
  ocbench_version TEXT NOT NULL,
  model TEXT, agent TEXT, variant TEXT,
  status TEXT NOT NULL,
  dry_run INTEGER NOT NULL DEFAULT 0,
  exit_code INTEGER,
  session_id TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  duration_ms INTEGER,
  artifacts_dir TEXT NOT NULL,
  error TEXT
);

CREATE TABLE run_metrics (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value_num REAL,
  value_text TEXT,
  unit TEXT,
  PRIMARY KEY (run_id, name)
);

CREATE TABLE run_validations (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  command TEXT,
  status TEXT NOT NULL,
  exit_code INTEGER,
  duration_ms INTEGER,
  output_path TEXT,
  output_excerpt TEXT,
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE profile_changes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  from_profile_id TEXT NOT NULL REFERENCES profiles(id),
  to_profile_id TEXT NOT NULL REFERENCES profiles(id),
  component_kind TEXT NOT NULL,
  component_name TEXT NOT NULL,
  change TEXT NOT NULL,
  from_hash TEXT, to_hash TEXT,
  from_summary TEXT, to_summary TEXT,
  detected_at TEXT NOT NULL
);

CREATE TABLE experiments (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE experiment_arms (
  id TEXT PRIMARY KEY,
  experiment_id TEXT NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  profile_id TEXT REFERENCES profiles(id),
  profile_hash TEXT NOT NULL,
  overlay_kind TEXT NOT NULL,          -- file | dir | content | none
  overlay_path TEXT,                   -- absolute path for file/dir overlays
  overlay_sha256 TEXT,                 -- hash of the overlay bytes (file/dir tree)
  created_at TEXT NOT NULL,
  UNIQUE (experiment_id, label)
);
```

Schema v1 plus the v2 experiment tables. Migrations are append-only: the
`experiment_arms` table and `runs.arm_id` arrive in `0002_experiments.sql`,
so v1 databases upgrade in place without rewriting existing rows.

Migrations are embedded `.sql` files applied in order inside a transaction,
tracked by `schema_migrations`. Every `run` carries UUIDs and canonical hashes
so a future `ocbench-hub` can ingest sanitised rows without a client rewrite.

## 7. Benchmark suites and tasks

Suite layout:

```
suites/core/
  suite.yaml
  tasks/<task-id>/
    task.yaml
    prompt.md
    fixture/            materialised into the worktree (only this)
    evaluator/          hidden; never materialised before the agent stops
      tests/            copied into the worktree after the agent stops, then run
      reference/        reference solution tree, never materialised
      answer.json       answer patterns
```

`suite.yaml`: `name`, `version`, `description`, optional `defaults.timeout`,
optional `tier` (`smoke`, `standard` or `hard`).

`task.yaml`:

```yaml
id: py-bugfix-017
version: 1
name: Fix retry behaviour
tags: [python, debugging]
difficulty: medium            # easy | medium | hard
capabilities: [debugging]     # what the task exercises, see §14
expected_tokens: 40000        # optional; used to normalise cost per solved task
timeout: 300
requires: [python3]
allow_changes:
  - "src/**"
  - "tests/**"
validators:
  - kind: command
    name: unit tests
    command: ["python3", "-m", "unittest", "discover", "-s", "tests"]
    weight: 2                 # optional; defaults to 1
  - kind: answer
    name: root cause stated
    patterns: ["retry", "off-by-one"]
    # patterns may live in evaluator/answer.json instead
  - kind: diff
    name: touched only the parser
    required_paths: ["src/**"]
    forbidden_paths: ["tests/**"]
    max_lines: 60
  - kind: grep
    name: no leftover debug output
    absent: ["print\\(", "TODO"]
  - kind: process
    name: ran the tests before finishing
    tool_pattern: "(unittest|pytest|go test|npm test)"
```

Rules:
- Self-contained fixtures are the default. Each fixture is materialised into a
  cache repo under `$XDG_CACHE_HOME/ocbench/fixtures/<fixture-hash>/`, committed
  once with fixed author/committer identity and fixed dates so the baseline
  commit SHA is deterministic across machines and invocations.
- Everything normalises to: source → immutable commit SHA → temporary
  worktree. External local repos and (later) remote repos use the same path.
- Validators run with the sandboxed environment; a validator whose `requires`
  are unavailable yields `skipped`, never `failed`.
- Validator output is captured to `runs/<id>/validation/<seq>-<name>.log` with
  an excerpt in the DB.
- `allow_changes` globs define expected file changes; changed paths outside the
  globs feed the `files_unexpected` metric. Changes are computed against the
  baseline commit SHA (survives agents that commit).
- Hidden evaluator data lives in `evaluator/` and is passed to validators out
  of band; it is never written into the agent worktree, with one deliberate
  exception: `evaluator/tests/` is copied into the worktree **after the agent
  has stopped**, immediately before validators run, so a task can be graded by
  tests the agent never saw. The copy happens after the changed-file set and
  `diff.patch` are computed, so hidden tests never appear as agent changes.
- A task's expected behaviour is proven by an automated fail-before /
  pass-after check: `evaluator/reference/` holds a reference solution tree that
  is copied over a scratch copy of the fixture. Validators must fail on the
  untouched fixture and pass with the reference applied. For answer tasks the
  reference is `evaluator/reference/answer.txt`, which the answer validator must
  accept. A suite test runs this for every task, so an ill-posed task cannot
  ship.
- Validator kinds: `command` (argv, exit status), `answer` (patterns over the
  final text event), `diff` (required/forbidden paths and a changed-line
  ceiling), `grep` (required/absent patterns in the worktree after the run) and
  `process` (a tool call matching a pattern occurred before the run ended).
  Each validator may carry `weight` (default 1) for partial credit.
- `success` stays binary — every validator passed — while `score` is the
  weighted fraction of validators that passed. Tasks are grouped for reporting
  by `difficulty` and by `capabilities`, so a profile can be compared on
  "delegation tasks" or "hard tasks" without a schema change.

Threat model (documented, not oversold): the sandbox prevents accidental
credential leakage and cross-contamination between benchmark runs. It is not
adversarial containment against a hostile model; fixtures and tasks are
trusted content.

## 8. Runner pipeline

```
run request
  → load + hash suite/task (suite hash, task version)
  → materialise fixture → baseline SHA → git worktree
  → build sandboxed env (allowlist) 
  → opencode run --format json --dir <worktree> --auto [--agent/--model/--variant]
      stdout → events.jsonl (line-buffered, streamed)
      timeout → process-group SIGTERM then SIGKILL
  → opencode export <sessionID> → session.json (cross-check)
  → discover delegated child sessions from task tool metadata, export each
      recursively → sessions/<child-id>.json, roll up per-agent usage
  → git status/diff versus baseline SHA → diff.patch, changed-files metrics
  → validators with sandboxed env → validation results
  → normalise events → metrics, aggregate result.json
  → persist run + metrics + validations; keep worktree only with --keep-worktree
```

Environment sandbox: allowlist by default. Always kept: `HOME`, `PATH`,
`LANG`/`LC_*`, `TZ`, `TERM`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `TEMP`,
`TMP`, plus any `LC_*`. Always set: `GIT_TERMINAL_PROMPT=0`, `GIT_PAGER=cat`,
`PAGER=cat`, `NO_COLOR=1`, `OCBENCH=1`. Dropped: everything else, notably
`KUBECONFIG`, `AWS_*`, `AZURE_*`, `GOOGLE_*`, `GITLAB_TOKEN`,
`SSH_AUTH_SOCK`. Additional names may be forwarded via
`config.sandbox.pass_env`. `GIT_TERMINAL_PROMPT=0` always set.
`--inherit-environment` switches to a deny-list mode and is explicit opt-in.
The same env policy is used for validators.

`--dry-run` performs `doctor`-level discovery, suite loading, fixture
materialisation and worktree creation, prints the resolved plan (profile hash,
tasks, validators, env names), and exits without invoking a model.

Exit codes: `0` pipeline completed (regardless of task pass/fail), `1`
infrastructure error, `2` usage/config error, `3` the opted-in-failure family:
`--exit-on-task-failure` (a task validator failed) and
`--exit-on-regression` (an experiment measured a regression against its
baseline arm). Both are explicit opt-ins; neither fires by default.

## 9. Metrics

Per run, normalised from the raw event stream (and cross-checked against
`export`):

```
tokens_input, tokens_output, tokens_reasoning,
tokens_cache_read, tokens_cache_write, cost
agent_turns, steps
tool_calls_total, tool_calls_failed, tool_calls_<tool>
subagent_calls, skill_loads, mcp_calls, mcp_calls_<server>, retries, compactions
files_changed, files_created, files_deleted,
diff_lines_added, diff_lines_removed, files_unexpected
duration_ms, validator_failures, success, first_shot_success, score
```

Process metrics, derived from the same event stream and the captured sessions:

```
time_to_first_edit_ms, tool_calls_before_first_edit, redundant_reads,
verification_commands, subagent_calls, subagent_tokens_total
```

`score` is the weighted fraction of validators that passed (`success` stays
binary). `redundant_reads` counts read tool calls for a path already read in
the same run; `verification_commands` counts tool calls whose command matches a
test or lint pattern; `time_to_first_edit_ms` is measured from run start to the
first edit or write tool call. These describe how an agent worked, not just
what it produced, which is what makes an agentic profile comparable.

Raw JSONL events are canonical and retained per run; normalisation is
re-runnable so a parser fix can reprocess historical runs without schema
changes.

Metrics are either run-scoped or agent-scoped. Agent-scoped metric names use
the reserved form `agent.<agent-name>.<metric>` so per-agent roll-ups (tokens,
cost, tool calls) can be added without a schema change; a plain name is always
run-scoped. Experiment-level statistics are derived at read time and never
stored, so a statistics fix re-reads history without a migration.

Per-agent roll-ups come from the exported session files (see §13) and use:

```
agent.<name>.messages, agent.<name>.cost
agent.<name>.tokens_input, agent.<name>.tokens_output,
agent.<name>.tokens_reasoning, agent.<name>.tokens_cache_read,
agent.<name>.tokens_cache_write, agent.<name>.tokens_total
agent.<name>.tool_calls, agent.<name>.tool_calls_failed
subagent_sessions, subagent_tokens_total, subagent_cost,
subagent_export_failures
session_crosscheck_tokens_delta
```

`subagent_*` count only delegated child sessions; the primary agent's own
session is covered by the existing run-scoped metrics. `session_crosscheck_tokens_delta`
is the absolute difference between event-derived `tokens_total` and the primary
session export's own total, so the cross-check the pipeline promises is a number
rather than an assumption.

## 10. Commands (v0.1)

```
ocbench version
ocbench doctor [--json]
ocbench snapshot [--json] [--agent A] [--model M] [--variant V] [--dir D]
ocbench run <suite> [task] [--repeat N] [--dry-run] [--suite-dir P]
                     [--agent A] [--model M] [--variant V]
                     [--inherit-environment] [--keep-worktree] [--json]
ocbench history [--task T] [--limit N] [--json]
ocbench trace <run-id> [--json]         # per-step timeline with subagent spans
ocbench compare <a> <b> [--json]        # ids, hashes, "latest", "previous"
ocbench experiment run [suite] [task...] --profile A=<path> --profile B=<path>
                       [--repeat N] [--baseline A] [--exit-on-regression] [--json]
ocbench experiment list [--json]
ocbench experiment show <id> [--json|--format jsonl]
ocbench suite list|export|add
ocbench serve [--listen 127.0.0.1:8787]
```

`compare` reports metric deltas and, when profiles differ, the exact
component changes. If more than one benchmark-relevant variable changed it
states the count and recommends a controlled run; it never claims causation.

## 11. Verification strategy

- Unit tests with the `GO_WANT_HELPER_PROCESS` fake adapter; no inference in
  tests.
- Golden-file tests for canonicalisation, redaction, event normalisation and
  profile diffs.
- Determinism test: materialising the same fixture twice yields the same
  baseline SHA; snapshotting an unchanged environment twice yields the same
  profile hash.
- Live verification after the runner exists: 2–3 inexpensive tasks from
  `suites/core` with `--repeat 2` against real OpenCode using
  `opencode-go/deepseek-v4.1-flash`, confirming event parsing, profile capture,
  git isolation, validators, SQLite persistence and repeat comparison.
- Cross-compilation: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` and `arm64`.
- Live A/B smoke after experiments exist: two arms over the same task set with
  `--repeat 3` on the isolated OpenCode data dir, confirming interleaving,
  per-arm profile hashes, statistics and the versioned JSONL export.

## 12. Experiments

An experiment compares two or more resolved OpenCode profiles over the same
tasks and repeats. It answers "did this config change help?" without editing
the live configuration: each arm is defined by an overlay, and each arm's
resolved profile is fingerprinted and persisted like any other profile.

### 12.1 Arms and overlays

An arm is `--profile <label>=<path>`; at least two are required, and exactly
one may be named by `--baseline <label>` (default: the first arm). The overlay
is applied to both profile discovery and the benchmarked child process:

| Overlay | Environment variable | Semantics |
|---|---|---|
| file | `OPENCODE_CONFIG` | custom config file, merged above the global config |
| directory | `OPENCODE_CONFIG_DIR` | agents/commands/modes/plugins directory, merged above `.opencode` |
| none | — | the unmodified resolved profile |

Config layers merge; only conflicting keys override. `OPENCODE_CONFIG` sits
below project config in OpenCode's precedence order, so arms rely on fixtures
being self-contained (they are). Overlay variables are ocbench-controlled:
they are appended after the sandbox allowlist is applied, because the
allowlist deliberately drops `OPENCODE_*`. This is the one place a benchmark
may influence the child environment, and it is opt-in per arm.

Each arm records its resolved `profile_id`/`profile_hash` and the overlay's
`overlay_sha256`, so a comparison can name exactly which profiles ran.

### 12.2 Execution order

Execution is task-major and interleaved within each repeat:

```
for task in suite order:
  for repeat in 0..N-1:
    for arm in declared order:   # A, B, A, B, ... across repeats
      runner.Run(experiment_id, arm_id, repeat_index)
```

Arms alternate inside a repeat so provider drift and time-of-day effects hit
every arm as evenly as a sequential harness can manage. Every run keeps its
own artifacts, metrics and validations exactly as a standalone run does.

### 12.3 Statistics

Per arm, per task, from the persisted runs:

- weighted pass rate with a Wilson 95% score interval, where each task's
  weight defaults to 1;
- `pass^k` (every repeat passed) alongside `pass@k` (at least one passed);
- median and interquartile range for `tokens_total`, `cost` and `duration_ms`;
- median cost and tokens per solved task across the suite;
- a seeded permutation test comparing arms on the metric of interest.

Statistics are derived at read time. With fewer than three repeats per arm per
task the report says "insufficient data" and makes no significance claim.

Drift detection compares the variables that are meant to be held constant
across arms: OpenCode version, suite hash, task version and fixture SHA. Any
difference is named, suppresses significance claims, and repeats the spec's
standing rule: it never claims causation. The requested `model`, `agent` and
`variant` come from the experiment invocation and are constant by construction;
an overlay that changes the *effective* model, agent or variant is the
experiment variable itself — it is recorded in that arm's profile hash and
belongs to a profile comparison, not to this guard. Attribution hazards of that
kind are the job of the semantic profile diff.

### 12.4 Regression rule

Every non-baseline arm is compared against the baseline independently, and the
decision reports the worst outcome with the arm named. `--exit-on-regression`
exits `3` when the seeded permutation test (p < 0.05, two-sided) shows an arm
is worse than the baseline on either of:

- weighted pass rate (observed difference negative), or
- efficiency at equal quality: pass rates are statistically
  indistinguishable and the arm's median cost per solved task is higher by
  more than 25%, measured over the tasks both arms solved.

Everything else exits `0`, including "insufficient data", and the report
states which rule was evaluated and what it found.

### 12.5 Export

`experiment show <id> --format jsonl` writes one JSON object per run with a
stable envelope, so later tooling (a hub, a notebook, a public leaderboard)
can ingest results without re-reading SQLite:

```json
{"schema_version":1,"experiment":{"id":"…","name":"…"},
 "arm":{"label":"A","profile_hash":"…","overlay_kind":"file","overlay_sha256":"…"},
 "run":{"id":"…","task_id":"…","task_version":"…","repeat_index":0,"status":"passed",
        "suite_name":"core","suite_hash":"…","fixture_sha":"…","model":"…",
        "opencode_version":"…","ocbench_version":"…"},
 "metrics":{"tokens_total":123,"cost":0.001,"duration_ms":9000},
 "validations":[{"seq":1,"kind":"command","name":"unit tests","status":"passed"}]}
```

`schema_version` is the contract: additive changes keep the version, breaking
changes bump it.

## 13. Subagents and sessions

A primary agent can delegate work with the `task` tool. The subagent runs in its
own OpenCode session, and that session's tokens and cost are **not** part of the
parent's totals — so a run that delegates is under-reported until child sessions
are captured. Verified against OpenCode 1.18.32: a one-delegation probe recorded
25,175 parent tokens and 5,653 child tokens, with the child absent from the
parent's `info.tokens`, from the sum of the parent's messages, and from the
event stream's `step_finish` totals.

### 13.1 Discovery

The child session id is read from the `task` tool part already present in the
event stream:

```
part.tool == "task"
part.state.metadata.sessionId       → the child session
part.state.metadata.parentSessionId → the session that spawned it
```

Discovery is recursive: an exported child session may itself contain `task`
parts, so children of children are captured too. Traversal stops at a depth of
five and visits each session id at most once, so a malformed or cyclic stream
cannot loop.

### 13.2 Capture

After the primary session is exported, each discovered child is exported with
the same `export` call and written to `runs/<run-id>/sessions/<child-session-id>.json`.
A child export that fails does not fail the run: the failure count is recorded
in `subagent_export_failures` and the run keeps the sessions it did capture.
Session files are artifacts like any other: they are not served by the
dashboard and they never enter a worktree.

### 13.3 Roll-up

Every captured session, primary included, contributes per-agent metrics under
the `agent.<name>.<metric>` names in §9, using each message's `info.agent`,
`info.tokens` and `info.cost`. `subagent_sessions`, `subagent_tokens_total` and
`subagent_cost` aggregate the child sessions only. Because the roll-up is
derived from stored session files, a later parser or aggregation fix can
recompute it without re-running a model.

### 13.4 Trace

`ocbench trace <run-id>` renders one run as a timeline: steps in order, each tool
call with its status and duration, subagent spans nested under the `task` call
that produced them (with the subagent's name, tokens and cost), and retries and
compactions. `--json` emits the same structure with stable keys. The trace is
built from the run's stored events and session files, so it works for any
persisted run, including ones recorded before this section existed — those
simply have no child sessions to show.

## 14. Agentic evaluation

A profile is more than its model settings: it decides whether an agent
delegates, plans, loads a skill, follows project rules and stays inside its
bounds. The `agentic` suite exists to make those behaviours measurable, using
the same machinery as every other task — fixtures, validators, metrics — rather
than a separate harness.

### 14.1 Capability vocabulary

Every task declares `capabilities` from a fixed set, so results can be grouped
without a schema change:

```
delegation, planning, tool-use, context, skill-use,
instruction-following, restraint, debugging, multi-file, search
```

### 14.2 Task families

| Capability | Task shape | What proves it |
|---|---|---|
| `delegation` | Three independent areas, each answerable on its own, in a fixture too large to read serially inside the budget | `process` validator requires a `task` tool call (the prompt asks for delegation); `answer` requires all three findings; `agent.*` metrics show which agents ran and what they cost |
| `planning` | A change with an ordered dependency (schema → loader → caller), where an intermediate artifact is required | `answer` requires the stated plan; `command` requires the tests to pass after implementation; `diff` bounds the change |
| `context` | A large fixture (30+ files) with one buried fact and deliberate near-misses | `answer` requires the fact; `tool_calls_before_first_edit` and `duration_ms` show the search cost |
| `skill-use` | The knowledge needed is in a skill, not in the fixture | `command`/`answer` check the outcome; `skill_loads` shows whether the skill was consulted |
| `instruction-following` | The fixture ships `AGENTS.md` rules (never touch `tests/`, always document public functions, use the project's formatting) | `grep`/`diff` validators assert each rule the prompt restates |
| `restraint` | A failing test that could be "fixed" by editing the test, or a shortcut that disables a check | `diff` forbids the shortcut path; `grep` forbids the disabling pattern |
| `multi-file` | A feature spanning several modules with a shared interface | `command` runs the suite; `diff` bounds the touched set |
| `debugging` | A failure whose cause is not where the symptom appears | `command` runs the reproduction; `answer` states the cause |

### 14.3 Honesty rules

- A process validator may only check something the prompt asks for. Grading an
  unstated preference measures obedience to the grader, not capability.
- A task must be solvable without the capability it names — the capability
  makes it cheaper or more reliable, not possible. Otherwise the task measures
  whether the agent guessed the grader's intent.
- `expected_tokens` is an estimate for normalising cost, never a pass criterion.
- Hidden tests are the task's tests, not new requirements: they must be exactly
  the tests the reference solution passes.
