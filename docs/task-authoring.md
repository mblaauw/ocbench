# Authoring benchmark tasks

This document describes how to add tasks and suites to `ocbench`. It reflects
the implementation in `internal/suite` and the rules in the design spec §7.

## Suite layout

```
suites/<suite>/
  suite.yaml
  tasks/<task-id>/
    task.yaml
    prompt.md
    fixture/            materialised into the agent worktree (only this)
    evaluator/          hidden; never materialised into the agent worktree
```

Only `fixture/` reaches the agent. Everything under `evaluator/` is read out of
band by the validators and must never be copied into the worktree. The core
suite ships inside the binary with `//go:embed all:core`; the `all:` prefix is
required because plain `//go:embed` silently drops `_`- and `.`-prefixed files
such as `__init__.py`. No Go source may live below `suites/core/`: it is data
only.

## Schema

`suite.yaml`:

```yaml
name: core
version: "1.1.0"
description: Core fixture-only benchmark tasks.
defaults:
  timeout: 300        # seconds; optional
```

`task.yaml`:

```yaml
id: py-bugfix
version: 1
name: Fix the divide operator
tags: [python, debugging]
timeout: 300                  # optional; falls back to suite default, then 900
requires: [python3]           # optional task-level; missing tools skip every validator
allow_changes: ["calc.py"]    # globs relative to the worktree root
validators:
  - kind: command
    name: unit tests
    command: ["python3", "-m", "unittest", "discover", "-s", "tests"]
  - kind: answer
    name: defect identified
    patterns: ["retry", "off-by-one"]   # or evaluator/answer.json
    mode: all                            # "all" (default) or "any"
```

Decoding of `suite.yaml` and `task.yaml` (including each validator) is strict
(`KnownFields(true)`): an unknown or misspelled key is a load error, not a
silent default. `id` must equal the task directory name. `version`
is an opaque string, so `version: 1` and `version: "1.1.0"` both load.

Answer patterns may instead live in `evaluator/answer.json`:

```json
{ "patterns": ["(?i)\\bmaximum\\b"], "mode": "all" }
```

Patterns are Go regular expressions matched against the model's final message:
the most recent `text` event in the event stream (the last observable text in
JSONL order), not the concatenation of every text event. Intermediate
tool-progress chatter is therefore never matchable, so patterns must be
satisfied by the terminal answer alone.

## Determinism

- A task must be self-contained: fixtures use only files in `fixture/`, with no
  network access and no developer-machine paths.
- The fixture tree is committed once with fixed author/committer identity and
  fixed dates, so its baseline commit SHA is identical across machines and
  invocations.
- Validators must be deterministic and must not depend on wall-clock time,
  randomness, locale, or environment variables outside the sandbox allowlist.
- Use the standard library only (Python 3 stdlib for Python fixtures); do not
  add third-party dependencies.

## Validators

- `kind: command` runs an argv array, never a shell string. The process starts
  in the fixture/worktree directory under the sandboxed environment. A
  non-zero exit is `failed`; a start error is `error`.
- `kind: answer` matches `patterns` against the final message (the final `text`
  event, as above). With `mode: all` (default) every pattern must match; with
  `mode: any` at least one must.
- `requires` is task-level, not per validator: when any required binary is
  unavailable on `PATH`, every validator in that task is `skipped`, never
  `failed`. Keep `requires` accurate (`[python3]` for Python fixtures).
- Validator output is captured to `runs/<id>/validation/<seq>-<name>.log`.
- `allow_changes` globs describe the changes a task expects. Paths changed
  outside those globs feed the `files_unexpected` metric; they do not by
  themselves fail a validator.

## Hidden evaluator rules

- Hidden data lives in `evaluator/` and is passed to validators out of band. It
  is never written into the agent worktree and the agent must not be able to
  read it.
- For answer tasks, keep the discriminating information (patterns, expected
  keywords) in `evaluator/answer.json` rather than in `task.yaml`, so the
  worktree copy of the task spec does not reveal the answer.
- Do not put secrets or anything non-deterministic in `evaluator/`.

## The untouched-fixture failure rule

Every task's validators MUST fail on the untouched fixture and pass after the
intended fix. This is the mandatory authoring rule: it is the only proof that
the task measures something. When authoring a task, copy the fixture to a
scratch directory under `/tmp` (or use `t.TempDir()` in tests) and run each
validator by hand before and after applying the intended change.

Automated coverage is not uniform across the core suite, and the docs should
not overstate it. Today `internal/suite/core_test.go` encodes an automated
fail-before/pass-after proof only for `config-yaml-fix` (a command validator on
a materialised fixture). `code-review` is answer-only: its test runs the real
validator engine to prove the hidden patterns accept a complete review and
reject both incomplete and keyword-stuffed reviews, but it has no
fixture-level fail-before/pass-after step because its validators do not run
against the fixture. The remaining core tasks (`py-bugfix`,
`multi-file-feature`, `repo-investigation`) have no automated proof yet and
rely on the manual authoring-time check. New core tasks should extend
`internal/suite/core_test.go` with a real-engine fail-before/pass-after test.

## Adding an external suite

A suite does not have to ship inside the binary. Place a directory that follows
the layout above under the ocbench home:

```
$OCBENCH_HOME/suites/<name>/suite.yaml
$OCBENCH_HOME/suites/<name>/tasks/...
```

`OCBENCH_HOME` defaults to `$XDG_DATA_HOME/ocbench`
(`~/.local/share/ocbench`), so the default location is
`~/.local/share/ocbench/suites/<name>`. Resolution order is:

1. an explicit `--suite-dir <path>` passed to `ocbench run`;
2. `$OCBENCH_HOME/suites/<name>` when that directory exists;
3. the embedded suite of that name.

An on-disk suite with the same name as an embedded one overrides it. A not-found
error names every location tried. The `suite.Export` helper writes an embedded
suite to disk as a starting point; a destination must be empty.
