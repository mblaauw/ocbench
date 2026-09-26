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

## Dashboard

- **Profile-first, not run-first.** The landing page ranks profiles rather than
  listing runs, because the question is "which configuration is better", and a
  run list answers it only by proxy. *Cost if wrong:* the newest run takes one
  more click.
- **Two pages, rendered from one vertical slice.** Overview and Runs were built
  end to end before Architecture and Suites and tasks, so the scoring rules and
  the layout are exercised against real data early. *Cost if wrong:* two pages
  are missing while the implemented two are useful.
- **A suite is scored only from its most recent `suite_hash`.** Runs from an
  older suite revision measure a different thing, so mixing them would silently
  average two benchmarks. They are counted and disclosed as `ExcludedRuns`
  instead. *Cost if wrong:* after editing a suite, its older runs stop counting
  until it is re-run.
- **A task's score is the mean of its runs' `score`, falling back to `success`.**
  Partial credit is what separates configurations that both eventually pass.
  The fallback keeps runs recorded before the metric existed from reading as
  zero. *Cost if wrong:* a run whose metrics were never written scores 0.
- **Intervals come from a seeded bootstrap**, not a normal approximation, so the
  same store renders the same interval on every page load and in every test.
  *Cost if wrong:* the interval is not what a textbook formula would give.
- **Nothing is computed at write time.** Scores, weights, intervals and
  exclusions are derived when a page is rendered, so a new statistic applies to
  runs recorded last month. *Cost if wrong:* a page render does more work than a
  stored figure would.
- **The dashboard is inert: no JavaScript, no CDN, no inline style.** The
  content security policy is `default-src 'none'` with only `style-src`,
  `font-src` and `img-src` limited to self, and it is enforced by the browser
  rather than by convention — which is why dynamic geometry is SVG attributes and
  the fonts are vendored. *Cost if wrong:* no interactive filtering, and a chart
  that needs a computed pixel must use an attribute or a discrete class.
- **The profile is rendered in full; run raw material never is.** Instruction
  text, permission rules, skills and MCP servers are the configuration under
  test and belong on screen. Events, sessions, worktrees and untracked files are
  large, noisy and can contain the user's own content. *Cost if wrong:* the
  architecture page cannot show a raw event excerpt.
- **The Architecture page reads the capture files, not just the database.** The
  fingerprint is deliberately lossy — a prompt is a hash, a model is a string —
  so the page that explains a profile has to read `profiles/<hash>/*.json`
  beside it. The reader tolerates a missing directory and fails on a malformed
  file, because an absent capture is a profile the database alone can still
  describe, while a corrupt one must not silently render as an empty prompt.
  *Cost if wrong:* a profile recorded on another machine has no text to show.
- **Skill bodies are not rendered; their names, descriptions, locations and
  content hashes are.** 39 skills carry about 326 KB of library text that barely
  differs between profiles, so rendering it would triple the page size to show
  content that does not explain a score difference. design.md §15.3 asks for
  "skills" while asking explicitly for agents' instruction text, which are
  rendered in full. *Cost if wrong:* a reader must open the capture file to see a
  skill's body.
- **The page is served at `/arch`, not `/profiles`.** design.md §15.1 names the
  page Architecture at `/arch` and `/arch/{hash}`, so the inherited route was
  corrected rather than kept as an alias. *Cost if wrong:* a bookmark to the
  earlier route 404s, which is acceptable because it was never published.
- **The sidebar states stored facts only.** Run and profile counts come from the
  store, so opening a page cannot touch the machine being benchmarked and cannot
  disagree with the tables below it. *Cost if wrong:* a stale OpenCode version in
  the sidebar is not noticed until a run is started.

## Measurement validity

- **A ranking is gated on the effect its sample could detect, not on a p-value
  alone.** A leaderboard that names a winner from seven tasks whose scores never
  varied is worse than one that says nothing. The verdict distinguishes "too few
  tasks", "no spread to size an experiment against", and a gap that genuinely
  exceeds what the data could resolve. *Cost if wrong:* a real but small
  difference is reported as unmeasurable until more runs exist.
- **Noise is measured only from repeats of one configuration on one task.**
  Running three configurations once each is not repetition, and treating it as
  such would invent a spread. A task with no repeats is reported as having no
  estimate. *Cost if wrong:* most of a young corpus has no noise figure at all.
- **The spread is pooled within groups, each normalised by its own mean.**
  Concatenating groups instead would understate the spread, because duplicating
  each group's deviations inflates the denominator without adding information —
  a bug the first implementation had and a test caught. *Cost if wrong:* noise is
  under-reported and small differences look real.
- **A spread from fewer than five observations is marked indicative.** The
  relative standard error of a sample standard deviation is about
  `1/sqrt(2*dof)`, so two or three runs put the spread itself out by 50–70%.
  *Cost if wrong:* a provisional figure is read as a constant.
- **Config keys are promoted out of the catch-all, and this re-hashes every
  profile.** `command`, `formatter`, `lsp`, `mode` and `provider` split per
  entry; `autoupdate`, `compaction`, `share` and `tools` are promoted whole.
  Attribution is the whole point of fingerprinting a config, and it cannot be
  had without changing the hash. *Cost if wrong:* the recorded corpus became a
  legacy cohort and must be re-run once.
- **An unsplit key still lands in `config`.** The promotion covers the keys whose
  changes are individually actionable; `watcher`, `tool_output`, `attachment`,
  `enabled_providers` and `subagent_depth` remain together. Claiming finer
  attribution than exists would be worse than admitting the residual. *Cost if
  wrong:* a change to one of those still reads as `configuration`.
- **`tokens_cache_write` is not reported by OpenCode.** The raw events carry
  `"cache":{"write":0,…}`, so the field is present and always zero. The harness
  parses it correctly; the provider does not populate it. *Cost if wrong:* cache
  write economics cannot be measured on this OpenCode version at all.

## Harvesting tasks from real work

- **A harvested task is a user turn paired with the commits that landed in its
  window.** That pair is what a task needs: the request, and the change that
  answered it. The window ends at the next substantive instruction, so an
  acknowledgement like "thanks" does not split a work unit in two. *Cost if
  wrong:* a long turn that produced several commits is proposed as one task.
- **Acknowledgements are not tasks.** A turn shorter than `--min-prompt` is
  dropped rather than paired with whatever commit happened to land beside it,
  which would produce a task whose prompt does not describe the work. *Cost if
  wrong:* a terse but real instruction is skipped.
- **Only top-level sessions are harvested.** A subagent session has no user turn
  of its own and would duplicate its parent's work. *Cost if wrong:* the work
  done by subagents is represented only through its parent.
- **The harvester proposes and never publishes.** A harvested task is built from
  private code, so the command only lists what it found and writes nothing; the
  session database is opened read-only. *Cost if wrong:* curating a task is
  manual work the tool does not do for you.
- **Size caps are opt-in.** A turn that produced 39 commits across 2219 files is
  a project, not a task, but the tool will not silently decide that for you: the
  caps default to off and the listing shows the size so the choice is visible.
  *Cost if wrong:* the default listing is dominated by unusable candidates.

- **A harvested scaffold is verified before it is offered.** Export copies the
  fixture to a temporary directory, runs the task's validator, applies the
  reference and runs it again; a task that passes untouched or still fails with
  its own reference is reported as such. Without the check a broken scaffold
  looks exactly like a working one. *Cost if wrong:* exporting runs the test
  command twice, which costs seconds.
- **The fixture carries the tests the work added.** That is what makes the task
  fail before the change and pass after it, and it is why a candidate that
  changed no test file usually cannot become a task. *Cost if wrong:* a change
  whose effect the test command does not observe is proposed and then fails
  verification.
- **`OCBENCH_HONESTY_SUITES` overrides the honesty test's root.** A task built
  from private code must be verifiable without that code entering the public
  repository. *Cost if wrong:* verifying a harvested suite needs one environment
  variable rather than a plain `go test`.
