# Plan 2 / Task 11 — Live End-to-End Verification

**Overall status: PASS-after-remediation** — the original `5ee3a25` verification
below FAILED the adversarial cancellation check: SIGINT terminated `ocbench`
abruptly, **orphaned the in-flight `opencode run` child** and **persisted no run
row**. A remediation rerun at `ea921ca` (section 11) now satisfies every
cancellation invariant: prompt cancellation-style exit, no orphaned child, exactly
one persisted `error` run row, `result.json` present, worktree removed, and
validators skipped. The original incident and its evidence are retained verbatim
below; only the top-level status changed. No source was modified and nothing was
committed.

**Remediation rerun:** commit `ea921ca` — see section 11 (PASS).

- **Date:** 2026-09-22
- **Repo:** `/Users/mich/dev/mbl-ocbench`
- **Branch:** `plan-2-runner`
- **Commit:** `5ee3a25` (`fix: bind code-review answer patterns to defects and corrections`)
- **Working tree:** clean before and after (only this evidence file added)
- **Verifier:** verifier subagent; read-only on source, no commits
- **Binary under test:** built from `5ee3a25` with the Makefile `LDFLAGS` into the
  isolated temp root (not into `bin/`); `ocbench version` → `ocbench 5ee3a25 (5ee3a25)`
- **Real OpenCode:** `/Users/mich/.opencode/bin/opencode` 1.18.32
- **Live model:** `opencode-go/deepseek-v4.1-flash`, variant `low`

---

## 1. Environment isolation

All runtime data (DB, profiles, runs, cache) was kept in one disposable temp root;
the developer `HOME` / OpenCode config and auth were left untouched and never written.

```
TMPBASE=/var/folders/9r/1qfn68ds57jd6hcmhn32j47c0000gn/T/opencode
T11=$TMPBASE/t11/verify.tvKBNr
OCBENCH_HOME=$T11/home      # DB, profiles/, runs/, suites/
XDG_CACHE_HOME=$T11/cache   # ocbench fixture cache + OpenCode package cache
HOME=/Users/mich            # unchanged: OpenCode config/auth discovery only
XDG_CONFIG_HOME=<unset>     # unchanged; no ocbench config.yaml exists -> defaults
XDG_DATA_HOME=<unset>       # unchanged: real OpenCode session/auth data
```

`config.Resolve` maps `OCBENCH_HOME` to `Home/DB/Profiles/Runs/Suites` and
`XDG_CACHE_HOME` to `Cache`; the sandbox child env allowlist (`runner.BuildEnv`)
forwards only `HOME`, `PATH`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `TEMP`, `TMP`,
`LANG`, `TERM`, `TZ` plus always-set overrides, so the isolated cache/DB paths do
not leak into the model child while OpenCode still finds its auth under the real `HOME`.

Binary build:

```sh
cd /Users/mich/dev/mbl-ocbench
LDFLAGS="-s -w -X mbl/ocbench/internal/version.Version=$(git describe --tags --always --dirty) \
  -X mbl/ocbench/internal/version.Commit=$(git rev-parse --short HEAD) \
  -X mbl/ocbench/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
go build -ldflags "$LDFLAGS" -o "$T11/bin/ocbench" ./cmd/ocbench
# -> build-ok ; ocbench version -> ocbench 5ee3a25 (5ee3a25)
```

---

## 2. Step 1 — static gates

| Gate | Command | Result |
|---|---|---|
| Unit tests | `go test ./... -count=1` | **PASS** (exit 0; all 11 packages `ok`, `cmd/ocbench`/`suites` no test files) |
| Vet | `go vet ./...` | **PASS** (exit 0, no output) |
| Format | `gofmt -l cmd internal` | **PASS** (no output, exit 0) |
| Cross-build | `make cross` | **PASS** (exit 0; `dist/ocbench-linux-amd64`, `dist/ocbench-linux-arm64`) |

`go test ./... -count=1` output:

```
?   	mbl/ocbench/cmd/ocbench	[no test files]
ok  	mbl/ocbench/internal/canon	0.287s
ok  	mbl/ocbench/internal/cli	6.782s
ok  	mbl/ocbench/internal/config	1.126s
ok  	mbl/ocbench/internal/doctor	0.760s
ok  	mbl/ocbench/internal/evaluation	1.529s
ok  	mbl/ocbench/internal/opencode	1.984s
ok  	mbl/ocbench/internal/profile	2.159s
ok  	mbl/ocbench/internal/runner	8.696s
ok  	mbl/ocbench/internal/store	2.025s
ok  	mbl/ocbench/internal/suite	1.828s
ok  	mbl/ocbench/internal/version	1.514s
?   	mbl/ocbench/suites	[no test files]
```

`make cross` writes only the git-ignored `dist/` directory (`.gitignore` contains
`/dist/`); `git status` remained clean.

---

## 3. Step 2 — live repeat run (`repo-investigation --repeat 2`)

Command (exactly as required):

```sh
cd /Users/mich/dev/mbl-ocbench
export OCBENCH_HOME="$T11/home" XDG_CACHE_HOME="$T11/cache"
"$T11/bin/ocbench" run core repo-investigation --repeat 2 \
  --model opencode-go/deepseek-v4.1-flash --variant low
```

Observed (exit 0, elapsed ~38 s):

```
1 repo-investigation  PASS  8.413s  38874 tokens  3 tools
2 repo-investigation  PASS  23.058s  64926 tokens  5 tools
2/2 successful

task repo-investigation: 2/2 successful (100%)
  tokens_total  median 51900  min 38874  max 64926
  tool_calls    median 4  min 3  max 5
  duration      median 15.736s
```

DB rows (`runs`), both `repeat_index` values present and distinct:

```
id        ri  task_id             status  dr  exit_code  session_id                      profile_hash
1506ff50  0   repo-investigation  passed  0   0          ses_f35441526ffeuIvXqxLAqWz7EY  18dfe897e54b084ce5ac19e6d1fff768d625e0acd87206892dff33fd17b0c56f
16119614  1   repo-investigation  passed  0   0          ses_f3543f44cffeICJu2qFzAYmPFy  18dfe897e54b084ce5ac19e6d1fff768d625e0acd87206892dff33fd17b0c56f
```

Artifacts per run (run dirs `$T11/home/runs/<id>/`):

| Artifact | Run 0 (`1506ff50…`) | Run 1 (`16119614…`) |
|---|---|---|
| `events.jsonl` | 8619 bytes, 10 lines | 12093 bytes, 16 lines |
| `session.json` | 16660 bytes | 23499 bytes |
| `result.json` | present | present |
| `validation/1-defect_identified.log` | present (answer validator) | present (answer validator) |
| `worktree/` | **absent** | **absent** |

Metrics (from `run_metrics` / `result.json`):

| run | `tokens_total` | `steps` | `tool_calls_total` | `parse_errors` |
|---|---|---|---|---|
| 1506ff50 | 38874 (>0) | 3 (>0) | 3 | 0 |
| 16119614 | 64926 (>0) | 5 (>0) | 5 | 0 |

Validators recorded (both runs):

```
run       seq  kind    name               status  output_path
1506ff50  1    answer  defect identified  passed  validation/1-defect_identified.log
16119614  1    answer  defect identified  passed  validation/1-defect_identified.log
```

Worktree cleanup verified two ways: no `worktree/` sub-directory under either run
dir, and `git worktree list` on the fixture baseline repo shows only the main
checkout (no leftover detached worktrees from these runs).

**Step 2 result: PASS** (repeat indexes 0/1, non-empty parseable events, `session.json`,
validators, `tokens_total>0`, `steps>0`, worktree removed).

---

## 4. Step 3 — dry run (`py-bugfix --dry-run`)

Command (exactly as required):

```sh
"$T11/bin/ocbench" run core py-bugfix --dry-run
```

Observed (exit 0):

```
1 py-bugfix  DRY  83ms  0 tokens  0 tools
0/1 successful
```

Run row `073c933b-…`, `status=dry_run`, `dry_run=1`, `session_id` empty. Its run
dir contained **only `plan.json`** — no `events.jsonl`, no `session.json`, no
`worktree/` (the plan's `worktree` path does not exist on disk). `plan.json`:

```json
{"run_id":"073c933b-777f-451a-81e4-1a44760e98c9",
 "profile_hash":"5b045629048fa7e582ca9839a1b4bad111aad3449f54bf9d96308fc9e76df238",
 "worktree":"…/runs/073c933b-777f-451a-81e4-1a44760e98c9/worktree",
 "validators":[{"kind":"command","name":"unit tests",
   "command":["python3","-m","unittest","discover","-s","tests"]}]}
```

**No session started: PASS. `plan.json` present: PASS. Worktree removed: PASS.**

### 4.1 Profile-hash discrepancy (expected; not an implementation failure)

The brief's Step 3 asks for the profile hash to be *unchanged from the Step 2
snapshot*. It is **not equal**:

- Step 2 live runs: `18dfe897e54b084ce5ac19e6d1fff768d625e0acd87206892dff33fd17b0c56f`
- Step 3 literal dry run: `5b045629048fa7e582ca9839a1b4bad111aad3449f54bf9d96308fc9e76df238`

This is **expected**, because the two mandated commands differ in `--model`/`--variant`,
and those run-time selections are part of the profile fingerprint. Spec §5.1 puts
them in the hashed structure (`primary.model`, and
`environment.overrides.{agent,model,variant}`), and `profile.Fingerprint`'s
`buildPrimary` applies `opts.Model`/`opts.Variant` before hashing. Isolating the cause:

| Invocation | model | variant | profile_hash |
|---|---|---|---|
| Step 2 live (both repeats) | `opencode-go/deepseek-v4.1-flash` | `low` | `18dfe897…` |
| Step 3 literal dry run (`py-bugfix --dry-run`) | (config default) | (none) | `5b045629…` |
| Diagnostic dry run `py-bugfix --dry-run --model opencode-go/deepseek-v4.1-flash --variant low` | `opencode-go/deepseek-v4.1-flash` | `low` | `18dfe897…` (**matches live**) |
| Step 3 literal dry run repeated (determinism) | (config default) | (none) | `5b045629…` (identical) |

So: an exact dry run **with matching overrides reproduced the live profile hash**,
while the literal required dry run **deterministically produced a different one**.
This is a command/brief mismatch, **not** an implementation failure. (The `profiles`
table correctly holds two immutable rows, one per distinct resolved profile.)

---

## 5. Step 4 — raw event stream round-trip

```sh
for d in "$T11"/home/runs/*/; do
  while IFS= read -r line; do printf '%s' "$line" | jq -e . >/dev/null || echo BAD; done < "$d/events.jsonl"
  jq -e . "$d/events.jsonl" >/dev/null; echo "$(basename $d): jq-exit=$?"
done
```

| Run | lines | unparseable lines | `step_start` events | `result.json` `metrics.steps` | whole-file `jq -e` |
|---|---|---|---|---|---|
| 1506ff50 | 10 | 0 | 3 | 3 | exit 0 |
| 16119614 | 16 | 0 | 5 | 5 | exit 0 |

Every stored `events.jsonl` line parses individually with `jq -e .`; line counts and
the `step_start` count equal the persisted `steps` metric. Envelope type histogram:
run 0 = 3 `step_start`, 3 `step_finish`, 3 `tool_use`, 1 `text`; run 1 = 5/5/5/1.

**Step 4 result: PASS.**

---

## 6. Step 5 — adversarial checks

### 6.1 Cancellation / process hygiene — **FAIL**

macOS has no GNU `timeout`, so the run was started and then signalled with SIGINT.
Two harness pitfalls were corrected before the result was trusted:

1. A first attempt started `ocbench … &` in the non-interactive shell. POSIX async
   jobs inherit `SIGINT` as `SIG_IGN`, and Go preserves an inherited ignored signal
   (no `signal.Notify` anywhere in the app), so `kill -INT` was a no-op: the run
   completed normally (`45afeab5`, `passed`, 8.7 s). This attempt is **invalid as a
   cancellation test** and is recorded only as evidence of the harness issue.
   Verified disposition: foreground Python `SIGINT` → `default_int_handler`;
   background `&` → `1` (`SIG_IGN`).
2. Attempt 2 sent SIGINT at ~5 s, but that landed during profile discovery (child was
   `opencode debug agent quick`); `ocbench` died, the discovery child was reparented to
   PID 1, and again no row was written. This showed the abrupt-death behaviour but did
   not exercise an in-flight `opencode run`.
3. Attempt 3 launched `ocbench` via an `exec` wrapper that reset SIGINT to default,
   polled until the actual `opencode run` child appeared (after ~8 s), then sent SIGINT.

Attempt 3 transcript (abridged, paths shortened):

```
ocbench_pid=2330
opencode run child pid=[2467] after 8s
  PID  PPID  PGID COMMAND
 2467  2330  2467 opencode run --format json --dir …/runs/b0fa92d5-…/worktree \
                         --model opencode-go/deepseek-v4.1-flash --variant low --auto -- # Identify the defective function…
sent SIGINT to ocbench 2330 at 22:12:43
ocbench exited
ocbench wait-rc=130
--- orphan check for opencode run child ---
ORPHAN ALIVE
  PID  PPID  PGID COMMAND
 2467     1  2467 opencode run --format json --dir …/runs/b0fa92d5-…/worktree …
pgrep -f 'opencode run' after SIGINT:
2467 opencode run --format json --dir …/runs/b0fa92d5-…/worktree …
runs-after=6   # runs-before=6
```

Findings:

- **`pgrep -f "opencode run"` was NOT empty**: the child (PID 2467) survived, reparented
  to PID 1. `ocbench` exited via SIGINT (`wait-rc=130`). → **orphan check FAIL**.
- **No run row was persisted**: `runs` count stayed 6→6; the cancelled invocation
  `b0fa92d5-…` has no row. → **“row is error/timeout rather than absent” FAIL**.
- Corroborating cleanup failure: the cancelled run left
  `runs/b0fa92d5-…/worktree/` on disk and an empty `events.jsonl`, and
  `git worktree list` on the fixture baseline repo lists it as a leftover detached
  worktree. The runner’s deferred `RemoveWorktree` never ran because the process died
  before the pipeline reached it.

Root cause: the CLI never installs a signal handler (`Execute` uses
`context.Background()`; no `os/signal`, `signal.Notify` or `NotifyContext` in
first-party code). SIGINT therefore terminates `ocbench` with the Go default action,
so the adapter’s process-group `Kill`/context-cancellation path is never reached and
the run is neither recorded nor cleaned up. The graceful context-cancellation path is
covered only by `TestRunTimeout` (PASS, below), which is unreachable from a real Ctrl-C.

Cleanup: the orphan was terminated by the verifier immediately after evidence capture
(`kill -TERM`), confirmed by `pgrep -f "opencode run"` → none.

### 6.2 Malformed-event resilience — **PASS**

The focused unit test that proves malformed JSONL increments `ParseErrors` without
crashing:

```sh
go test ./internal/evaluation -run 'TestObserveMalformedLineCountsParseErrorNoPanic' -count=1 -v
```

Output:

```
=== RUN   TestObserveMalformedLineCountsParseErrorNoPanic
--- PASS: TestObserveMalformedLineCountsParseErrorNoPanic (0.00s)
PASS
ok  	mbl/ocbench/internal/evaluation	0.347s
```

The test feeds `{"type":`, `not json`, ``, `null`, `[]`, `{"type":""}` and asserts
`ParseErrors == 6` with no panic. As a cross-check of the persistence side of the
timeout path, `go test ./internal/runner -run 'TestRunTimeout|TestRunDryRunStartsNoSession' -count=1 -v`
also passes (`TestRunTimeout` 1.29 s, `TestRunDryRunStartsNoSession` 0.12 s).

---

## 7. PASS / FAIL / BLOCKED table

| # | Check | Status | Evidence |
|---|---|---|---|
| 1 | `go test ./... -count=1` | PASS | exit 0, all packages `ok` |
| 1 | `go vet ./...` | PASS | exit 0, no output |
| 1 | `gofmt -l cmd internal` | PASS | no output |
| 1 | `make cross` | PASS | exit 0, both linux binaries built |
| 2 | Two runs, `repeat_index` 0 and 1 | PASS | `runs` table |
| 2 | `events.jsonl` non-empty + parseable | PASS | 8619/12093 B; 0 bad lines |
| 2 | `session.json` present | PASS | 16660/23499 B |
| 2 | Validators recorded | PASS | 2 `answer` rows, `passed` |
| 2 | Metrics `tokens_total>0`, `steps>0` | PASS | 38874/64926; 3/5 |
| 2 | Worktree removed | PASS | no `worktree/`; `git worktree list` clean |
| 3 | Dry run starts no session | PASS | `status=dry_run`, no `session.json`/events |
| 3 | `plan.json` present | PASS | `runs/073c933b-…/plan.json` |
| 3 | Dry-run worktree removed | PASS | only `plan.json` in run dir |
| 3 | Profile hash unchanged vs Step 2 | **FAIL (brief command mismatch — expected, not an implementation failure)** | `5b045629…` vs `18dfe897…`; matching-override dry run = `18dfe897…` |
| 4 | `events.jsonl` lines parse linewise | PASS | `jq -e .`, 0 unparseable |
| 4 | line/step count consistency | PASS | `step_start` = `metrics.steps` (3,5) |
| 5 | No orphan `opencode run` after cancel | **FAIL** | PID 2467 reparented to 1; `pgrep -f "opencode run"` non-empty |
| 5 | Cancelled run persisted as `error`/`timeout` | **FAIL** | `runs` 6→6; no row for `b0fa92d5-…` |
| 5 | Malformed JSONL increments `ParseErrors`, no panic | PASS | `TestObserveMalformedLineCountsParseErrorNoPanic` |

**Counts:** PASS 18, FAIL 3, BLOCKED 0.
(FAIL 3 = Step 3 literal hash equality + Step 5 orphan + Step 5 missing row. The
Step 3 item is a brief/command inconsistency, not an implementation defect; the two
Step 5 items are a genuine implementation defect.)

---

## 8. Concrete defect

**Cancellation is not handled: SIGINT orphans the `opencode run` child and drops the
run record.** `cmd/ocbench/main.go` → `cli.Execute` uses `context.Background()` and
installs no `signal.NotifyContext`; on SIGINT the Go runtime terminates the process
(default action) before the runner can kill the child process group, remove the
worktree, or call `store.InsertRun`. Observable consequences: orphaned `opencode run`
(PID 2467, PPID 1), leftover worktree (`b0fa92d5-…/worktree`), and no DB row. The
existing `TestRunTimeout` proves the *context-timeout* path records a `timeout` row,
but nothing wires OS signals into that path, so a user’s Ctrl-C never benefits from it.

## 9. Limitations

- The isolated temp root is ephemeral; artifact paths below are for this session only.
- One live model/provider (OpenCode 1.18.32, `opencode-go/deepseek-v4.1-flash`, low
  variant) was exercised; results are provider-timing dependent but all invariants held.
- The first cancellation attempt was invalidated by shell `SIG_IGN` inheritance; only
  the corrected SIGINT attempt (with the signal reset to default) is treated as evidence.
- `make cross` rewrites the git-ignored `dist/` binaries as the brief requires; no
  tracked files changed.

## 10. Artifact paths (this session)

```
$T11 = /var/folders/9r/1qfn68ds57jd6hcmhn32j47c0000gn/T/opencode/t11/verify.tvKBNr
$T11/bin/ocbench
$T11/home/ocbench.db
$T11/home/runs/1506ff50-9a09-4232-9b8e-1ed0ee4d2281/   # step 2 repeat 0
$T11/home/runs/16119614-c74d-40c8-9b39-0b50bec09d84/   # step 2 repeat 1
$T11/home/runs/073c933b-777f-451a-81e4-1a44760e98c9/   # step 3 literal dry run
$T11/home/runs/b0fa92d5-cffc-4b71-bab6-7475b3f504ed/   # cancelled: no DB row, worktree left
```

---

## 11. Remediation rerun — live SIGINT cancellation at `ea921ca` (PASS)

Added 2026-09-22. This section re-runs only the previously failing live
cancellation probe against the remediation commits `e8fdaf4`..`ea921ca`
(signal bridging, cancellation watcher, bounded post-run persistence,
validator-skip-on-cancel). Sections 1–10 above are unchanged.

### 11.1 Environment and binary

- **Repo:** `/Users/mich/dev/mbl-ocbench`; **branch:** `plan-2-runner`
- **Commit:** `ea921ca038a29cb5e50e87bd5ac3da3c57ce991c`
- **Working tree:** clean except the untracked evidence file; no source changes, no commits
- **Temp root:** `T11=/var/folders/9r/1qfn68ds57jd6hcmhn32j47c0000gn/T/opencode/t11/verify.vT1TOl`
  - `OCBENCH_HOME=$T11/home`, `XDG_CACHE_HOME=$T11/cache` (fresh, isolated)
  - `HOME=/Users/mich` unchanged (real OpenCode auth/config discovery only)
- **Binary:** built with Makefile `LDFLAGS` into `$T11/bin/ocbench`;
  `ocbench version` → `ocbench ea921ca (ea921ca)`
- **Real OpenCode:** `/Users/mich/.opencode/bin/opencode` 1.18.32
- **Live model:** `opencode-go/deepseek-v4.1-flash`, variant `low`

### 11.2 Static gates at `ea921ca`

| Gate | Command | Result |
|---|---|---|
| Unit tests | `go test ./... -count=1` | **PASS** (exit 0; all 11 packages `ok`; `cmd/ocbench`/`suites` no test files) |
| Vet | `go vet ./...` | **PASS** (exit 0, no output) |
| Format | `gofmt -l cmd internal` | **PASS** (no output, exit 0) |
| Cross-build | `make cross` | **PASS** (exit 0; `dist/ocbench-linux-amd64`, `dist/ocbench-linux-arm64`) |

### 11.3 Signal harness (and why plain `trap - INT TERM` is not enough)

macOS ships bash 3.2. A non-interactive shell starts background (`&`) jobs with
`SIGINT`/`SIGQUIT` set to `SIG_IGN`, and POSIX/bash cannot reset a signal that was
ignored on entry with a bare `trap - INT TERM`. Verified directly: a background
`bash -c 'trap - INT TERM; exec sleep 30' &` survived `kill -INT`.

Enabling job control (`set -m`) in the launcher gives the background job its own
process group with the **default** signal disposition, after which
`trap - INT TERM; exec` is effective (re-verified: the same sleep dies on SIGINT).
The CLI is then signalled by **positive PID**, so only `ocbench` receives SIGINT
(the `opencode` child process group is not signalled directly — the fix must tear
it down).

Launcher (single attempt; no retry):

```sh
set -m
bash -c 'trap - INT TERM; exec "$0" run core repo-investigation \
  --model opencode-go/deepseek-v4.1-flash --variant low' "$T11/bin/ocbench" \
  >"$T11/live.out" 2>&1 &
ocb=$!          # ocbench PID; child observed via pgrep -P "$ocb"
```

The probe waited until the actual `opencode run` child appeared (6.5 s; profile
discovery runs first), then sent `kill -INT "$ocb"` and polled the CLI PID for at
most 30 s.

### 11.4 Observed result

```
pgrep_before=[]                       # no opencode run processes before
runs_before=NA                        # isolated DB did not exist yet
ocbench_pid=28049
opencode_run_child=28272 appeared_at=6.5s
process tree before SIGINT (t=7.1s):
  28049 28042 28049 S  .../bin/ocbench run core repo-investigation --model ... --variant low
  28272 28049 28272 R  opencode run --format json --dir .../runs/86abe111-.../worktree --model ...
sending_SIGINT_to=28049
ocbench_exit_rc=1
elapsed_after_sigint=0.25s
child_alive=NO pid=28272
pgrep_after=[]
runs_after=1
```

`live.out`:

```
error: run cancelled: context canceled
```

### 11.5 Invariants

| # | Invariant | Status | Evidence |
|---|---|---|---|
| a | Prompt cancellation-style exit (not abrupt signal death) | **PASS** | exit rc=1 in 0.25 s; `error: run cancelled: context canceled` (not 130/143) |
| b | No orphaned `opencode run` child | **PASS** | child 28272 `child_alive=NO`; `pgrep -f "opencode run"` before=[] after=[] |
| c | Exactly one persisted cancelled run row (`error`/`timeout`) | **PASS** | `runs` 0→1; row `86abe111-...` `status=error` |
| d | `result.json` present; no worktree remains | **PASS** | `result.json=PRESENT` (`status=error`); `WORKTREE_ABSENT` |
| e | Validators skipped / cancellation-consistent | **PASS** | `run_validations` empty; `result.json` `validations:[]` |

Persisted run row:

```
id        repeat_index  task_id             status  dry_run  exit_code  session_id  error
86abe111  0             repo-investigation  error   0        -1         (null)      export skipped: no session id; run cancelled
```

Run directory `$T11/home/runs/86abe111-efdd-4e88-843b-8f410e3c10cc/`:
`changed.json`, `diff.patch`, `events.jsonl`, `prompt.md`, `result.json`,
`stderr.txt`, `suite.yaml`, `task.yaml`; no `worktree/`.

### 11.6 Limitations

- One live attempt (as instructed, no retry); provider-timing dependent.
- SIGINT landed while the model was mid-run (child observed at 6.5 s), so the
  cancelled session had not yet produced a session id: `session_id` is NULL and
  `export skipped: no session id` is recorded. Persistence still succeeded with
  `status=error`, which is the invariant under test.
- `runs_before=NA` because the isolated `OCBENCH_HOME` was fresh; the single row
  after the probe is therefore unambiguously this attempt.
- `make cross` rewrites the git-ignored `dist/` binaries; no tracked files changed.
- The adaptive wait (kill as soon as the `opencode run` child appears, at/after
  ~5 s) was required because a fixed 5 s lands during profile discovery; the
  original brief's fixed `~5 s` is not sufficient to exercise an in-flight run.
