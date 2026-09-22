# ocbench Plan 2 — Benchmark Suites and the Run Pipeline

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver `ocbench run`: execute immutable benchmark suites against the resolved OpenCode profile in disposable git worktrees, capture the raw JSONL event stream, run deterministic validators, persist everything, and print a repeatable result report.

**Architecture:** New packages `internal/suite` (schema, loader, embedded core suite), `internal/runner` (fixture materialisation, deterministic git baselines, worktrees, environment sandbox, pipeline orchestration), `internal/evaluation` (event parsing, metric normalisation, validators). The `opencode.Adapter` grows `Start`/`Export`; the real implementation streams `opencode run --format json` via temp files (not pipes — Bun truncates piped output at 65536 bytes, proven in Plan 1). All fixture sources normalise to `source → immutable commit SHA → git worktree`. Store grows suite/task/run/metric/validation repositories.

**Tech Stack:** existing Plan 1 stack; no new dependencies. Suite fixtures use Python 3 stdlib only (air-gapped safe); validators declare `requires:` and SKIP when a requirement is absent.

**Spec:** `docs/superpowers/specs/2026-09-22-ocbench-design.md` — §2 (CLI contracts incl. the observed JSONL envelope), §7 (suites/tasks), §8 (runner pipeline, sandbox, exit codes), §9 (metrics), §10 (commands), §11 (verification). Plan 1 is merged to `main`; this plan starts from `main`.

## Global Constraints

- Module path `mbl/ocbench`. No CI files. No new dependencies (stdlib only). Deps stay vendored.
- Everything compiles and tests on macOS and cross-compiles for `linux/amd64` + `linux/arm64` with `CGO_ENABLED=0`.
- **No test may invoke the real `opencode` binary, the network, or the developer's real data dir.** Tests inject fake adapters and use temp dirs; the real binary is exercised only in the explicitly-marked live smoke tasks (7's Step and Task 11), never in `go test`.
- Determinism: materialising the same fixture twice must produce the same baseline commit SHA on any machine (fixed author/committer identity and dates). An identical canonical profile must keep resolving to one profile hash (Plan 1 guarantee, still asserted).
- Never persist secret values; env values are never stored, only names. Worktree env is an allowlist by default.
- Validators run with the sandboxed env and an argv array — never a shell string.
- Worktrees are always disposable: created under `$OCBENCH_HOME/../cache` or a temp run dir and removed on completion unless `--keep-worktree`.
- Exit codes: `0` pipeline completed (regardless of task pass/fail), `1` infrastructure error, `2` usage/config error, `3` reserved for `--exit-on-task-failure`.
- Code style: no comments unless they explain non-obvious intent; `gofmt` clean; `go vet ./...` clean. Commits `feat:`/`test:`/`fix:`/`chore:` lowercase imperative.

## Observed CLI contract (probe, 2026-09-22, OpenCode 1.18.32)

`opencode run --format json --dir <d> --agent build --model opencode-go/deepseek-v4.1-flash --variant low --auto "<prompt>"` emitted 9 JSONL lines:

```
{"type":"step_start","timestamp":…,"sessionID":"ses_…","part":{"type":"step-start",…}}
{"type":"tool_use","timestamp":…,"sessionID":"ses_…","part":{"type":"tool","tool":"read","callID":"…","state":{"status":"completed","input":{…},"output":"…"}}}
{"type":"step_finish","timestamp":…,"sessionID":"ses_…","part":{"type":"step-finish","reason":"tool-calls","tokens":{"total":…,"input":…,"output":…,"reasoning":…,"cache":{"write":…,"read":…}},"cost":…}}
{"type":"text","timestamp":…,"sessionID":"ses_…","part":{"type":"text","text":"…"}}
```

- No idle/terminal event; completion = process exit 0.
- `tool_use` states observed: `completed`. Handle `error` (and pending/running) defensively.
- macOS resolves temp paths through symlinks (`/var/…` → `/private/var/…`); path normalisation must map both the run dir and its `filepath.EvalSymlinks` form to `<run-dir>`/`<worktree>`.
- Probe evidence: `events.jsonl` + `stderr.txt` + `git diff` under `/var/folders/…/T/ocbench-probe/` on the dev machine; copy the JSONL into `internal/evaluation/testdata/probe-events.jsonl` (Task 5) as a real golden fixture.

## Review Focus

1. **Fixture/worktree determinism** — fixed commit dates and identities, sorted traversal, no absolute paths or timestamps in hashes. A second materialisation of the same fixture must produce the identical SHA (test asserts equality across two calls in different temp roots).
2. **Process hygiene** — child killed on timeout and cancellation including its process group; temp files removed on every path; no goroutine leak in the event tailer; `Wait` always reaped.
3. **Sandbox correctness** — an allowlist that actually drops `KUBECONFIG`/`AWS_*`/`GITLAB_TOKEN`/`SSH_AUTH_SOCK`; `--inherit-environment` is the only path to full env; the env *names* recorded in the profile equal the names actually passed to the child.
4. **Event parser resilience** — unknown `type`/`part.type` values, malformed lines, truncated final line, and very long lines (≥1 MB) must not abort the run; parse failures are counted and surfaced, never fatal.
5. **No hidden coupling to the developer's machine** — no test writes under `$HOME`; `$OCBENCH_HOME`, `$XDG_*` and `HOME` are always faked; the live smoke is the only real-environment step.

---

### Task 1: `internal/suite` — schema, loader, validation

**Files:**
- Create: `internal/suite/suite.go`, `internal/suite/load.go`, `internal/suite/hash.go`
- Create: `internal/suite/suite_test.go`, `internal/suite/testdata/mini/{suite.yaml,tasks/t1/{task.yaml,prompt.md,fixture/a.txt},tasks/t2/…}`
- Test: same package

**Interfaces:**
- Consumes: nothing (stdlib + `gopkg.in/yaml.v3`).
- Produces:

```go
package suite

type Suite struct {
	Name, Version, Description string
	Defaults struct{ TimeoutSeconds int } `yaml:"defaults"`
	Dir string          // absolute dir for on-disk suites; "" for embedded
	FS  fs.FS           // rooted at the suite dir (embedded or os.DirFS)
	Tasks []*Task
	Hash string
}

type Task struct {
	ID, Version, Name string
	Tags []string
	TimeoutSeconds int
	Requires []string
	AllowChanges []string
	Validators []Validator
	Prompt string
	Dir string
	Fixture fs.FS          // subtree of FS at tasks/<id>/fixture
	Evaluator map[string][]byte
	FixtureHash string
	SpecHash string
}

type Validator struct {
	Kind string      // "command" | "answer"
	Name string
	Command []string
	Patterns []string
	Mode string      // answer: "all" (default) | "any"
}

func LoadFS(fsys fs.FS, root string) (*Suite, error)
func LoadDir(dir string) (*Suite, error)   // os.DirFS(dir) + LoadFS
func (s *Suite) Task(id string) (*Task, error)
func (t *Task) EffectiveTimeout(s *Suite) time.Duration
```

Rules (spec §7): `suite.yaml` requires `name` and `version`; each `tasks/<id>/task.yaml` requires `id`, `version`, `name`, `prompt.md` and a `fixture/` directory; `id` must equal the directory name; unknown validator kinds are load errors; `answer` validators need `patterns` in `task.yaml` or in `evaluator/answer.json` (`{"patterns":[…],"mode":"all|any"}`); command validators need a non-empty argv; `timeout` defaults to `suite.defaults.timeout` then 900; tags default empty; `allow_changes` defaults empty (meaning: any change is unexpected). Both YAML files decode with `yaml.Decoder.KnownFields(true)` so a misspelled key is a load error naming the file, never a silent default.

`Hash` = SHA256 of canonical JSON (use `canon.JSON`) of `{name, version, tasks:[{id, version, timeout, requires, allow_changes, validators, prompt_sha256, fixture_hash, evaluator_sha256}]}` sorted by task id. `FixtureHash` = SHA256 over sorted `relpath\0sha256(bytes)` of every regular file in the fixture tree. Loading must be deterministic: walk with `fs.WalkDir` (lexical order) and never include absolute paths.

- [ ] **Step 1: Write failing loader tests** in `internal/suite/suite_test.go` against `testdata/mini`: happy path (2 tasks, hashes non-empty, fixture hash changes when a fixture file changes); missing prompt → error naming the task and file; id/directory mismatch → error; unknown validator kind → error; `answer` patterns resolved from `evaluator/answer.json`; timeout defaulting chain; suite hash is order-independent (two suites whose task dirs load in the same order produce identical hashes — shuffle `Tasks` after load and re-hash via an exported `HashTasks(tasks []*Task) (string, error)` helper used by both `LoadFS` and the test).
- [ ] **Step 2: Run and watch them fail**, then implement `suite.go`/`load.go`/`hash.go`.
- [ ] **Step 3: Run tests green**; `go vet`, `gofmt`.
- [ ] **Step 4: Commit** `feat: add suite and task schema loader with content hashing`

---

### Task 2: Embedded core suite and suite resolution

**Files:**
- Create: `suites/embed.go` (package `suites`, `//go:embed core`, `func FS() fs.FS`)
- Create: `suites/core/suite.yaml`, `suites/core/tasks/{repo-investigation,py-bugfix,multi-file-feature}/{task.yaml,prompt.md,fixture/…}`
- Create: `internal/suite/resolve.go`; test `internal/suite/resolve_test.go`
- Modify: `internal/cli/deps.go` (add `SuiteFS fs.FS` to `Deps`, default `suites.FS()`)

**Interfaces:**
- Consumes: Task 1 loader.
- Produces:

```go
// internal/suite
type Source struct { Name string; Embedded bool; Dir string }
func ListSources(fsys fs.FS) ([]Source, error)              // embedded names
func Resolve(fsys fs.FS, paths config.Paths, name, suiteDirFlag string) (*Suite, Source, error)
func Export(s *Suite, dest string) error                     // write embedded suite to disk
```

Resolution order: explicit `--suite-dir`/`suiteDirFlag` → `$OCBENCH_HOME/suites/<name>` if present → embedded. Errors name every location tried. `Export` writes all suite files preserving relative paths and fails if `dest` is non-empty.

Core suite v1 tasks (all fixture-only, Python stdlib, no network):
1. `repo-investigation` (answer validator): small Python package with a planted defect; prompt asks *which* function has the bug and what the correct behaviour is; `evaluator/answer.json` holds regex patterns; no file changes expected (`allow_changes: []`).
2. `py-bugfix`: `calc.py` with a wrong operator plus `tests/test_calc.py` (stdlib `unittest`) that fails; validator `["python3","-m","unittest","discover","-s","tests"]`; `allow_changes: ["calc.py"]`; `requires: [python3]`.
3. `multi-file-feature`: add a `summarize()` function used by a CLI entry point and by tests across three files; validator runs the tests and a stdlib smoke script; `allow_changes: ["src/**","tests/**","cli.py"]`.

Each `task.yaml` sets `version: 1`, tags, timeout 300.

- [ ] **Step 1: Write failing tests**: embedded FS lists the three task IDs; `Resolve` prefers an on-disk override over embedded (build a temp suite dir); `Export` round-trips (load embedded → export → `LoadDir` → identical `Hash`).
- [ ] **Step 2: Implement** `suites/embed.go`, the three core tasks, `resolve.go`; run tests green.
- [ ] **Step 3: Verify the fixtures are honest**: run each task's validator command manually in a scratch copy of its fixture and confirm it FAILS on the untouched fixture and PASSES after applying the intended fix (do this with a throwaway `git stash`-free copy under `/tmp`; record the exact commands and outputs in your report). This proves the tasks measure something.
- [ ] **Step 4: Commit** `feat: add embedded core suite v1 with three fixture tasks`

---

### Task 3: Deterministic fixture baselines and disposable worktrees

**Files:**
- Create: `internal/runner/gitsource.go`, `internal/runner/worktree.go`
- Test: `internal/runner/gitsource_test.go`, `internal/runner/worktree_test.go`

**Interfaces:**
- Consumes: `suite.Task.Fixture`/`FixtureHash`; `canon`.
- Produces:

```go
package runner

type Baseline struct {
	RepoDir string // local git repo holding the baseline commit
	SHA     string
}

// MaterializeFixture writes an embedded fixture into the cache as a real git
// repo with one deterministic commit and returns it. Idempotent per FixtureHash.
func MaterializeFixture(ctx context.Context, cacheDir string, fixture fs.FS, fixtureHash string) (Baseline, error)

// MaterializeRepo normalises an external git source (local path or URL, ref)
// to a local repo + immutable SHA. Remote sources require an explicit URL.
func MaterializeRepo(ctx context.Context, cacheDir, source, ref string) (Baseline, error)

func CreateWorktree(ctx context.Context, b Baseline, dest string) error
func RemoveWorktree(ctx context.Context, b Baseline, dest string) error
func ChangedFiles(ctx context.Context, worktree, baselineSHA string) ([]string, error) // git status --porcelain -z, includes committed changes vs baseline
func DiffAgainstBaseline(ctx context.Context, worktree, baselineSHA string) ([]byte, error) // git diff <sha> -- binary-safe
```

`MaterializeFixture`: cache path `<cacheDir>/fixtures/<fixtureHash>`; if `<path>/.git` exists with a commit, reuse (read SHA via `git rev-parse HEAD`). Otherwise `git init -q -b main`, write every regular file (0644, directories as needed), `git add -A`, commit with env `GIT_AUTHOR_NAME=ocbench`, `GIT_AUTHOR_EMAIL=ocbench@localhost`, `GIT_COMMITTER_NAME=ocbench`, `GIT_COMMITTER_EMAIL=ocbench@localhost`, `GIT_AUTHOR_DATE=2000-01-01T00:00:00Z`, `GIT_COMMITTER_DATE=2000-01-01T00:00:00Z`, `-c commit.gpgsign=false`. Concurrency: guard with a lock file (`<path>.lock`, `O_CREATE|O_EXCL`, retry/backoff) so parallel runs cannot corrupt the cache.

`CreateWorktree` runs `git -C <repo> worktree add --detach <dest> <sha>`; `RemoveWorktree` runs `git -C <repo> worktree remove --force <dest>` then `git -C <repo> worktree prune`. Both tolerate already-absent dests.

`ChangedFiles` parses `git status --porcelain -z` (NUL-separated; handle `R` rename entries' two paths) and returns paths relative to the worktree root, sorted. `DiffAgainstBaseline` runs `git -C <worktree> diff <sha> --` and appends `git ls-files --others --exclude-standard` content for untracked files as a `diff --git`-style section? **No** — keep it simple and honest: `DiffAgainstBaseline` returns the tracked diff only; untracked files are reported via `ChangedFiles` and their bytes are captured separately by the runner as `untracked/<path>` artifacts. Document this in the function comment.

- [ ] **Step 1: Write failing tests** with a temp fixture FS built in-test: materialise twice in two different cache roots → identical SHA; second call in the same root reuses without a new commit; `ChangedFiles` after modifying a file, adding an untracked file, and committing a change (all three detected vs baseline); `RemoveWorktree` removes the dir and prunes; lock contention: two concurrent `MaterializeFixture` calls on the same cache root both succeed and agree on the SHA.
- [ ] **Step 2: Implement** `gitsource.go`/`worktree.go`; run tests green.
- [ ] **Step 3: Commit** `feat: add deterministic git fixtures and disposable worktrees`

---

### Task 4: Sandboxed child environment

**Files:**
- Create: `internal/runner/environment.go`; test `internal/runner/environment_test.go`

**Interfaces:**
- Produces:

```go
package runner

type EnvPolicy struct {
	Inherit bool     // true only for --inherit-environment
	PassEnv []string // additional names forwarded in allowlist mode
}

func BuildEnv(base []string, policy EnvPolicy) []string // sorted, KEY=VALUE
func EnvNames(env []string) []string                    // sorted keys
```

Allowlist (always kept): `HOME PATH USER LOGNAME SHELL TMPDIR TEMP TMP LANG TERM TZ`, any `LC_*`, plus `PassEnv`. Always set (overriding): `GIT_TERMINAL_PROMPT=0`, `GIT_PAGER=cat`, `PAGER=cat`, `NO_COLOR=1`, `OCBENCH=1`. Inherit mode returns `base` unchanged except the always-set overrides. Deduplicate by key, last write wins, output sorted for deterministic tests and for the profile's `env_names`.

- [ ] **Step 1: Failing tests**: allowlist drops `KUBECONFIG`, `AWS_ACCESS_KEY_ID`, `GITLAB_TOKEN`, `SSH_AUTH_SOCK`; keeps `HOME`/`PATH`/`LC_ALL`; `PassEnv` forwards exactly the named vars; `GIT_TERMINAL_PROMPT=0` always present; inherit mode keeps `AWS_*`; `EnvNames` sorted and value-free.
- [ ] **Step 2: Implement, run green, commit** `feat: add sandboxed child environment builder`

---

### Task 5: Event parsing and metric normalisation

**Files:**
- Create: `internal/evaluation/events.go`, `internal/evaluation/metrics.go`
- Create: `internal/evaluation/testdata/probe-events.jsonl` (copy of the real probe stream)
- Test: `internal/evaluation/events_test.go`, `internal/evaluation/metrics_test.go`

**Interfaces:**
- Produces:

```go
package evaluation

type Event struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	SessionID string          `json:"sessionID"`
	Part      json.RawMessage `json:"part"`
}

type Part struct {
	Type string `json:"type"`
	Tool string `json:"tool"`
	CallID string `json:"callID"`
	State json.RawMessage `json:"state"`
	Text  string `json:"text"`
	Reason string `json:"reason"`
	Tokens *Tokens `json:"tokens"`
	Cost   *float64 `json:"cost"`
	Attempt *int `json:"attempt"`
}

type Tokens struct {
	Total, Input, Output, Reasoning int64
	Cache struct{ Read, Write int64 } `json:"cache"`
}

func ParseLine(line []byte) (Event, error)          // envelope only; validates type non-empty
func (e Event) PartDecoded() (Part, error)

type Metrics struct {
	Steps, ToolCalls, ToolCallsFailed, SubagentCalls, SkillLoads int
	MCPCalls, Retries, Compactions, ParseErrors int
	ToolCallsByName, MCPCallsByServer map[string]int
	TokensInput, TokensOutput, TokensReasoning, TokensCacheRead, TokensCacheWrite, TokensTotal int64
	Cost float64
	Texts []string
	FinalAnswer string // concatenation of text parts joined by "\n"
}

func NewMetrics(mcpServers []string) *Metrics
func (m *Metrics) Observe(e Event)     // never returns an error; increments ParseErrors on undecodable parts
func (m *Metrics) MetricsMap() map[string]float64 // spec §9 names
```

Mapping rules: `step_start` → `Steps++`; `tool_use` → `ToolCalls++`, `ToolCallsByName[tool]++`, failed when `state.status=="error"`, subagent when `tool=="task"`, skill when `tool=="skill"`, MCP when `tool` has a `<server>_<tool>` prefix for any server in `mcpServers`; `step_finish` → add tokens/cost (skip a `Tokens` whose `total` is 0 to avoid double counting); `text` → append to `Texts`; `part.type=="retry"` → `Retries++`; `part.type=="compaction"` → `Compactions++`. Unknown types are ignored silently (forward compatibility); undecodable `part` JSON increments `ParseErrors`.

`MetricsMap` names (float64): `steps`, `tool_calls_total`, `tool_calls_failed`, `tool_calls_<sanitized tool>` (non-alphanumerics → `_`), `subagent_calls`, `skill_loads`, `mcp_calls`, `mcp_calls_<server>`, `retries`, `compactions`, `parse_errors`, `tokens_input`, `tokens_output`, `tokens_reasoning`, `tokens_cache_read`, `tokens_cache_write`, `tokens_total`, `cost`.

- [ ] **Step 1: Failing tests** using `probe-events.jsonl` plus synthetic lines. The probe's exact expected values (hand-derived from the fixture; assert them literally): `Steps=3`, `ToolCalls=2` (`read`=1, `edit`=1), `ToolCallsFailed=0`, `SubagentCalls=0`, `SkillLoads=0`, `MCPCalls=0`, `Texts=1`, `TokensInput=15540`, `TokensOutput=212`, `TokensReasoning=239`, `TokensCacheRead=31104`, `TokensCacheWrite=0`, `TokensTotal=47095`, `Cost=0.002694912` (compare with a tolerance of 1e-9), `FinalAnswer` equals the probe's final text. Also: malformed line → `ParseErrors`, no panic; unknown type ignored; 1 MB text line parses; tool error state counts as failed; MCP prefix matching only when the server is in the list.
- [ ] **Step 2: Implement, run green, commit** `feat: add event parsing and metric normalisation`

---

### Task 6: Validator engine

**Files:**
- Create: `internal/evaluation/validator.go`; test `internal/evaluation/validator_test.go`

**Interfaces:**
- Produces:

```go
package evaluation

type ValidatorSpec struct {
	Kind, Name string
	Command []string
	Patterns []string
	Mode string // answer only: "all" | "any"
}

type ValidationResult struct {
	Seq int
	Kind, Name, Status string // passed|failed|error|skipped|timeout
	ExitCode int
	DurationMS int64
	Output string // full captured output (runner writes it to a file)
	Excerpt string // first 2 KiB, rune-safe
}

func Requirements(requires []string) (missing []string)   // exec.LookPath for each; "python3" etc.
func RunValidator(ctx context.Context, seq int, spec ValidatorSpec, dir string, env []string, timeout time.Duration, finalAnswer string) ValidationResult
```

Command validators: `exec.CommandContext` with argv, `Dir=dir`, `Env=env`, combined output captured (bounded to 4 MiB), timeout → `timeout` status with `ExitCode=-1`, non-zero exit → `failed`, start error → `error`. Answer validators: compile each pattern with `regexp.Compile` (invalid pattern → `error` status with the message); `all` requires every pattern to match, `any` requires one; failure message lists which patterns did not match. `Requirements` returns names whose `exec.LookPath` fails; the runner marks such validators `skipped` with a reason.

- [ ] **Step 1: Failing tests**: passing/failing command; timeout (sleep) kills the child; output excerpt is rune-safe at 2 KiB; missing binary → `error`; answer `all` vs `any`; invalid regex → `error`; `Requirements` finds `sh` but not `definitely-not-a-real-binary`.
- [ ] **Step 2: Implement, run green, commit** `feat: add deterministic validator engine`

---

### Task 7: Adapter `Start`/`Export` — streaming `opencode run`

**Files:**
- Modify: `internal/opencode/adapter.go` (interface + types), `internal/opencode/real.go`
- Create: `internal/opencode/session.go`, `internal/opencode/proc_unix.go` (`//go:build unix`)
- Modify: `internal/opencode/helper_test.go` (add streaming modes), add `internal/opencode/session_test.go`

**Interfaces:**
- Produces:

```go
type RunRequest struct {
	Dir, Prompt, Agent, Model, Variant string
	Auto, Pure bool
	Env []string
	Timeout time.Duration
}

type Session struct { /* unexported */ }
func (s *Session) ID() string                  // sessionID from the first parsed event; "" until known
func (s *Session) Events() <-chan []byte       // raw JSONL lines, closed after Wait
func (s *Session) Wait() (exitCode int, err error)
func (s *Session) Kill()                       // SIGTERM to the process group, SIGKILL after 5s
func (s *Session) Stderr() string

// Adapter interface grows:
//   Start(ctx context.Context, req RunRequest) (*Session, error)
//   Export(ctx context.Context, sessionID string) ([]byte, error)
```

`Start` builds argv `run --format json --dir <dir> [--agent A] [--model M] [--variant V] [--auto] [--pure] -- <prompt>` (prompt last, `--` separator so prompts starting with `-` are safe), sets `SysProcAttr{Setpgid:true}`, redirects stdout/stderr to temp files under the run's temp dir (never pipes — Bun truncates piped output at 64 KiB), and starts a tailer goroutine that polls the stdout file (10 ms ticker) emitting complete lines on `Events()`, then drains and closes after `Wait` returns. The tailer stops when the process exits and the file is fully drained. `Wait` is single-call-safe (`sync.Once`), reaps the child, removes temp files, and returns the exit code. `Kill` signals the process group (`syscall.Kill(-pgid, SIGTERM)`, then SIGKILL after 5 s). On `ctx` cancellation, `Wait` kills the group and returns `context.Canceled`.

`Export` runs `export <sessionID>` with the Plan 1 temp-file capture and returns stdout bytes.

- [ ] **Step 1: Extend the helper process** with streaming modes: `run-ok` (emit the probe's 9 lines with `--format json`, exit 0), `run-fail` (2 lines, exit 3), `run-slow` (1 line, sleep 30 s), `run-big` (one 1 MiB text line), `run-no-session` (lines without sessionID), plus `export` returning a canned session JSON.
- [ ] **Step 2: Failing tests**: `Start` yields all 9 lines in order and `Wait` returns 0; `ID()` becomes the probe's `ses_…`; `run-fail` → exit 3 and stderr captured; `run-slow` + 200 ms timeout → `Wait` returns within 3 s with a timeout error and the helper's process group is gone (assert via a sentinel: helper writes a grandchild pid file — skip if flaky, instead assert `Wait` returns promptly and `Kill` is idempotent); `run-big` line delivered intact (length 1 MiB); `run-no-session` → `ID()==""` and no panic; cancellation via `context.WithCancel` returns promptly; `Export` returns the canned JSON. All via `Options{TestPrefix: …}`; no real binary.
- [ ] **Step 3: Run green; `go vet`, `gofmt`.**
- [ ] **Step 4: Commit** `feat: add streaming run sessions and session export to the adapter`

---

### Task 8: Runner pipeline, artifacts and persistence

**Files:**
- Create: `internal/runner/runner.go`, `internal/runner/artifacts.go`
- Create: `internal/store/run.go`; test `internal/store/run_test.go`
- Test: `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: Tasks 1–7, `profile`, `store`, `config`.
- Produces:

```go
package runner

type Request struct {
	Suite *suite.Suite
	Task *suite.Task
	Profile *profile.Profile
	Paths config.Paths
	EnvPolicy EnvPolicy
	Agent, Model, Variant string
	Auto, Pure bool
	DryRun, KeepWorktree bool
	ExperimentID string
	RepeatIndex int
	MCPTools []string // MCP server names for metric classification
}

type Result struct {
	RunID string
	Status string // dry_run|passed|failed|error|timeout
	TaskID string
	SessionID string
	Metrics map[string]float64
	Validations []evaluation.ValidationResult
	ChangedFiles []string
	UnexpectedFiles []string
	DurationMS int64
	ArtifactsDir string
	Error string
}

func Run(ctx context.Context, a opencode.Adapter, st *store.Store, req Request) (Result, error)
```

Pipeline (spec §8), in order: create `runs/<uuid>/`; materialise fixture baseline; create worktree under `<runs>/<uuid>/worktree`; build env; when `DryRun`: write `plan.json`, insert a `dry_run` run row, return. Otherwise `Start` the session, tee every raw line into `events.jsonl` while feeding `Metrics`, enforce the task timeout (adapter-level), then `Export` → `session.json`; capture `git status`/`diff` vs baseline → `diff.patch`, `changed.json`, untracked file bytes under `untracked/`; run validators (writing `validation/<seq>-<name>.log`); copy `task.yaml`/`prompt.md`/`suite.yaml` into the artifacts; compute `unexpected` = changed files not matching `allow_changes` globs; derive status (`timeout` if the adapter timed out, `error` if the session failed, `passed` when no validator failed, else `failed`); write `result.json`; persist run row + metrics + validations; remove the worktree unless `KeepWorktree`.

Store additions (`internal/store/run.go`): `InsertSuite`, `InsertTask`, `InsertRun(RunRow)`, `InsertRunMetrics(runID, map[string]float64)`, `InsertRunValidations(runID, []ValidationRow)`, `GetRun(id)`, `ListRuns(limit, taskID)`, all single-transaction, RFC3339 UTC, `ON CONFLICT DO NOTHING` for suite/task upserts.

- [ ] **Step 1: Store tests first** (insert/read round-trip, FK to profile, metric upsert semantics, list ordering).
- [ ] **Step 2: Runner tests** with a scripted fake adapter (queue of canned JSONL per call): happy path produces all artifacts, one run row, metrics and validations; validator failure → status `failed`; adapter error → `error`; timeout → `timeout`; `DryRun` → no session started, `plan.json` present, run row `dry_run`; `KeepWorktree` keeps the dir, default removes it; `unexpected` computed from `allow_changes`.
- [ ] **Step 3: Implement, run green, commit** `feat: add run pipeline with artifacts and persistence`

---

### Task 9: `ocbench run` CLI, repeat aggregation, dry-run

**Files:**
- Create: `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/cli/render_run.go`
- Modify: `internal/cli/root.go` (register the command)

**Interfaces:**
- Produces: `ocbench run [suite] [task...]` with flags `--repeat N` (default `config.defaults.repeat`), `--dry-run`, `--suite-dir`, `--agent`, `--model`, `--variant`, `--inherit-environment`, `--keep-worktree`, `--json`, `--exit-on-task-failure`.
- Behaviour: snapshot the profile once per invocation with `--dir` = the process working directory (dedupe by hash), create one `experiments` row per invocation, run tasks in plan order; with `--repeat N`, run every task N times sequentially (task-major order: task1×N, task2×N, …) and each execution gets its own run row with `repeat_index`; print per-execution lines then an aggregate table per task (success count/rate, median tokens_total, min/max, median tool calls, range, median duration). `--json` emits the aggregate + run ids. Exit code 0 when the pipeline completed; `--exit-on-task-failure` → 3 when any validator failed; infrastructure failures → 1.
- The profile snapshot passes `SandboxMode` from the effective policy (`"inherit"` when `--inherit-environment` or `config.sandbox.inherit_environment`). No run-dir/worktree prefixes are added to the profile in this plan: snapshots are taken with `--dir` = cwd before any worktree exists, and event paths are per-run artifact data, not hash inputs.

- [ ] **Step 1: Failing CLI tests** with injected fake adapter + temp paths: single run prints PASS and writes one row; two tasks with one failing validator prints `1/2 successful` and exit 0, with `--exit-on-task-failure` exit 3; `--repeat 2` produces two rows per task and an aggregate with success rate; `--dry-run` never calls `Start` (fake records calls); `--inherit-environment` flips the recorded `environment.sandbox` in the snapshot; `--json` parses.
- [ ] **Step 2: Implement, run green, commit** `feat: add run command with repeats, dry-run and aggregate report`

---

### Task 10: Core suite tasks 4–5 and authoring docs

**Files:**
- Create: `suites/core/tasks/{config-yaml-fix,code-review}/{task.yaml,prompt.md,fixture/…}` (+ `evaluator/answer.json` for `code-review`)
- Create: `docs/task-authoring.md`; modify `suites/core/suite.yaml` (version bump to `1.1.0`)
- Test: `internal/suite/core_test.go` asserting both new tasks load and their validators fail on the untouched fixture and pass after the intended fix (same manual proof as Task 2 Step 3, encoded where practical as fixture-level unit tests that copy the fixture to a temp dir, apply the fix from `evaluator/fix.patch` if present, and run the validator).

Task 4 `config-yaml-fix`: fixture is a small app with `config/app.yaml` and a stdlib Python loader/test that fails because a key is misspelled/mistyped; validator runs the tests; `allow_changes: ["config/**"]`; `requires: [python3]`.
Task 5 `code-review`: fixture is a module with three planted defects (an off-by-one, a swallowed exception, a wrong default); prompt asks for a structured review listing each defect and its location; answer validator patterns require all three defect keywords; no changes allowed.

`docs/task-authoring.md`: schema reference, determinism rules, validator rules, hidden evaluator rules, the "validators must fail on the untouched fixture" rule, and how to add external suites.

- [ ] **Step 1: Failing tests**, then fixtures/tasks/docs.
- [ ] **Step 2: Run green; verify both tasks' validators fail-before/pass-after manually and record evidence.**
- [ ] **Step 3: Commit** `feat: add config-fix and code-review tasks with authoring docs`

---

### Task 11: Live end-to-end verification (verifier subagent, no fixes)

**Files:** none; produces `docs/superpowers/evidence/2026-09-22-plan2-live-verification.md`.

- [ ] **Step 1:** `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal`, `make cross`.
- [ ] **Step 2:** `ocbench run core repo-investigation --repeat 2 --model opencode-go/deepseek-v4.1-flash --variant low` (real OpenCode): verify two run rows with `repeat_index` 0/1, `events.jsonl` non-empty and parseable, `session.json` present, validators recorded, metrics plausible (tokens > 0, `steps` > 0), worktree removed.
- [ ] **Step 3:** `ocbench run core py-bugfix --dry-run`: verify no session started, `plan.json` present, worktree removed (unless kept), profile hash unchanged from the snapshot taken in Step 2.
- [ ] **Step 4:** Verify the raw event stream round-trips: `ocbench`'s stored `events.jsonl` lines all parse with `jq -e .`; count lines and compare to `result.json` step counts.
- [ ] **Step 5:** Adversarial checks: kill the run mid-flight (`timeout 5 ocbench run …` or SIGINT) and verify no orphan `opencode` process remains (`pgrep -f "opencode run"` empty) and the run row is `error`/`timeout` rather than absent; corrupt one line of an `events.jsonl` and re-run `history`-level parsing (if exposed) or assert the normaliser counts a parse error without crashing (unit-level if no CLI path).
- [ ] **Step 6:** Report PASS/FAIL per check with exact commands and observed output; do not fix anything.

---

## Self-Review Record

- Spec coverage: suites/tasks (§7 → T1, T2, T10), runner/worktrees/sandbox/artifacts (§8 → T3, T4, T7, T8), metrics (§9 → T5), validators (§7 → T6), command surface (§10 → T9), verification (§11 → T11). JSONL envelope contract is pinned by the probe and T5's golden fixture. History/compare/serve are Plan 3/4.
- Review Focus tests: determinism (T3 Step 1, T11 Step 2), process hygiene (T7 Steps 2–3, T11 Step 5), sandbox (T4 Step 1, T9 Step 1), parser resilience (T5 Step 1, T11 Step 4–5), no host coupling (every test's temp-dir rule; T11 is the only live step).
- No placeholders: every task names exact files, signatures, commands and expected results; tricky logic (deterministic commit, process-group kill, tailer, metric mapping) is specified with exact mechanics and test expectations.
