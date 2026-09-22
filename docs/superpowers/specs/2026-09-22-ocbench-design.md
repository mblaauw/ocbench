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
- Event stream schema (from the locally installed `@opencode-ai/sdk` types):
  each event is `{"type": "...", "properties": {...}}`; the `Event` union is
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
```

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
    evaluator/          hidden; never materialised into the worktree
```

`suite.yaml`: `name`, `version`, `description`, optional `defaults.timeout`.

`task.yaml`:

```yaml
id: py-bugfix-017
version: 1
name: Fix retry behaviour
tags: [python, debugging]
timeout: 300
requires: [python3]
allow_changes:
  - "src/**"
  - "tests/**"
validators:
  - kind: command
    name: unit tests
    command: ["python3", "-m", "unittest", "discover", "-s", "tests"]
  - kind: answer
    name: root cause stated
    patterns: ["retry", "off-by-one"]
    # patterns may live in evaluator/answer.json instead
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
  of band; it is never written into the agent worktree.

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
  → git status/diff versus baseline SHA → diff.patch, changed-files metrics
  → validators with sandboxed env → validation results
  → normalise events → metrics, aggregate result.json
  → persist run + metrics + validations; keep worktree only with --keep-worktree
```

Environment sandbox: allowlist by default. Always kept: `HOME`, `PATH`,
`LANG`/`LC_*`, `TZ`, `TERM`, `USER`. Dropped: everything else, notably
`KUBECONFIG`, `AWS_*`, `AZURE_*`, `GOOGLE_*`, `GITLAB_TOKEN`,
`SSH_AUTH_SOCK`. Additional names may be forwarded via
`config.sandbox.pass_env`. `GIT_TERMINAL_PROMPT=0` always set.
`--inherit-environment` switches to a deny-list mode and is explicit opt-in.
The same env policy is used for validators.

`--dry-run` performs `doctor`-level discovery, suite loading, fixture
materialisation and worktree creation, prints the resolved plan (profile hash,
tasks, validators, env names), and exits without invoking a model.

Exit codes: `0` pipeline completed (regardless of task pass/fail), `1`
infrastructure error, `2` usage/config error, `3` reserved for
`--exit-on-task-failure`.

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
duration_ms, validator_failures, success, first_shot_success
```

Raw JSONL events are canonical and retained per run; normalisation is
re-runnable so a parser fix can reprocess historical runs without schema
changes.

## 10. Commands (v0.1)

```
ocbench version
ocbench doctor [--json]
ocbench snapshot [--json] [--agent A] [--model M] [--variant V] [--dir D]
ocbench run <suite> [task] [--repeat N] [--dry-run] [--suite-dir P]
                     [--agent A] [--model M] [--variant V]
                     [--inherit-environment] [--keep-worktree] [--json]
ocbench history [--task T] [--limit N] [--json]
ocbench compare <a> <b> [--json]        # ids, hashes, "latest", "previous"
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
