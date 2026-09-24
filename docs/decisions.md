# Decisions

Every design decision that was not obvious from the design document, with the
reasoning and what it costs if the call was wrong. The design itself is
[design.md](design.md); outstanding work is [roadmap.md](roadmap.md).

## Process and layout

- **One branch per plan, merged locally.** This is a solo repository with no
  remote collaborators, so a plan executes on a branch and fast-forwards into
  `main` when its gates pass. *Cost if wrong:* none — the history is linear and
  the branch is deleted after merge.
- **Docs live at tool-agnostic paths.** `docs/design.md`, `docs/plans/`,
  `docs/evidence/`, plus this file and the roadmap. Earlier revisions used a
  workflow-specific `docs/superpowers/` tree and process headers; those were
  retired when the workflow was removed from the environment. *Cost if wrong:*
  historical records reference a process that no longer exists, which the
  evidence files note in their own footers.
- **The seven plan documents are kept as historical records**, not as a live
  tracker; their checkboxes were never updated after execution. *Cost if wrong:*
  someone reads a checkbox as current state — the historical banner at the top
  of each plan is the guard.
- **No new dependencies without a reason.** Direct dependencies are Cobra, YAML
  and a pure-Go SQLite, all vendored; there is no JavaScript build step and no
  CDN asset. *Cost if wrong:* some features are hand-rolled (statistics,
  charts) that a library would provide.

## Profile and fingerprinting

- **Paths are normalised longest-prefix-first** so a home directory or run
  directory never leaks into a hash. *Cost if wrong:* two machines with
  different home paths would fingerprint differently.
- **Permission arrays are canonicalised recursively** and MCP environment values
  are recorded as names only, so a profile hash is stable and secret-free.
  *Cost if wrong:* a permission reorder would look like a profile change, or a
  secret would leak into the database.
- **Raw skill and instruction captures are retained per spec §5.1** rather than
  hashed only. *Cost if wrong:* disk usage grows with the number of profiles.
- **The suite default timeout key is `defaults.timeout`** (the plan said
  `timeout_seconds`; the spec won) and YAML is decoded with `KnownFields(true)`,
  so a typo is a named load error rather than a silent default. *Cost if wrong:*
  a future suite key must be added to the raw structs explicitly — which is the
  point.
- **`expected_tokens` is excluded from the task hash** while `difficulty` and
  `capabilities` are included. *Cost if wrong:* retuning a token estimate does
  not break comparability (intended); changing a capability does (also
  intended).

## Suites and tasks

- **The embedded core suite is loaded with `//go:embed all:core`.** Plain
  `//go:embed` silently omits `_`-prefixed files, which dropped both fixture
  `__init__.py` files and made the embedded suite hash differently from the
  on-disk one. A test now asserts they match. *Cost if wrong:* none — `all:` is
  strictly more inclusive.
- **An explicit `--suite-dir` is terminal**: it does not fall back to the
  embedded suite. *Cost if wrong:* a typo'd directory errors instead of silently
  running the embedded suite, which is the safer failure.
- **`suite.Validator` (YAML-facing) and `evaluation.ValidatorSpec`
  (execution-facing) stay separate types**, with the runner converting. *Cost if
  wrong:* one small mapping function to remove.
- **Answer patterns are matched against the final text event only**, not the
  concatenation of every text event. Intermediate tool-progress chatter could
  otherwise satisfy a pattern the final answer never states. *Cost if wrong:* an
  exotic stream whose true answer is followed by a non-answer text event is
  graded on the later event.

## Runner and process hygiene

- **A non-zero OpenCode exit code means the invocation did not complete
  cleanly**, so the run status is `error` even when validators passed; a
  deadline still wins over it. *Cost if wrong:* a run whose edits are valid but
  whose CLI crashed is labelled `error` — recoverable from `result.json`.
- **Cancellation is authoritative over validator failure.** After validators
  return, `ctx.Err() != nil` sets the run status to `error`; validators already
  started are recorded, and later ones are recorded as skipped rather than
  executed. *Cost if wrong:* a cancellation can hide a real validator failure,
  but the cancellation is the run's actual outcome.
- **Post-run work is bounded by `max(task timeout, 5s)`** on a
  cancellation-free context. The model budget governs execution; local
  artifacts, persistence and cleanup begin after it and need their own grace.
  *Cost if wrong:* a slow local cleanup is allowed up to the task timeout, never
  unbounded.
- **The watchdog sets an atomic `fired` flag only when it actually kills**,
  rather than inferring a timeout from a failed `Stop`, so a run that finishes
  just before the deadline is not mislabelled. *Cost if wrong:* none.
- **`drainEvents` failure kills and reaps the session** before returning the
  original error, because the alternative is an orphaned model process. *Cost if
  wrong:* none; the drain error is still the one reported.
- **Fixture repositories are created with pinned `--object-format=sha1`, hooks
  disabled and fixed identity/dates**, so the baseline commit SHA is identical
  across machines. *Cost if wrong:* none for determinism; a host with a
  pre-2.29 git cannot create the repo.
- **The fixture hash guard requires exactly 64 lowercase hex characters**,
  rather than merely rejecting path separators. *Cost if wrong:* a legitimate
  non-hex cache key would be rejected — there is none.

## Metrics and statistics

- **Cost is summed even when a step reports zero tokens**, because `cost` is the
  authoritative per-step billed amount; only tokens are skipped to avoid double
  counting. *Cost if wrong:* a hypothetical duplicate zero-token step with
  non-zero cost would inflate the metric.
- **Answer text semantics**: `FinalAnswer` is the final text event while `Texts`
  keeps the whole transcript. *Cost if wrong:* see the answer-pattern decision
  above.
- **Agent-scoped metric names are reserved as `agent.<name>.<metric>`** and
  experiment statistics are derived at read time, never stored, so neither
  needs a schema migration. *Cost if wrong:* a metric rename is a read-time
  concern, not a migration.
- **Agent roll-ups that sanitise to the same key are merged**, and
  `agent_name_collisions` records that it happened. *Cost if wrong:* two
  unusually named agents share one metric key and their split is lost, though
  the totals stay correct.
- **`success` stays binary; `score` carries weighted partial credit.** *Cost if
  wrong:* consumers that read `success` only will not see partial progress.
- **Statistics are deterministic**: Wilson intervals for proportions and seeded
  permutation tests for comparisons, so the same inputs and seed always produce
  the same number. *Cost if wrong:* a permutation p-value is an approximation,
  not an exact test.

## Experiments

- **Every non-baseline arm is compared against the baseline**, and the decision
  names the worst arm. *Cost if wrong:* a large summary struct for the common
  two-arm case.
- **`Summarize` computes the comparisons and `DecideRegression` is pure.** *Cost
  if wrong:* a small refactor if a later slice needs raw samples in the summary.
- **The cost rule uses a median-difference permutation**, so the p-value and the
  25% gate read the same statistic. *Cost if wrong:* the median test is
  degenerate for a constant cost shift, which the roadmap records as debt.
- **Drift detection covers the four variables that can vary across arms**
  (OpenCode version, suite hash, task version, fixture SHA). The requested
  model, agent and variant come from one invocation and are constant by
  construction; an overlay that changes the *effective* model is the experiment
  variable itself, recorded in that arm's profile hash. *Cost if wrong:* an
  overlay-driven model change is not flagged as an attribution hazard — that
  belongs in a semantic profile diff.
- **`--exit-on-regression` lives on `experiment run`**, so the gate is evaluated
  in the same invocation that produces the data. *Cost if wrong:* an
  already-completed experiment cannot be re-gated without re-running it.

## Subagents and sessions

- **Child sessions are discovered from the `task` tool metadata**
  (`state.metadata.sessionId`) in the event stream, and exported recursively with
  a depth bound of five and a visited set. *Cost if wrong:* a future OpenCode
  version that renames the metadata field silently captures nothing — the
  `subagent_sessions` metric is the tell.
- **Capture never fails a run.** A failed child export increments
  `subagent_export_failures` and the run keeps what it captured. *Cost if
  wrong:* a systematically failing capture is visible only as a metric.
- **Session files live under `runs/<id>/sessions/`**, are never written into a
  worktree and are never served by the dashboard. *Cost if wrong:* none — they
  are artifacts like any other.
- **A retry or compaction attaches to the most recently opened step**, and to a
  synthetic step 0 when no step has opened yet. *Cost if wrong:* a compaction
  emitted between steps is attributed to the previous step rather than the next.

## Task infrastructure and the agentic suite

- **The hidden-test destination is per task** (`hidden_tests_dest`, default
  `tests`). Hard-coding `tests/` assumed a language whose tests live in their own
  directory; Go tests sit beside the source, so a Go task sets `"."`. *Cost if
  wrong:* one more key in `task.yaml`.
- **A `.hidden` suffix keeps a Go test out of the repository's own build.** Go
  test files anywhere under the module are compiled by `go vet ./...` and
  `go test ./...`, so a hidden Go test stored as `_test.go` broke the repository
  it lives in. Storing it as `_test.go.hidden` and stripping the suffix on copy
  keeps both properties. *Cost if wrong:* one naming convention to remember.
- **Hidden tests keep their directory name.** `evaluator/tests/` lands at
  `<worktree>/tests/` because a validator refers to it by path. The first
  implementation walked the subtree and wrote files at the worktree root, which
  broke every task whose validator runs `unittest discover -s tests`. *Cost if
  wrong:* a task cannot place hidden files outside `tests/`.
- **Runs set `PYTHONDONTWRITEBYTECODE=1`.** Stray bytecode counts as created
  files and can be read by a grep validator; worse, a same-length edit inside
  the same second makes Python reuse a stale `.pyc`, so a fixed task still
  fails. *Cost if wrong:* a task that genuinely needs bytecode caching loses it.
- **The grep validator skips files that look binary** (a NUL byte in the first
  8 KiB, the heuristic git uses). *Cost if wrong:* a pattern that only appears
  in a binary artefact is not matched.
- **A process validator may only check what the prompt asks for**, and a task
  must be solvable without the capability it names. Grading an unstated
  preference measures obedience to the grader, not capability. *Cost if wrong:*
  some agentic behaviours cannot be checked at all.
- **`score` is weighted and `success` stays binary.** Skipped validators count in
  neither the numerator nor the denominator, and `score` is omitted rather than
  zeroed when everything was skipped. *Cost if wrong:* consumers reading only
  `success` see no partial progress.
- **The honesty harness excludes process validators** because they observe agent
  behaviour, which no reference tree can supply; those are proven by a live run
  instead. *Cost if wrong:* a broken process validator is caught by a live run,
  not by the suite test.
- **Reference solutions are file trees, not patches.** They copy over a scratch
  fixture, so they review in a diff and need no patch tooling. *Cost if wrong:*
  a reference that deletes a file cannot be expressed.

## Documentation

- **The design is one document**, not a set of per-subsystem files. It is long
  but internally consistent, and a split would let sections drift. *Cost if
  wrong:* a reader looking for one subsystem scrolls more.
- **Evidence records are kept verbatim**, with a footer noting that process
  names and scratch paths reflect the workflow in use at the time. *Cost if
  wrong:* a reader may look for a scratch directory that no longer exists.
