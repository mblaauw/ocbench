# Agentic suite — verification

**Overall status: PASS.** The task-infrastructure work (metadata, hidden tests,
new validators, weights, process metrics) is exercised end to end, every task
fails on its untouched fixture and passes with its reference, and a live run of
`delegation-sweep` shows the agent delegating and the harness recording it.

- **Date:** 2026-09-24
- **Branch:** `main`
- **Commits under test:** `6472c50` (process metrics) through `fb2f025` (agentic suite)
- **Model:** `opencode-go/deepseek-v4.1-flash`, variant `low`

## 1. Static gates

| Gate | Result |
|---|---|
| `go test ./... -count=1` | **PASS** (17 packages) |
| `go vet ./...` | **PASS** |
| `gofmt -l cmd internal web` | **PASS** (no output) |
| `make cross` | **PASS** (linux amd64 + arm64) |

## 2. Task honesty

`TestEveryTaskFailsUntouchedAndPassesWithItsReference` runs every task of the
core and agentic suites through the real validator engine: each must fail on its
untouched fixture and pass once `evaluator/reference` is applied.

```
--- PASS: TestEveryTaskFailsUntouchedAndPassesWithItsReference (0.60s)
    --- PASS: .../core/code-review          --- PASS: .../agentic/buried-fact
    --- PASS: .../core/config-yaml-fix      --- PASS: .../agentic/delegation-sweep
    --- PASS: .../core/multi-file-feature   --- PASS: .../agentic/restraint-test-edit
    --- PASS: .../core/py-bugfix            --- PASS: .../agentic/rules-compliance
    --- PASS: .../core/repo-investigation
```

Process validators are excluded from the harness — they observe agent behaviour,
which no reference tree can supply — so `delegation-sweep`'s delegation claim is
proven by the live run below instead.

## 3. Live delegation smoke

```sh
export OCBENCH_HOME=$PWD/.verify-agentic/home
ocbench run agentic delegation-sweep --suite-dir suites/agentic \
  --model opencode-go/deepseek-v4.1-flash --variant low
```

```
1 delegation-sweep  PASS  29.203s  24434 tokens  3 tools
1/1 successful
```

The task asks for one subagent per package; the model used three.

Validators:

```
seq  kind     name                        status
1    process  delegated to a subagent     passed
2    answer   all three retention values  passed
```

Metrics from the isolated database:

| Metric | Value |
|---|---|
| `subagent_sessions` | 3 |
| `subagent_calls` | 3 |
| `subagent_tokens_total` | 37606 |
| `subagent_cost` | 0.003984558 |
| `agent.build.tokens_total` | 24434 |
| `agent.explore.tokens_total` | 37606 |
| `score` | 1.0 |
| `success` | 1 |
| child session files captured | 3 |

Two things this demonstrates beyond "the task passed":

- **Delegation is now measurable.** The `process` validator confirms a `task`
  tool call happened because the prompt asked for one, and `subagent_sessions`
  counts what it cost.
- **The delegation dominated the run.** The three subagents spent 37,606 tokens
  against the parent's 24,434 — before slice C that work was entirely invisible,
  which is exactly the measurement hole that made agentic profiles
  incomparable.

## 4. Findings from writing the suite

Three real defects were found and fixed while authoring these tasks; they are
worth recording because each would have silently degraded every run:

1. **Hidden tests landed at the worktree root** instead of under `tests/`,
   because the copy walked the subtree without restoring its directory name. A
   task whose validator runs `unittest discover -s tests` would have failed
   every time. The runner now writes to `<worktree>/tests/`, and the spec says
   so explicitly.
2. **Stale bytecode across validator phases.** `x + factor` and `x * factor` are
   the same length, so Python reused the `.pyc` written before the reference was
   applied and the "fixed" task still failed. Runs now set
   `PYTHONDONTWRITEBYTECODE=1`, and the honesty harness mirrors it.
3. **A grep validator read a binary artefact.** A tab inside a `.pyc` matched an
   "absent tabs" rule. The grep scan now skips files that look binary (a NUL
   byte in the first 8 KiB), the same heuristic git uses.

## 5. Limitations

- One live attempt, one task. `delegation-sweep` is the only task whose central
  claim needs a live run; the other eight are proven by the honesty harness.
- The agentic suite is `tier: standard` by declaration, not by measurement: its
  pass rate has not been characterised across models, so "a strong model fails
  it 30–70% of the time" is not yet true of any task here.
- `restraint-test-edit`, `rules-compliance` and `buried-fact` have not been run
  against a live model at all.
