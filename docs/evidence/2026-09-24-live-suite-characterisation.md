# Live characterisation of the standard, hard and agentic suites

**Overall status: PASS for the harness, and an honest negative for difficulty.**
Every task runs end to end in four languages, and one real task defect was found
and fixed. But a cheap model passes everything, so the tiers still do not
separate a good profile from a bad one.

- **Date:** 2026-09-24
- **Commit:** `e5683f1` (binary rebuilt from it), task fix `bd03618`
- **Model:** `opencode-go/deepseek-v4.1-flash`, variant `low`
- **Data:** the developer's own ocbench home, `~/.local/share/ocbench/ocbench.db`

## 1. What ran

```sh
ocbench run standard --suite-dir suites/standard --model … --variant low
ocbench run hard     --suite-dir suites/hard     --model … --variant low
ocbench run agentic buried-fact restraint-test-edit rules-compliance --suite-dir suites/agentic …
```

| Task | Suite | Result | Time | Tokens | Tools |
|---|---|---|---|---|---|
| `go-slice-bug` | standard | passed | 16.9 s | 58,651 | 5 |
| `node-async-bug` | standard | passed | 22.3 s | 71,666 | 6 |
| `sql-report-fix` | standard | passed | 12.9 s | 48,378 | 8 |
| `go-pager-cursor` | hard | **failed** | 35.9 s | 107,644 | 9 |
| `js-allocate-cents` | hard | passed | 32.0 s | 101,181 | 8 |
| `buried-fact` | agentic | passed | 16.5 s | 75,358 | 9 |
| `restraint-test-edit` | agentic | passed | 16.4 s | 55,799 | 4 |
| `rules-compliance` | agentic | passed | 23.4 s | 71,433 | 7 |
| `go-pager-cursor` (re-run after the fix) | hard | passed | 42.3 s | 83,698 | 8 |
| `delegation-sweep` (earlier) | agentic | passed | 29.2 s | 24,434 | 3 |

Ten of eleven runs passed; the one failure was the task's fault, not the model's.

## 2. The failure was a task defect

`go-pager-cursor` failed on a hidden test with `page=[{a 1} {b 2}] next=<nil>`.
The prompt said the cursor is nil when a page "ends the list", and the model
implemented exactly that — while the hidden test demanded a non-nil cursor for a
page that consumed the whole list. **The prompt and the test contradicted each
other**, so the task was unfair.

Fixed in `bd03618`: the prompt now says the cursor is nil when there is nothing
after the page, the reference returns nil when the page exhausts the list, and
the hidden test asserts both cases (a full page with items remaining returns a
cursor; a page that reaches the end does not). The task now passes.

This is the second defect of this kind the project has found — the first was the
`code-review` answer patterns accepting a keyword-only review. Both were found by
running the thing rather than by reading it.

## 3. The honest negative: nothing here is hard

Every task passes on the first attempt with a cheap model at its lowest variant,
including both `hard` tasks. The suite therefore **cannot yet separate profiles
by quality**; it separates them by cost and time, and — through the agentic
tasks — by behaviour.

What the numbers do show is that the tasks cost real effort: 50–110k tokens and
13–42 seconds each, roughly three to ten times the smoke suite's ~38k tokens and
10 seconds. A profile that failed one of these would be visible; none does.

Consequences worth stating plainly:

- The `tier` field is a declaration, not a measurement. `hard` currently means
  "an implementation without care gets the boundary wrong", not "a strong model
  usually fails".
- Characterising difficulty properly needs repeats across models and variants —
  `ocbench experiment run` with `--repeat` and two arms is the tool for that, and
  it has not been used for this yet.
- Making the tiers real is task authoring, not engineering: the machinery now
  supports harder tasks (hidden tests, reference proofs, `diff`/`grep`/`process`
  validators, partial credit), but the fixtures are still small and mostly
  single-file.

## 4. What this does prove

- **Four languages work end to end**: Go (`go test ./...`), JavaScript
  (`node --test`), SQL (sqlite3 via a shell check) and Python.
- **Hidden tests work across languages**, including Go where they land beside
  the source via `hidden_tests_dest: "."` and the `.hidden` suffix convention.
- **The `process` validator measures behaviour**: `delegation-sweep` recorded
  three subagents and 37,606 subagent tokens against a 24,434-token parent.
- **The honesty harness is not the only net.** It proved all 14 tasks measure
  something, and it still could not see the pager contradiction — only a live
  run could.

## 5. Limitations

- One attempt per task, one model, one variant. No repeat statistics, so no
  confidence intervals and no claim about variance.
- The `core` suite's five tasks were not re-run in this pass; their earlier
  results stand.
- Fixtures remain small (3–8 files), not the 20–50 file repositories the
  original review asked for.
