# Plan 6 (Subagents and Session Capture) — Verification

**Overall status: PASS.** A live run that genuinely delegated recorded the
child session, rolled its usage up per agent, and rendered it as a nested span;
the event-derived and session-derived token totals agree exactly.

- **Date:** 2026-09-24
- **Repo:** `/Users/mich/dev/mbl-ocbench`, worktree `.worktrees/plan-6-subagents`
- **Branch:** `plan-6-subagents`
- **Commit:** `12dda48` (`fix: cover an empty event stream in trace`)
- **Binary under test:** built from this worktree with the Makefile LDFLAGS
- **Model:** `opencode-go/deepseek-v4.1-flash`, variant `low`

## 1. Isolation

OpenCode ran through `~/.local/bin/ocbench-opencode`, which points it at the
isolated data dir `~/.local/share/ocbench-bench`; `~/.local/share/opencode` was
never touched. The run used an isolated `OCBENCH_HOME` inside the worktree.

## 2. Static gates

| Gate | Command | Result |
|---|---|---|
| Unit tests | `go test ./... -count=1` | **PASS** (exit 0) |
| Vet | `go vet ./...` | **PASS** (exit 0, no output) |
| Format | `gofmt -l cmd internal web` | **PASS** (no output) |
| Cross-build | `make cross` | **PASS** (linux amd64 + arm64) |

## 3. Live delegation smoke

A throwaway suite (passed with `--suite-dir`) held one task whose fixture
contained `alpha.txt` and `beta.txt` and whose prompt required delegating the
listing to the `explore` subagent, with an `answer` validator (`mode: all`)
requiring both file names.

```sh
ocbench run deleg deleg-smoke --suite-dir <scratch> \
  --model opencode-go/deepseek-v4.1-flash --variant low
```

Exit 0, run `e89f1805-c153-4d99-9a23-f96993447948`, status `passed`. The model
delegated on the first attempt — no retry was needed.

Recorded metrics (from the isolated SQLite database):

| Metric | Value |
|---|---|
| `subagent_sessions` | 1 |
| `subagent_tokens_total` | 9175 |
| `subagent_cost` | 0.000652296 |
| `subagent_export_failures` | 0 |
| `agent.explore.tokens_total` | 9175 |
| `agent.explore.messages` | 4 |
| `agent.explore.tool_calls` | 2 |
| `agent.build.tokens_total` | 25242 |
| `agent.build.messages` | 3 |
| `session_crosscheck_tokens_delta` | **0.0** |

The child session file was written to
`runs/e89f1805-c153-4d99-9a23-f96993447948/sessions/ses_f2b9e2a71ffeBXnydVL73Ow16T.json`.

The cross-check delta of `0.0` is the headline result: the event-derived token
total and the primary session export's own total agree exactly, so the
cross-check the spec promised is now a recorded number rather than an
assumption. The delegated child's 9175 tokens and $0.000652 are additional to
the parent's 25242 — before this plan they were invisible, which is precisely
the hole the review identified.

## 4. Trace

```sh
ocbench trace e89f1805-c153-4d99-9a23-f96993447948
```

```
run e89f1805-c153-4d99-9a23-f96993447948  task deleg-smoke
step 0  12549 tokens  7.97s
  task  List directory files  completed  7.23s
    explore  ses_f2b9e2a71ffeBXnydVL73Ow16T  9175 tokens  cost 0.000652296
step 1  12693 tokens  540ms
```

The subagent span is nested under the `task` call that produced it, with the
agent name, child session id, tokens and cost.

- `ocbench trace <id> --json` parses with `jq -e`: 2 steps and a non-empty
  `subagents` array containing `explore` / `ses_f2b9e2a71ffeBXnydVL73Ow16T` / 9175 tokens.
- `ocbench trace no-such-run-id` exits 2 (usage error).

## 5. Cleanup and limitations

- `pgrep -f "opencode run"` is empty after the run; no child processes remain.
- All scratch data lived under the worktree and was removed afterwards.
- One live attempt, one delegation: enough to prove discovery, capture, roll-up
  and trace nesting, not enough to characterise nested (grandchild) delegation
  or the no-delegation fallback, which remain unit-tested only.
- The smoke task's prompt explicitly requested delegation; a task that
  delegates spontaneously was not observed.
