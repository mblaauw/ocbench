# Task 9 — Independent Verification Report (Plan 1: ocbench foundation)

- **Repository:** `/Users/mich/dev/mbl-ocbench`
- **Branch / HEAD:** `plan-1-foundation` @ `93b20aad6a7e38b3e5e92cb3c9b9adc37660a893` (short `93b20aa`)
- **Date:** 2026-09-22
- **Verifier:** independent verification pass; no code changed, no commits, nothing deleted.
- **Scope:** `ocbench` binary with `version`, `doctor`, `snapshot`; spec §5 (profile rules) and §11 (verification strategy).
- **Method:** every result below was produced by a command actually run in this session. The implementer's report was not treated as evidence. Where the brief's expectation and the observed behaviour diverge, it is recorded as a FAIL / counter-evidence rather than repaired.

## Complete check table

| # | Check | Command | Observed result | Verdict |
|---|-------|---------|-----------------|---------|
| 1a | Build | `go build ./...` | exit 0, no output | PASS |
| 1b | Vet | `go vet ./...` | exit 0, no output | PASS |
| 1c | Unit tests | `go test ./... -count=1` | all 8 packages `ok` (`canon`, `cli`, `config`, `doctor`, `opencode`, `profile`, `store`, `version`); cmd/ocbench has no test files; exit 0 | PASS |
| 1d | Formatting | `gofmt -l cmd internal` | no files listed; exit 0 | PASS |
| 1e | Cross-compile | `make cross` then `file dist/ocbench-linux-amd64 dist/ocbench-linux-arm64` | both built exit 0. `dist/ocbench-linux-amd64: ELF 64-bit LSB executable, x86-64, statically linked, stripped`; `dist/ocbench-linux-arm64: ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped` (CGO_ENABLED=0) | PASS |
| 2 | Profile determinism | `go run ./cmd/ocbench snapshot --json` ×3 | all three exit 0; identical `profile.hash = aed5065e2d6b37fa5720043c09323e54a702a56bdf80054181363282920bb545`; `created:false` each time; byte-identical JSON (diff clean); `select count(*) from profiles` = 1; `select count(*) from profile_changes` = 0 | PASS |
| 3 | Override change detection | `go run ./cmd/ocbench snapshot --variant low` then `--variant high` | low: id `14027d19…`, hash `16d369a92fc615467cda91cb11aa22c916d1442f0fb4f3690667cc5b51fb60fb`, `created:true`. high: id `32ed2756…`, hash `c4135090e0adb581e06dc74cd7e55f7cff898437eb5d806987885bbaab85e276`, `created:true`. Each transition recorded exactly 2 changed components: `environment` and `primary`. DB now has 3 profiles / 4 `profile_changes` rows; printed from_hash/to_hash match the DB rows exactly. Exactly 2 changed components is expected from the code path (`buildPrimary` in `internal/profile/fingerprint.go` puts the variant override into `primary.variant` and `environment.overrides.variant` only; no agent/config/instructions/skill component depends on `--variant`). No unexpected component churn observed. | PASS |
| 4 | Secret-leakage audit | grep captures + DB (see dedicated section) | No real credential found. Nonzero pattern counts only in `skills.json` (documentation fragments); auth.json literal/fragment probes = 0 matches in captures and 0 in DB. | PASS |
| 5a | No duplicate profile for unchanged env | 2 further `go run ./cmd/ocbench snapshot --json` | both `created:false`, hash `aed5065e…`; profiles stayed 3, `profile_changes` stayed 4 (no new rows). | PASS |
| 5b | Fake-adapter isolation | `OCBENCH_OPENCODE_BIN=/nonexistent go test ./... -count=1` | all 8 packages `ok`; exit 0. Tests do not reach the real binary. | PASS |
| 5c | Doctor fails cleanly with no opencode | `go build -o /tmp/ocbench-verify ./cmd/ocbench` then `PATH=/usr/bin:/bin OCBENCH_OPENCODE_BIN=/nonexistent /tmp/ocbench-verify doctor --json` | exit 1; well-formed JSON, no panic: `opencode` → `fail` ("binary \"opencode\" not found"), `git` → ok, `db` → ok (schema version 1), `config` → ok (missing, defaults), `profile` → `fail` ("opencode --version: executable file not found"), `mcp` → `warn`, `sandbox` → ok; trailer `error: doctor: environment is not healthy`. | PASS |
| 5d.1 | Reject unknown flag | `go run ./cmd/ocbench snapshot --nope; echo "exit=$?"` | `error: unknown flag: --nope`; `exit=1` (same with built binary). | PASS |
| 5d.2 | Bare invocation exits nonzero | `go run ./cmd/ocbench; echo "exit=$?"` | prints full help (Usage / Available Commands / Flags) and **`exit=0`**. Confirmed with the built binary `/tmp/ocbench-verify` too: `exit=0`. The brief's check requires help **and a nonzero exit**. | **FAIL** |

**Totals: 12 PASS, 1 FAIL, 0 UNVERIFIED** (5d split into two assertions; 5d.2 is the only failure).

## Secret-leakage audit — detail

### Pattern counts per file class (captures)

| File | sk- | glpat- | `Bearer ` | apiKey | api_key | ANTHROPIC_API_KEY | OPENAI_API_KEY |
|------|-----|--------|-----------|--------|---------|-------------------|----------------|
| `resolved-config.json` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `agents.json` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `snapshot.json` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `instructions.json` | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `skills.json` | 27 | 0 | 4 | 1 | 0 | 0 | 0 |

Identical counts for each of the three profile directories (`aed5065e…`, `16d369a9…`, `c4135090…`). All profile files are mode 644; no secrets.

### DB pattern counts

`profiles.canonical_json` (all 3 rows) and every `profile_components.canonical_json` row: **0** for all seven patterns.

### Explanation of the nonzero `skills.json` counts (all false positives)

- `sk-` (27): matches are substrings of ordinary words in skill documentation, e.g. `task-specific`, `task-done`, `task-reviewer`, `task-start`, `task-by-task`, `risk-…`. The longest match is `sk-reviewer` (11 chars); no OpenAI/Anthropic-shaped key is present.
- `Bearer ` (4): documentation placeholders — `"Authorization": "Bearer ..."`, `Bearer {env:GITHUB_TOKEN}`, `Bearer $RUNWARE_API_KEY`.
- `apiKey` (1): example JSON `"anthropic": { "options": { "apiKey": "..." } }` in a skill's docs.

None of these is a real credential.

### auth.json literal-content probe

`~/.local/share/opencode/auth.json` was parsed (contents never printed). 9 distinct leaf string values ≥8 chars (anthropic/xai/openai/opencode-go/openrouter/google key or token material), expanded to 2829 probe fragments (each full value plus every 16-char sliding window). Matching those against:

- all files in all three profile capture directories → **0 matches**
- `profiles.canonical_json` (3 rows) → **0 matches**
- `profile_components.canonical_json` → **0 matches**

No real credential value leaks into captures or the database. Redaction `(?i)(api[_-]?key|token|secret|password|passwd|credential|authorization|cookie)` and env-name-only recording are effective for the tested environment.

## State note (evidence preservation)

- Before checks: 1 profile (`2b6c192a…` / hash `aed5065e…`), 1 profile/0 `profile_changes`, 67 component rows, DB mtime from the prior implementer run.
- After checks: 3 profiles — base `aed5065e…` plus `16d369a9…` (variant low) and `c4135090…` (variant high) — and 4 `profile_changes` rows (2 per variant transition). Two repeat snapshots added no rows.
- `/Users/mich/.local/share/ocbench` profiles and DB were **not deleted**; nothing under it was removed.
- `git status --porcelain` was empty before and after all checks. **No tracked file was modified, no commit was created.** This report lives under gitignored `.superpowers/sdd/` (`.superpowers/sdd/.gitignore` contains `*`), so writing it does not alter the tracked tree.

## Overall verdict

**12 PASS, 1 FAIL, 0 UNVERIFIED.** Plan 1's build gates, test suite, cross-compilation, profile determinism, override change detection, redaction/secret handling, and adapter isolation all hold under independent execution. One counter-evidence item was found:

1. **FAIL — bare `ocbench` exits 0 instead of nonzero.** `go run ./cmd/ocbench` (and `/tmp/ocbench-verify`) prints help and returns exit code 0, whereas the Task 9 verification checklist requires help **and a nonzero exit** (and its stdin is piped when invoked programmatically, so a zero exit from a no-argument invocation is easy to miss). Root cause is standard cobra behaviour: the root command defines subcommands and a help template but no `RunE` returning a non-nil error, so cobra prints help and returns nil. Note the design spec §10 does not itself mandate the exit code, so this is a contract mismatch between the verification checklist and the implementation, not a spec violation; it is reported, not repaired, per instructions.

No other counter-evidence. `doctor` degrades cleanly (exit 1, structured JSON, no panic) when the opencode binary is unavailable; unknown flags are rejected with exit 1.
