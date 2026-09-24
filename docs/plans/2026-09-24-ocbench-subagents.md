# Subagents and Session Capture Implementation Plan

> **Historical record.** This plan was executed under a workflow that is no longer in use, so the checkboxes below are not a live tracker — see `docs/roadmap.md` for outstanding work. To execute a plan like this one: work through the tasks in order, write the failing test first, run the gates each task names, and commit each task separately.

**Goal:** Capture delegated child sessions, roll their tokens and cost up per agent, and render a run as a timeline with nested subagent spans.

**Architecture:** `internal/session` parses OpenCode's session export and discovers child session ids from `task` tool metadata. The runner exports children after the primary session and writes them under the run directory; metrics gain the reserved `agent.<name>.<metric>` names plus `subagent_*` roll-ups and a cross-check delta. `internal/trace` builds the timeline from stored events and sessions, and `ocbench trace <run-id>` renders it.

**Tech Stack:** Go stdlib only (existing deps unchanged), modernc SQLite, Cobra.

**Spec:** `docs/design.md` §13 (Subagents and sessions), plus the §8 pipeline step, §9 metric names and §10 command surface.

## Global Constraints

- No new dependencies; no network in tests; temp dirs and injected adapters only.
- Child capture must never fail a run: a failed child export increments `subagent_export_failures` and the run keeps what it captured.
- Traversal stops at depth 5 and visits each session id once.
- Session files live under `runs/<run-id>/sessions/<child-session-id>.json`, are never written into a worktree, and are never served by the dashboard.
- Metrics are derived, never authoritative state: a parser fix must be able to recompute them from the stored session files.
- `ocbench trace` must work for runs recorded before this change (no child sessions to show).

## Review Focus

- A delegation must change the recorded numbers: `subagent_tokens_total`/`subagent_cost` appear and `tokens_total` stays the primary session's own total (so the two are not double-counted).
- A missing or failing child export must not lose the parent's result or fail the run.
- Depth/visited guards must survive a self-referential or repeated `task` metadata.
- The trace must nest a child under the `task` call that produced it, not append it at the end.
- Fixtures are the real probe exports captured from OpenCode 1.18.32; parsing must not depend on fields that are absent for non-delegating runs.

---

### Task 1: Session export parser

**Files:**
- Create: `internal/session/session.go`, `internal/session/session_test.go`
- Create: `internal/session/testdata/{parent-session.json,child-session.json,events.jsonl}` — copy verbatim from `~/.local/share/ocbench-bench/probe-fixtures/` (real OpenCode 1.18.32 exports; do not edit them)

**Interfaces (Produces):**
```go
type Tokens struct { Input, Output, Reasoning, CacheRead, CacheWrite, Total int64 }
type ToolCall struct { Name, Title, Status string; DurationMS int64 }
type Message struct { Agent string; Cost float64; Tokens Tokens; Tools []ToolCall }
type Session struct { ID, Agent string; Cost float64; Tokens Tokens; Messages []Message }
type AgentRollup struct { Agent string; Messages, ToolCalls, ToolCallsFailed int; Cost float64; Tokens Tokens }
type ChildRef struct { SessionID, ParentSessionID, Description string }

func ParseExport(data []byte) (*Session, error)
func Rollup(sessions []*Session) []AgentRollup          // sorted by agent
func DiscoverChildren(events []evaluation.Event) []ChildRef // from task tool metadata, deduped, input order
```
- `ParseExport` reads `{"info":{…},"messages":[…]}`. `info.tokens` is an object with `input/output/reasoning/cache{read,write}` and may omit `total`; when `total` is absent compute `input+output+reasoning+cache.read` (this is what the probe shows). Missing `agent`/`cost`/`tokens` decode to zero values, never an error.
- `Message.Tools` comes from parts whose `type == "tool"`, using `tool`, `state.title`, `state.status`, and `state.time.start/end` for the duration.
- `DiscoverChildren` reads `part.tool == "task"` and `part.state.metadata.{sessionId,parentSessionId}` plus `state.title` as the description; it skips parts with an empty session id and de-duplicates by session id.

- [ ] **Step 1: Failing tests** over the real fixtures: `ParseExport(parent)` yields `Agent == "build"` and the parent's totals (25175 tokens, 0.002078364 cost ±1e-9); `ParseExport(child)` yields `Agent == "explore"` and 5653 tokens / 0.000511764; `Rollup` of both returns two entries sorted by agent with the right per-agent totals; `DiscoverChildren` over the fixture events returns exactly one ref with `SessionID == "ses_f2bc3c5a4ffe0WMXC38zE4mJNF"` and `ParentSessionID == "ses_f2bc3d92affeCnLLfkkU6BkZso"`; a synthetic session missing `tokens.total` computes the sum; a session with no `messages` parses without error.
- [ ] **Step 2: RED.** `go test ./internal/session -count=1`.
- [ ] **Step 3: Implement** the types and functions above; keep decoding tolerant (unknown fields ignored).
- [ ] **Step 4: GREEN + commit.** `go test ./internal/session -count=1`, then `git commit -m "feat: parse session exports and discover child sessions"`.

### Task 2: Child session capture in the runner

**Files:**
- Modify: `internal/runner/runner.go`, `internal/runner/artifacts.go`, `internal/runner/runner_test.go`

**Interfaces (Produces):**
```go
type SessionCapture struct { Primary *session.Session; Children []*session.Session; Failures int }
func captureSessions(ctx context.Context, a opencode.Adapter, runDir, primarySessionID string, events []evaluation.Event) SessionCapture
```
- Runs after the primary `Export` succeeds and after `events.jsonl` is written. It re-reads the stored events (so it never depends on in-memory state), discovers children, and exports each with `a.Export(ctx, childID)`, writing `runs/<id>/sessions/<childID>.json`.
- Recursive with a depth limit of 5 and a visited set. A child that is already captured is not re-exported. Any export or write error increments `Failures` and is otherwise ignored — the run continues.
- The primary session's own export stays where it is (`session.json`); `captureSessions` receives it parsed, or parses it from disk when the primary export is missing.

- [ ] **Step 1: Failing tests** with a scripted adapter whose `Export` returns the fixture exports and fails for one child: a run with one `task` event writes `sessions/<child>.json`, records no failures, and leaves `session.json` intact; an adapter whose child export errors still completes the run, writes `result.json`, and reports `Failures == 1`; a `task` event whose `metadata.sessionId` equals the primary session id is skipped (no self-export); an event stream with a `task` part lacking `metadata` is ignored.
- [ ] **Step 2: RED.** `go test ./internal/runner -run TestCaptureSessions -count=1`.
- [ ] **Step 3: Implement** `captureSessions` and call it from `Run` after the events artifact is written, before validators.
- [ ] **Step 4: GREEN + commit.** `go test ./internal/runner -count=1`, then `git commit -m "feat: capture delegated child sessions"`.

### Task 3: Per-agent metrics and the session cross-check

**Files:**
- Modify: `internal/runner/artifacts.go`, `internal/runner/runner.go`, `internal/runner/runner_test.go`

**Interfaces (Produces):** `derivedMetrics` gains the session roll-up:
```go
func sessionMetrics(c SessionCapture) map[string]float64
```
- Emits, for every captured session including the primary:
  `agent.<name>.messages`, `agent.<name>.cost`, `agent.<name>.tokens_input|output|reasoning|cache_read|cache_write|total`, `agent.<name>.tool_calls`, `agent.<name>.tool_calls_failed`.
- Emits `subagent_sessions`, `subagent_tokens_total`, `subagent_cost` for the children only, and `subagent_export_failures`.
- Emits `session_crosscheck_tokens_delta` = `abs(event_tokens_total - primary_session_tokens_total)`, where `event_tokens_total` is the value already computed by `evaluation.Metrics`; when the primary session is unavailable the delta is omitted rather than written as zero.
- Agent names are sanitised with the existing `evaluation` sanitizer so a name cannot produce an invalid metric key; a name that sanitises to empty falls back to `unknown`.

- [ ] **Step 1: Failing tests**: a run with the fixture parent and child yields `subagent_sessions=1`, `subagent_tokens_total=5653`, `subagent_cost≈0.000511764`, `agent.explore.tokens_total=5653`, `agent.build.tokens_total=25175`, and `tokens_total` unchanged at the event-derived value; a run with no children emits `subagent_sessions=0` and no `agent.` keys; a failed child export emits `subagent_export_failures=1` and no `subagent_tokens_total`; the cross-check delta equals the difference between the event total and the primary session total.
- [ ] **Step 2: RED.** `go test ./internal/runner -run 'TestSessionMetrics' -count=1`.
- [ ] **Step 3: Implement** `sessionMetrics` and merge it into the metrics map written by `derivedMetrics`.
- [ ] **Step 4: GREEN + commit.** `go test ./internal/runner -count=1`, then `git commit -m "feat: roll up per-agent usage and cross-check sessions"`.

### Task 4: `ocbench trace`

**Files:**
- Create: `internal/trace/trace.go`, `internal/trace/trace_test.go`, `internal/cli/trace.go`, `internal/cli/trace_test.go`
- Modify: `internal/cli/root.go`

**Interfaces (Produces):**
```go
type ToolSpan struct { Tool, Title, Status string; DurationMS int64; Child *SubagentSpan }
type Step struct { Index int; Tokens session.Tokens; Cost float64; Retries, Compactions int; Tools []ToolSpan }
type SubagentSpan struct { SessionID, Agent string; Tokens session.Tokens; Cost float64; Tools []session.ToolCall }
type Trace struct { RunID, TaskID string; Steps []Step; Subagents []SubagentSpan }
func Build(runID, taskID string, events []evaluation.Event, sessions map[string]*session.Session) Trace
```
- `Build` walks events in order: `step_start` opens a step, `tool_use` appends a `ToolSpan` (attaching a `Child` when the tool is `task` and its `metadata.sessionId` is in `sessions`), `step_finish` closes the step with its tokens and cost. `retry`/`compaction` parts increment the current step's counters (a part before the first `step_start` counts on a synthetic step 0). A `task` whose child session was not captured still renders, with a `Child` whose tokens/cost are zero and `SessionID` set.
- CLI: `ocbench trace <run-id> [--json]` resolves the run via `store.GetRun`, reads `events.jsonl` and `sessions/*.json` from `artifacts_dir`, and renders. Missing run → usage error (exit 2), matching `compare`. Missing events file → infrastructure error (exit 1) naming the path. Human output indents subagent spans under their `task` call and prints per-step tokens and duration.

- [ ] **Step 1: Failing tests** for `Build` over the fixture events plus the fixture child session: two `task`-free steps and one step containing the `task` call; the child span attaches to that tool span with agent `explore` and 5653 tokens; a `task` without a captured child still yields a span with an empty agent; retries/compactions increment the step they fall in. CLI tests: `trace <id> --json` has stable keys and an empty `subagents` array when no children; unknown id → usage error.
- [ ] **Step 2: RED.** `go test ./internal/trace ./internal/cli -run TestTrace -count=1`.
- [ ] **Step 3: Implement** `Build`, the CLI command, the human/JSON renderers, and register the command in `root.go`.
- [ ] **Step 4: GREEN + commit.** `go test ./... -count=1`, then `git commit -m "feat: add run trace with subagent spans"`.

### Task 5: Verification

**Files:** none (evidence only).

- [ ] **Step 1: Gates.** `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web`, `make cross`.
- [ ] **Step 2: Live delegation smoke** with a throwaway suite under a temp dir (so the prompt can require delegation): one task whose prompt says to use the `task` tool with `subagent_type: explore` and whose validator checks the answer mentions a file from the fixture. Run it with the isolated OpenCode data dir, then assert from the DB that `subagent_sessions >= 1`, `subagent_tokens_total > 0`, `agent.explore.tokens_total > 0`, and that `runs/<id>/sessions/` holds at least one child export.
- [ ] **Step 3: Trace smoke**: `ocbench trace <run-id>` on that run shows the child nested under the `task` call, and `--json` parses with `jq -e`.
- [ ] **Step 4: Cross-check sanity**: report the recorded `session_crosscheck_tokens_delta` for that run and state whether the event-derived and session-derived totals agree.
- [ ] **Step 5: Record evidence** under `docs/evidence/` with a PASS/FAIL table and exact commands.
