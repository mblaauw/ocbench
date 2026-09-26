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
