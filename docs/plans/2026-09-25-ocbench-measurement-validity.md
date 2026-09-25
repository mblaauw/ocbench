# Measurement Validity Implementation Plan

Make the benchmark able to answer the question it exists for: *does this
configuration change help?* The harness measures faithfully; the corpus and the
attribution are what stop the question being answerable today.

## Why this plan exists

A sanity check over the live store (13 runs, 5 profiles) found:

| Axis | State | Evidence |
|---|---|---|
| tokens in/out, cache read | sound | repeats spread 0.2–1.4% |
| cache write | not reported upstream | raw events carry `"cache":{"write":0,…}` |
| quality | **saturated, no signal** | 10 of 11 scored runs = 1.00 |
| speed | weak | repeats spread 13% |
| config params | **unattributable** | every unconsumed key hashes into one `config` component whose diff reads `"configuration"` |
| agents, prompts, subagents, architecture | attributable | per-agent components, `prompt_sha256`, permission edges |
| n per task | **far too small** | 6 of 9 exercised tasks have exactly one run |
| arm interleaving | already correct | `experiment.BuildPlan` alternates arms inside each repeat |

## Decisions taken

- **Objective function**: primary **cost per solved task**; then cache hit rate
  and tokens; then quality; then speed. Recorded in design §15.2.
- **Budget**: ~30–50 runs per experiment (~15–20 minutes; money is not the
  constraint, wall-clock is).
- **Fingerprint re-baseline is approved.** Splitting the config component changes
  every profile hash, so the current profiles become a legacy cohort. This is
  deliberate: attribution of config params is a stated goal and cannot be had
  without it.
- **No holdout split.** At 3–5 tasks a holdout is noise. Confirmation is by
  replication instead.
- **Task source**: harvest from the user's own OpenCode history.

## The detectable-effect envelope

At 2 arms × 5 repeats × 3–5 tasks, from the observed spreads:

| Axis | Detectable | Consequence |
|---|---|---|
| tokens, cache read | ~2% | tune this finely |
| cache hit rate | ~2 points | tune this finely |
| wall-clock | ~16% | gross changes only |
| quality | nothing | no signal exists to detect until the corpus is recalibrated |

The instrument at this budget is a **cost-and-context optimiser**. That is
legitimate; it is simply not a quality benchmark, and no result may be reported
as one.

## Global Constraints

- Standard library first; no new direct dependency without a reason that
  survives review.
- Statistics are derived at read time and never persisted.
- Every slice ends with all four gates: `go test ./... -count=1`, `go vet ./...`,
  `gofmt -l cmd internal web` (must print nothing), `make cross`.
- One commit per slice, subject `feat:`/`fix:`/`docs:`, lowercase summary.
- A behaviour change without a test that failed first is not finished.
- Do not disturb `spec_hash`: `Task.Name`, `Task.Tags` and `ExpectedTokens` are
  deliberately excluded from the task hash.

## Critical path

B → C → D → E. F and G follow.

---

### Task B: Noise and power, the first tool used

The prerequisite for every other result: without it a delta cannot be read.

- `internal/stats`: from a sample, the standard error, the half-width of the
  interval, and the **minimum detectable effect** at a stated power; and the
  repeats needed to detect a given effect. Seeded and deterministic like
  `BootstrapCI`.
- `internal/history`: a `Variance` read model over repeated `(task, profile)`
  runs — per axis (tokens, cache reads, tool calls, duration, score) the n, mean,
  spread and implied MDE; plus per-task coverage, so a task with n=1 is visibly
  unsupportable rather than quietly averaged.
- `ocbench variance [suite] [task...]`: renders it, and states plainly when no
  ranking is supportable.
- Overview: show **n** and the MDE beside each score, and gate the ranking
  verdict on n. The existing "not distinguishable" verdict becomes a gate rather
  than a footnote.

*Acceptance*: on the current store the report states that 6 of 9 tasks have n=1
and that no ranking is supportable; a test pins the MDE arithmetic against a
known sample; the Overview shows n per profile.

*Risks*: a spread computed from n=2 is itself noise — the report must label
small-n spreads as indicative, never as an estimate.

---

### Task C: Config attribution (approved re-baseline)

- Split `configCatchAll` into named components: `command/<name>`, `lsp/<lang>`,
  `formatter/<name>`, `provider/<id>`, `mode/<name>`, `compaction`, `share`,
  `autoupdate`, `tools`.
- Extend `describeChange` per new kind so a diff names the param
  (`compaction.prune on→off`) rather than saying "configuration".
- Land in one commit: a partial split changes the hash twice.

*Acceptance*: flipping one config param produces a note naming that param and
nothing else; a test pins a config-only change to a config-scoped note.

*Risk*: every existing profile hash changes. The corpus must be re-run once
afterwards; old runs stay readable but are not comparable.

---

### Task D: Cache hit rate, and honest cache writes

- Derive `cache_hit_rate = cache_read / (cache_read + tokens_input)` at read
  time; surface per run and per profile.
- Render `tokens_cache_write` as "not reported by OpenCode" rather than `0`, and
  document `tokens_total = input + output + reasoning + cache_read`.

*Acceptance*: hit rate appears in the run aside and the profile leaderboard; a
test pins the derivation including the divide-by-zero case; no surface presents
cache-write zero as a measurement.

---

### Task E: Discriminative power and corpus calibration

- Classify each task by pass rate across runs: informative (~0.2–0.8), saturated
  (>0.9), always-fail (<0.2).
- Process-signature screen: tasks where every config produces the same tool-call
  sequence cannot discriminate, however hard they are. The data already exists in
  `run_metrics`.
- Move saturated tasks into a `regression` tier: guards, not evidence.

*Acceptance*: a report ranks all 14 tasks by discriminating power and flags most
as uninformative today.

---

### Task F: Harvest tasks from the user's own history

Derive candidates from real OpenCode sessions, with the human's commit as the
reference solution; prove one end to end through the existing fail-before/
pass-after honesty test.

*Risk*: session history is private. Tasks must be extracted into the fixture
format without carrying unrelated content into a public repository.

---

### Task G: Confirmation by replication

Record the arm count in the experiment report so a winner among many arms is
visibly a multiple-comparison risk, and require a replication at a later time
before calling a winner.

---

## Out of scope

- The Suites & tasks dashboard page (Phase 4). It would faithfully render a
  corpus that cannot answer the question. Revisit after E.
- An LLM judge. It injects more variance than it resolves.
- Changes to `experiment.BuildPlan`: arm interleaving is already correct.
