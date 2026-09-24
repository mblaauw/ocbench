# Plan 5 (Experiments and Statistics) — Verification

**Overall status: PASS.** Static gates, a live two-arm A/B experiment against the
real model, database interleaving/arm persistence, the versioned JSONL export
and the regression exit code all check out.

- **Date:** 2026-09-23
- **Repo:** `/Users/mich/dev/mbl-ocbench`, worktree `.worktrees/plan-5-experiments`
- **Branch:** `plan-5-experiments`
- **Commit:** `fb7dd4b` (`feat: add experiment list and show with JSONL export`)
- **Binary under test:** built from `fb7dd4b` with the Makefile LDFLAGS into the
  scratch dir; `ocbench version` → `ocbench fb7dd4b (fb7dd4b)`
- **Model:** `opencode-go/deepseek-v4.1-flash`, variant `low`

## 1. Isolation

The user's default OpenCode data dir is schema-incompatible with the installed
1.18.32 (it was migrated by a beta build in Aug 2026), so OpenCode runs through
`~/.local/bin/ocbench-opencode`, which points it at `~/.local/share/ocbench-bench`.
`~/.local/share/opencode` was never touched. This verification used an isolated
`OCBENCH_HOME=.verify-tmp/home` inside the worktree (the harness denied writes to
the system temp dir), so the user's real ocbench database is untouched.

## 2. Static gates

| Gate | Command | Result |
|---|---|---|
| Unit tests | `go test ./... -count=1` | **PASS** (exit 0; all 14 packages ok) |
| Vet | `go vet ./...` | **PASS** (exit 0, no output) |
| Format | `gofmt -l cmd internal web` | **PASS** (no output) |
| Cross-build | `make cross` | **PASS** (linux amd64 + arm64) |

## 3. Live A/B smoke

Two overlay files, each a minimal OpenCode config delta:

```json
{"agent":{"build":{"temperature":0.1}}}   # arm A
{"agent":{"build":{"temperature":0.9}}}   # arm B
```

```sh
export OCBENCH_HOME=$PWD/.verify-tmp/home
ocbench experiment run core repo-investigation \
  --profile A=$PWD/.verify-tmp/a.json \
  --profile B=$PWD/.verify-tmp/b.json \
  --repeat 3 --model opencode-go/deepseek-v4.1-flash --variant low
```

Observed (exit 0):

```
arm A
  executions: 3
  pass rate: 1.0000 [0.4385, 1.0000]
  pass^k: pass@k=true pass-all-k=true
  median tokens: 38792.0000
  median cost: 0.0024
  median duration: 10649.0000 ms
arm B
  executions: 3
  pass rate: 1.0000 [0.4385, 1.0000]
  pass^k: pass@k=true pass-all-k=true
  median tokens: 38879.0000
  median cost: 0.0024
  median duration: 9673.0000 ms
cost per solved task
  A: 0.0024
  B: 0.0023
regression: none detected
```

Both arms pass every repeat, so no regression is claimed — the expected result
for an easy smoke task. The experiment exists to exercise the mechanism, not to
discriminate these two overlays.

## 4. Database evidence

```sql
SELECT id, name FROM experiments;
-- 6badd918-2c8f-4fd6-be6f-90a00cfad9db | experiment core@1.1.0 2026-09-23T20:37:33Z

SELECT label, substr(profile_hash,1,12), overlay_kind, length(overlay_sha256) FROM experiment_arms;
-- A | 15a1c609915a | file | 64
-- B | 188b0c953cd3 | file | 64

SELECT a.label, r.repeat_index, r.status
FROM runs r JOIN experiment_arms a ON a.id = r.arm_id ORDER BY r.started_at;
-- A | 0 | passed
-- B | 0 | passed
-- A | 1 | passed
-- B | 1 | passed
-- A | 2 | passed
-- B | 2 | passed
```

- One experiment row; two arm rows with **distinct** `profile_hash` values
  (the overlay changes the resolved profile) and a 64-character
  `overlay_sha256` each.
- Six runs alternating **A, B, A, B, A, B** when ordered by `started_at`, i.e.
  the spec §12.2 interleaving, with `repeat_index` 0/1/2.
- 235 `run_metrics` rows across the six runs.

## 5. Versioned JSONL export

```sh
ocbench experiment show 6badd918-2c8f-4fd6-be6f-90a00cfad9db --format jsonl
```

- 6 lines, one per armed run, in `started_at` order.
- Every line parses as JSON; `schema_version == 1` on all lines.
- Arm order in the file is A, B, A, B, A, B; every `run.status` is `passed`.
- No line contains `null`; `validations` is an array on every line.

First line (truncated for width):

```json
{"schema_version":1,"experiment":{"id":"6badd918-2c8f-4fd6-be6f-90a00cfad9db","name":"experiment core@1.1.0 2026-09-23T20:37:33Z"},"arm":{"label":"A","profile_hash":"15a1c609915aa0bc0443878…","overlay_kind":"file","overlay_sha256":"…"},"run":{"id":"…","task_id":"repo-investigation","repeat_index":0,"status":"passed", …},"metrics":{…},"validations":[…]}
```

## 6. Regression exit code

`--exit-on-regression` is an `experiment run` flag (spec §10), so the gate is
exercised by the focused unit test rather than by a live failing arm — forcing a
real model to fail a task reliably is not something this harness should attempt.

```
go test ./internal/cli -run 'TestExperimentRunRegression' -count=1 -v
--- PASS
```

The test asserts exit `3` with `--exit-on-regression` and exit `0` without it,
over a scripted fake adapter.

## 7. Cleanup and limitations

- `pgrep -f "opencode run"` is empty after the run; no child processes remain.
- All runtime data for this verification lived under `.verify-tmp/` (removed
  after the run) except the shared OpenCode data dir, which is the intended
  benchmark environment.
- One live attempt, three repeats per arm, one task: enough to prove
  interleaving, arm persistence, statistics and the export, not enough to
  characterise either overlay.
- The two overlays differ only in temperature, and the task is easy, so the
  smoke cannot show a real difference; a discriminating experiment needs the
  harder suites planned for the next slice.
