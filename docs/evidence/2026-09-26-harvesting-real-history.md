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

- **No harvested task exists yet.** Draft-prompt synthesis and fixture export are
  both missing, so nothing has been proven fail-before/pass-after.
- **The corpus has still not been re-run** under the Slice C fingerprint.
- **Slice G** — confirmation by replication — is not started.
