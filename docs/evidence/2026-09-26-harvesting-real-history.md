# Evidence — harvesting tasks from real history (slice F)

Date: 2026-09-26. Commits `385bd73`, `34a4051`.

Slice F of [the measurement-validity plan](../plans/2026-09-25-ocbench-measurement-validity.md).
The synthetic corpus cannot show whether a configuration suits the work someone
actually does, so this slice proposes tasks from real sessions.

## What the history contains

The real session database (`~/.local/share/opencode/opencode.db`) holds 253
sessions, 10,556 messages and 47,888 parts across 20 projects:

| Repository | Sessions |
|---|---|
| `/Users/mich/dev/mbl-ocbench` | 126 |
| `/` (outside a repository) | 62 |
| `/Users/mich/godot-sandbox` | 42 |
| `/Users/mich/dev/soar` | 5 |

**The shape is not one session per task.** 59 sessions are top-level and 194 are
subagent children, and the ocbench work lives in a *single* top-level session of
1,316 messages with 67 user turns. So the harvest unit is a user turn, not a
session: each turn is paired with the commits that landed before the next
substantive instruction.

That linkage was verified by hand before any code was written. The first user
turns and the first commits interleave correctly once both are compared in UTC,
which is why the implementation reads `%ct` from git rather than a formatted
date.

## What the harvester found

`ocbench harvest` over the real database: 15 candidates from 2 repositories, 11
of them touching a test file.

| When | Repo | Commits | Files | Lines | Tests | Prompt |
|---|---|---|---|---|---|---|
| 2026-09-22 15:18 | mbl-ocbench | 39 | 2219 | +3,857,793 | yes | *were are going to build a new project: Yes. For your first version…* |
| 2026-09-23 19:52 | mbl-ocbench | 20 | 31 | +6,229 | yes | *do it then move on to task 5, 6, 7, 8 and 9* |
| 2026-09-24 19:31 | mbl-ocbench | 4 | 28 | +2,776 | yes | *1 C, dark for now. 2, vendoring, 3, agree, 4 bootstrap, 5 confirmed…* |
| 2026-09-24 17:38 | mbl-ocbench | 16 | 173 | +3,990 | yes | *1 sing;e. 2 follow recommendation, 3 no, do it* |
| 2026-09-25 20:15 | mbl-ocbench | 1 | 8 | +1,268 | yes | *yes do that i follow your recommendation* |
| 2026-09-08 17:44 | soar | 2 | 11 | +166/−205 | no | *merge all to main first then test complete app…* |

## The finding: the linkage is sound, the prompts are not

Two problems, and only the second is fundamental.

**1. Sizes are wildly variable, and fixable.** A turn that produced 39 commits
across 2,219 files and 3.8 million lines is a project, not a task, and no fixture
can represent it. `--max-commits`, `--max-files` and `--max-lines` drop these.
Capped at 3 commits / 15 files / 1,500 lines the list falls to 5 candidates. The
caps are opt-in: the tool will not silently decide what is too big.

**2. Most user turns are not standalone task statements.** This is the real
obstacle:

> *"yes do that i follow your recommendation"*
> *"do it and continue B, then do C and then do D and then do E"*
> *"do it then move on to task 5, 6, 7, 8 and 9"*
> *"good start, continue with the rest."*

These are continuations of a conversation. They carry no goal, no acceptance
criterion and no context, so a benchmark run given one of them verbatim would
measure nothing: the agent would be told "do it" with no referent. The minority
that do state a task — *"were are going to build a new project…"*, *"merge all to
main first then test complete app"* — are usable.

So the harvester's output is raw material for curation, not finished tasks. That
is an honest and useful result, but it means slice F is **not complete**: the
step from candidate to task is missing.

## Export: from candidate to verified task

`ocbench harvest --export <dir> --index N` now writes a task scaffold and
**checks the honesty property itself** rather than claiming it:

- `fixture/` — the repository before the candidate's first commit, **with the
  test files the work added or changed at their final content**. That is what
  makes the task fail before the reference is applied.
- `evaluator/reference/<path>` — the changed non-test files at their final
  content.
- `prompt.md` — the raw material, with the recorded turn, the session title and
  the commit subjects clearly marked as the answer a human must keep out of the
  prompt.
- `task.yaml` — a scaffold whose command validator is inferred from the fixture's
  manifest.

Export then copies the fixture to a temporary directory, runs the validator,
applies the reference, and runs it again. Two candidates harvested from real
history pass:

```
Wrote a task scaffold to /tmp/harvested/measure-the-run-to-run-noise-floor-and-the-detec-9e25b2b
Checked: go test ./... fails on the fixture and passes with the reference, which is
the property every task is held to.
```

```
Wrote a task scaffold to /tmp/harvested/keep-the-dashboard-within-its-content-security-p-db41716
Checked: go test ./... fails on the fixture and passes with the reference, which is
the property every task is held to.
```

### The check earns its place

A third candidate — *"do it and also add suites specifically to test agentic
workflows"* — was exported and **failed the check**:

```
Check FAILED: the fixture passes untouched, so the task cannot show anything
```

The change added a benchmark suite, so no changed path looked like a test file
and nothing landed in the fixture; `go test ./...` passed before and after. The
tool refused to call that a task. Without the check the scaffold would have
looked identical to the two that work.

This is the general shape of the limitation: **the inferred validator is a guess
and is often wrong.** It is right when a candidate added a test that the fixture
can then carry, and wrong for a change whose effect `go test ./...` does not
observe. Export reports which it was.

### Remaining gaps

- The fixture is the **whole repository**, not the package the work touched. A
  real task should be trimmed; the export does not do that yet.
- The prompt still needs a human. Export says so and shows what to work from, but
  it does not synthesise a goal.
- No candidate has been run through `ocbench run`; only the honesty check has.

## What that step requires

A draft prompt has to be **synthesised** from the turn plus its context, then
edited by a human. Two constraints on how:

- The session **title** is usable context (*"Local OpenCode benchmark appliance
  design"*).
- The **commit subjects must not go into the prompt.** They describe the solution
  — *"feat: add canonical json, redaction, path normalization and hashing"* — so
  including them would hand the agent the answer. They belong in the reference
  metadata the human reads, never in the prompt.

A fixture export is also missing: a task needs the repository at the commit
*before* the candidate's first commit, and a validator that shows the task fails
before and passes after. Neither exists yet, so **no harvested candidate has been
proven through the fail-before/pass-after test** the existing 14 tasks all pass.

## Test coverage

`internal/harvest/harvest_test.go` — turns are linked to the commits in their
window; an acknowledgement is skipped and does not split the previous turn's
window; child sessions are ignored; the repo filter works; oversized turns are
dropped and in-size ones kept. The tests build an OpenCode-shaped database and a
git repository in a temp directory and **never read a real user database**.

`internal/cli/harvest_test.go` — human and JSON rendering, and a missing database
reported as a usage error naming the path rather than a crash.

## Privacy

The command reads the session database read-only, writes nothing, and prints the
path it read. Harvested tasks are built from private repositories, so publishing
one is a deliberate human decision rather than something the tool can do.

## Gates

`go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web` (empty) and
`make cross` (linux amd64 + arm64, CGO_ENABLED=0) all pass at `34a4051`.

## Not done

- **Two harvested tasks are proven honest**, but their prompts are still raw
  material and their fixtures are whole repositories.
- **The corpus has still not been re-run** under the Slice C fingerprint.
- **Slice G** — confirmation by replication — is not started.

## End-to-end: a real task runs

The harvested task was placed in a local suite (`/tmp/harvested-suites/harvested`,
never in this repository) and run for real:

```
1 measure-the-run-to-run-noise-floor-and-the-detec-9e25b2b  PASS  2m25.768s  781456 tokens  38 tools
1/1 successful
```

It passed the honesty check through the real runner first
(`OCBENCH_HONESTY_SUITES=/tmp/harvested-suites go test ./internal/suite -run
TestEveryTaskFailsUntouchedAndPassesWithItsReference`), which is what that
override exists for.

### What a real task costs

| Task | Tokens | Tool calls | Duration |
|---|---|---|---|
| harvested (real work) | **781,456** | **38** | **2m26s** |
| go-slice-bug (synthetic) | 58,041 | 5 | 19s |
| go-pager-cursor (synthetic, hard) | 83,698 | 8 | 42s |

**Real work costs 13x the tokens and 7.6x the wall-clock of the hardest synthetic
task.** Its cache hit rate is 93%, against 80% for the synthetic corpus.

This is the most consequential number in the whole measurement-validity effort.
Every noise floor and detectable effect measured on the synthetic corpus is
measured in a regime an order of magnitude cheaper than the work it is supposed
to predict. A configuration tuned for token efficiency on 58k-token tasks is
being tuned against the wrong problem, and the dashboard's `cost per solved task`
of $0.003 is not the cost of real work.

## A harness bug the harvest exposed

Verifying the harvested task through the honesty test initially failed with
`undefined: Chunk` and `undefined: Cursor` — errors from *other* tasks' tests.

`copyFS` in the honesty test strips `.hidden` from every file it copies, and it
was used for the fixture as well as for hidden tests and references. The
harvested fixture is a copy of this repository, which contains
`suites/**/evaluator/tests/*.go.hidden`; copying it un-hid those files into
runnable tests that nothing could satisfy.

Fixed in `4451769`: the fixture is copied verbatim, and only hidden tests and
references use the stripping copy. The existing suites have no `.hidden` files in
their fixtures, so nothing else changed.

The export's own `Verify` had reported the task as honest, correctly: it never
stripped the suffix. The two checks disagreeing is what surfaced the bug.

## Rewriting the prompt, and what it did

The harvested task's prompt was rewritten from the recorded turn (*"do it and
continue B, then do C and then do D and then do E"*) into a standalone statement:
implement the statistics and the variance read model that two test files
describe, so `go test ./...` passes. The task still passed the honesty check, and
was then run.

| Prompt | Outcome | Duration | Tokens | Tools |
|---|---|---|---|---|
| the recorded turn, verbatim | **passed** | 2m26s | 781,456 | 38 |
| rewritten as a specification | **timeout** | 15m26s | 3,043,794 | 54 |

Both are in the store. Three findings, in order of importance.

**1. The prompt is a larger lever than most configuration parameters.** The same
task, the same fixture and the same model produced a pass in 2m26s or a timeout
at 15m26s depending only on how the request was phrased — a 4x difference in
tokens and a pass/fail flip. Nothing in the current harness measures this,
because the prompt is part of the task rather than part of the configuration
under test. For anyone tuning a setup, it is the strongest signal in the data.

**2. The harvested task is too large for the default timeout.** 3.0M tokens and
926 seconds against a 900-second limit. The size caps that admitted it — 3
commits, 15 files, 1500 lines — did not predict that: **line count is a poor
proxy for effort.** The real work behind this candidate was four slices of a long
session, not one sitting. A tractability filter would need to be based on the
reference's own cost, which is only knowable after running it once.

**3. The fixture is the whole repository, and `vendor/` is in it.** 139 MB, most
of it vendored dependencies the agent has no reason to read but may well explore.
Excluding paths is now possible (`--exclude`), but on a Go module it is
build-sensitive: excluding `web/` or `suites/` breaks `go test ./...` because
`internal/web` and `internal/cli` import them, and `Verify` refuses the result.
Excluding `docs/` is safe and verified. **The check is what makes trimming usable
at all** — an unsafe trim produces a task that silently cannot be solved.

### What this implies for the corpus

The two harvested runs are the only real-work data points that exist, and they
already show the shape of the problem: an order of magnitude more expensive than
the synthetic corpus, extremely sensitive to prompt phrasing, and large enough to
exceed a default timeout. A useful tuning corpus needs tasks cut down to one
sitting — which means harvesting a *slice* of a turn's work rather than the whole
turn, and that is not implemented.

## Sub-turn harvesting: the supply problem, solved

A turn is not a unit of work. The history holds **159 commits across 19 turns** —
one turn produced 39 commits, another 20 — and proposing a turn as one task
produced something too large to finish, which is what the timeout above showed.

`--split commit` proposes each commit as its own candidate. The prompt stays the
turn's, because the turn is what asked for the work; only the change set differs.

| | Whole turn | Split by commit, capped at 400 lines / 12 files |
|---|---|---|
| Candidates | 19 | **95** |
| Changed a test file | — | 46 |
| **Also changed something to implement** | — | **42** |

Forty-two candidates where there was one. The shapes are what a task needs:

```
2026-09-24 17:38  mbl-ocbench  1  6  +40/-0   fix: keep hidden Go tests out of the repository
2026-09-24 17:38  mbl-ocbench  1  3  +26/-0   fix: resolve the pager cursor contradiction
2026-09-24 17:38  mbl-ocbench  1  2  +114/-0  feat: record process metrics
```

### A change confined to test files cannot be a task

Of three sampled exports, one verified and two failed with *"the fixture passes
untouched"*. Both failures had the same cause: the change was **entirely within
test files**, so there was no implementation to put in the reference — the test
was the whole change.

That is correct behaviour, and it is now visible before exporting. The listing
gained a `REFS` column: how many changed files are not tests. A task needs at
least one, because the tests go into the fixture and the implementation becomes
the reference. The listing reports that **42 of the 95 candidates are gradable**.

### What is still missing

- **The prompt is still the turn's.** For a split candidate the turn may have
  asked for twenty things; the human must cut the prompt down to the one commit
  it now describes. The export says which commit it is for, but does not do the
  cutting.
- **Tractability is still measured in lines.** 400 lines admitted a task that
  timed out; the honest measure is the reference's own cost, which needs a run.

## The prompt is now derived from the check

The last gap was that a split candidate's prompt was still the whole turn's text,
which may have asked for twenty things. The prompt is now written from **what the
verification observed**: the failing tests are the specification, and the agent
can already read them in the fixture, so quoting the failure reveals nothing the
task does not.

```
# Make the failing tests pass

`go test ./...` fails in this repository. The failing tests are the
specification: make them pass without weakening them.

## What is failing

--- FAIL: TestEveryTaskFailsUntouchedAndPassesWithItsReference (2.77s)
    honesty_test.go:31: task "go-pager-cursor": validator "go tests" (command) is failed …
        --- FAIL: TestPageExactBoundary (0.00s)
            pager_ties_test.go:70: next=&{2 b}, want nil at the end of the list
```

No model is involved. The recorded turn, the commit subjects and the file lists
follow below a `## Material for the curator` heading, because the turn is not the
prompt and the subjects describe the answer.

### Each outcome is stated as itself

The first version of this told a curator *"the fixture passes untouched"*
whenever the task failed for **any** reason — including when the reference was
what failed. That sends someone to fix the wrong thing. There are four outcomes
and each now says which one it is: honest, fixture passes untouched, the
reference does not satisfy the validator, or no validator could be inferred. A
test pins the wording of all four.

### A repo-wide validator makes a confusing prompt

The example above is honest and still awkward: the failing test is ocbench's own
honesty check, because `go test ./...` runs the whole repository and the change
touched a suite fixture. The prompt is mechanically correct — that *is* what the
commit fixed — but a reader has to work out that the failure is a fixture
problem rather than a missing function.

This is the whole-repository fixture showing up a third time, now in the prompt.
The fix is the same one: narrow the validator to the package the change is about,
which the export does not do because inferring it is a guess.

## Validator narrowing: the prompt becomes a specification

The validator was `go test ./...` — the whole repository — which fails on parts
of the tree a change never touched. That produced a task that could not be solved
and a prompt describing somebody else's failure. It is now scoped to the packages
the work touched:

```
command: ["go", "test", "./internal/cli", "./internal/history", "./internal/stats"]
```

and the prompt it produces is precise:

```
# Make the failing tests pass

`go test ./internal/cli ./internal/history ./internal/stats` fails in this repository.

## What is failing

internal/stats/power_test.go:21:21: undefined: ZFor
internal/stats/power_test.go:29:20: undefined: Mean
internal/stats/power_test.go:33:12: undefined: StdDev
internal/stats/power_test.go:56:13: undefined: MDE
internal/history/variance_test.go:11:41: undefined: history.VarianceReport
```

That is the specification: the failing symbols are exactly what has to be built.
Compare the same export before this change, whose prompt quoted ocbench's own
honesty test failing on an unrelated task fixture.

The "cutting" step that previously needed a human is now done mechanically, for
free, by narrowing the validator. The turn's text and the commit subjects still
follow under `## Material for the curator`, and the prompt still carries a
`## TODO` for what the change is *for* — a failure says what to build, not what
counts as a good solution.

### What this does not fix

**The timeout is unaffected.** The runner applies validators *after* the agent's
session ends, so the validator cannot cause or prevent an agent timeout. The
15m26s run was the agent working, not the grader. Narrowing the validator fixes
prompt legibility and fixture fragility; the timeout remains a task-size problem,
and the honest measure of size is still missing.

### How the scope is chosen

The directories of the changed files, kept only when they hold a `.go` file in
the fixture — naming a directory with none would fail the command for a reason
unrelated to the task. The module root is `.` to `go test`, not `./.`. An empty
result falls back to the whole module rather than emitting a command that tests
nothing. Tests pin all three behaviours.
