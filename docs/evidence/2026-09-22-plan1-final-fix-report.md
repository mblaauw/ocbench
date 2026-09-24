# Final whole-branch review fix wave — ocbench Plan 1

- Branch: `plan-1-foundation`
- Base: `93b20aa` (feat: add snapshot command with profile diff output)
- Commit: `c30dcb9` — `fix: close final review findings for plan 1`
- Agent: opencode-go/deepseek-v4.1-flash

## Summary

Six findings fixed in one commit, one file-scope of intent per finding. TDD was
used for findings 1–5; each new behavioural test was confirmed RED against the
pre-fix behaviour and GREEN with the fix (evidence below). Finding 6 (Makefile)
has no sensible RED and is proven post-fix by `make fmt` no longer dirtying the
vendored `pflag` tree.

## Finding 1 — Usage errors exit 2 (spec §8)

**Changes** (`internal/cli/root.go`, `version.go`, `doctor.go`, `snapshot.go`,
`root_test.go`):
- Added `type UsageError struct{ Err error }` with pointer-receiver `Error()`
  and `Unwrap()`.
- `NewRootWithDeps` installs `root.SetFlagErrorFunc(...)` wrapping flag-parse
  errors in `*UsageError` (cobra walks the parent chain, so subcommands inherit
  it).
- New `usageArgs(cobra.PositionalArgs)` wrapper; `version`, `doctor` and
  `snapshot` now use `Args: usageArgs(cobra.NoArgs)`.
- `execute` calls `root.InitDefaultHelpCmd()` then `root.Find(args)`, returning
  `&UsageError{Err: err}` when target resolution fails. `InitDefaultHelpCmd` is
  required so `ocbench help` is not misclassified as an unknown subcommand.
- Added `exitCode(err)`: nil → 0, `errors.As(&UsageError)` → 2, else 1.
  `Execute()` now returns `exitCode(err)` instead of always 1.

**Tests** (`internal/cli/root_test.go`): `TestExecuteUnknownFlagIsUsageError`
(`snapshot --nope`), `TestExecuteUnknownSubcommandIsUsageError` (`bogus`),
`TestExecuteRejectsPositionalArgsAsUsageError` (`version extra`), and
`TestExitCode` (nil/plain/usage/wrapped-usage). Existing doctor tests untouched.

**RED/GREEN evidence**: the classification mechanism was temporarily disabled
(`execute` returning `root.ExecuteContext(ctx)` directly). While disabled, the
unknown-subcommand test failed (the error was not a `UsageError`); after restore
all four tests pass:

```
=== RUN   TestExecuteUnknownFlagIsUsageError
--- PASS: TestExecuteUnknownFlagIsUsageError (0.00s)
=== RUN   TestExecuteUnknownSubcommandIsUsageError
--- PASS: TestExecuteUnknownSubcommandIsUsageError (0.00s)
=== RUN   TestExecuteRejectsPositionalArgsAsUsageError
--- PASS: TestExecuteRejectsPositionalArgsAsUsageError (0.00s)
=== RUN   TestExitCode
--- PASS: TestExitCode (0.00s)
```

## Finding 2 — Model sandbox mode in the profile (spec §5.1)

**Changes**: `profile.Options` gains `SandboxMode string`
(`internal/profile/sources.go`). `buildPrimary` sets `environment.sandbox` to
`opts.SandboxMode` when non-empty, else `"default"`. `snapshot.go` derives the
value from the effective config: `cfg.Sandbox.InheritEnvironment` → `"inherit"`,
otherwise `"default"`.

**Test**: `TestFingerprintSandboxModeChangesEnvironment` asserts the
`environment` component hash and the overall hash differ between `""`/default
and `"inherit"`, and that the component JSON records `default`/`inherit`.
No standalone RED run: the test does not compile against the pre-fix API
(`SandboxMode` field absent), which is the RED signal.

## Finding 3 — Recursive permission canonicalisation

**Changes**: `canonicalizePermission` now delegates to a new recursive
`sortPermissionArrays`, which descends through every map and array and sorts
each array by the canonical JSON of its (recursively canonicalised) elements.
Object key order remains deterministic via `canon.JSON`.

**Test**: `TestFingerprintNestedPermissionOrderIndependent` uses a global
permission object with a nested rule array plus an agent permission with nested
arrays, shuffling both.

**RED/GREEN evidence**: with the old top-level-only behaviour emulated (map
values returned untouched), the test failed:

```
--- FAIL: TestFingerprintNestedPermissionOrderIndependent
    fingerprint_test.go:318: profile hash depends on nested permission order:
         acbf04b6...de3d6ca7238
         89f4c65e...691e627ddd9ed8e
```

After restore it passes (part of the package run below).

## Finding 4 — MCP environment values must not reach the raw capture

**Changes**: the resolved-config capture is now built from `configCapture(cfg)`
instead of `cfg`. `configCapture` shallow-copies the config and replaces every
`mcp.<name>.environment` value map with its sorted key-name list (keeping the
`environment` field), sharing the `environmentKeyNames` projection with the
`mcp/<name>` component. The hashed structure and the `mcp/<name>` component keep
their existing `environment_keys` semantics; `cfg` itself is not mutated.

**Test**: `TestFingerprintMCPEnvironmentValuesNotCaptured` uses an MCP entry
with a non-sensitive `DB_DSN` value; it asserts the capture contains `DB_DSN`
but not the value, and that rotating the value leaves the profile hash
unchanged.

**RED/GREEN evidence**: with the capture reverted to `canon.JSON(cfg)`:

```
--- FAIL: TestFingerprintMCPEnvironmentValuesNotCaptured (0.00s)
    fingerprint_test.go:344: resolved-config capture leaked an MCP environment value:
    {"mcp":{"db":{"environment":{"DB_DSN":"postgres://user:pw@host/db","DB_HOST":"localhost"},"type":"local"}}}
```

After restore it passes.

## Finding 5 — Raw capture byte-stability (`agents.json`)

**Changes**: `buildAgentsCapture` routes each captured agent's `permission`
value through `sortPermissionArrays` (the same recursive canonicalisation as the
permissions component) before serialising. Other agent fields are untouched.

**Test**: `TestFingerprintAgentsCapturePermissionOrderStable` fingerprints two
sources whose build-agent permission array is shuffled and asserts byte-identical
`Captures.Agents`.

**RED/GREEN evidence**: with the canonicalisation disabled (`&& false` guard):

```
--- FAIL: TestFingerprintAgentsCapturePermissionOrderStable (0.06s)
    fingerprint_test.go:381: agents.json depends on permission order:
        ...[{"action":"ask","pattern":"*","permission":"bash"},{"action":"allow","pattern":"*","permission":"edit"}]...
        ...[{"action":"allow","pattern":"*","permission":"edit"},{"action":"ask","pattern":"*","permission":"bash"}]...
```

After restore it passes.

## Finding 6 — Makefile fmt scope

**Changes**: `fmt:` now runs `gofmt -l -w ./cmd ./internal` instead of
`gofmt -l -w .`, so the vendored `github.com/spf13/pflag` sources are no longer
rewritten. No sensible RED test; proven post-fix by the porcelain check below.

## Validation (exact output)

`grep -rn "TEMP-RED" internal/ Makefile` → no matches (exit 1). A second scan
for `TEMP|DEBUG|TODO|FIXME|fmt\.Print|println\(` over the changed files matched
only pre-existing, legitimate `fmt.Fprintln` CLI output calls.

`go test ./... -count=1`:

```
?   	mbl/ocbench/cmd/ocbench	[no test files]
ok  	mbl/ocbench/internal/canon	0.279s
ok  	mbl/ocbench/internal/cli	0.612s
ok  	mbl/ocbench/internal/config	0.398s
ok  	mbl/ocbench/internal/doctor	0.850s
ok  	mbl/ocbench/internal/opencode	1.312s
ok  	mbl/ocbench/internal/profile	1.957s
ok  	mbl/ocbench/internal/store	1.813s
ok  	mbl/ocbench/internal/version	1.328s
```

`go vet ./...` → exit 0, no output.

`gofmt -l cmd internal` → no files listed, exit 0.

`make fmt` (runs `gofmt -l -w ./cmd ./internal`) → exit 0. `git status
--porcelain` before and after `make fmt` was byte-identical and listed only the
nine intended paths (`Makefile`, `internal/cli/{root.go,doctor.go,snapshot.go,
version.go,root_test.go}`, `internal/profile/{fingerprint.go,
fingerprint_test.go,sources.go}`); no `vendor/` paths appeared, proving the fmt
target no longer dirties vendored pflag.

Post-commit `git status --porcelain` → empty.

## Concerns

- **Finding 1 RED evidence** is the unknown-subcommand test failing while the
  classification was disabled; the flag/positional variants share the same
  mechanism and are covered GREEN.
- **`__complete` edge**: initially `execute` resolved the target before cobra
  registered its hidden completion command, so completion invocations were
  reported as unknown subcommands. Resolved in the follow-up correction below.
- **Finding 4 shape**: the resolved-config capture keeps the `environment` key
  but replaces its map value with the sorted key-name list, rather than renaming
  the field to `environment_keys`. This preserves the captured config's key path
  while removing all values; the `mcp/<name>` component keeps
  `environment_keys` unchanged as required.

## Follow-up correction — keep completion commands out of usage-error classification

Scoped re-review confirmed all six findings addressed but reported one new
Important regression from finding 1: the `root.Find(args)` pre-check ran before
cobra registers its default completion commands (they are added inside
`ExecuteC`), so `ocbench completion bash` and the hidden `ocbench __complete ...`
/ `__completeNoDesc ...` protocol were classified as unknown subcommands and
exited 2.

**Changes** (`internal/cli/root.go`, `internal/cli/root_test.go` only):
- `execute` now calls `root.InitDefaultCompletionCmd()` after
  `root.InitDefaultHelpCmd()` and before the `Find` pre-check, so the advertised
  `completion` command resolves.
- The pre-check is skipped for the hidden completion protocol:
  `if len(args) > 0 && strings.HasPrefix(args[0], "__complete") { return
  root.ExecuteContext(ctx) }`, leaving cobra to register and run it.
- Added `TestCompletionCommandsAreNotUsageErrors` with three subtests: the
  advertised `completion bash` command renders non-empty output (built via
  `NewRootWithDeps` with injected temp deps and captured with `cmd.SetOut`);
  `execute(ctx, {"completion","bash"}, deps)` returns no error; and
  `execute(ctx, {"__complete","snapshot",""}, deps)` returns no `UsageError`. A
  `silenceOutput` helper redirects the process streams (the completion protocol
  writes directly to them) to a temp file so test logs stay clean.

**RED/GREEN evidence**: with `InitDefaultCompletionCmd()` temporarily removed,
the completion subtest failed as expected, reproducing the reported regression:

```
--- FAIL: TestCompletionCommandsAreNotUsageErrors (0.00s)
    --- FAIL: TestCompletionCommandsAreNotUsageErrors/execute_does_not_misclassify_completion (0.00s)
        root_test.go:87: completion bash: unknown command "completion" for "ocbench", want no error
```

After restore all subtests pass.

**Validation**:

`go test ./internal/cli/ -count=1` → `ok  mbl/ocbench/internal/cli  0.261s`.

`go test ./... -count=1`:

```
?   	mbl/ocbench/cmd/ocbench	[no test files]
ok  	mbl/ocbench/internal/canon	0.285s
ok  	mbl/ocbench/internal/cli	1.192s
ok  	mbl/ocbench/internal/config	0.424s
ok  	mbl/ocbench/internal/doctor	0.751s
ok  	mbl/ocbench/internal/opencode	1.204s
ok  	mbl/ocbench/internal/profile	1.848s
ok  	mbl/ocbench/internal/store	1.738s
ok  	mbl/ocbench/internal/version	1.946s
```

`go vet ./...` → exit 0, no output. `gofmt -l cmd internal` → no files listed,
exit 0. No `TEMP-RED` markers remain.

---

**Note on paths and process.** This record was written while the project used a
different workflow and directory layout; mentions of `.superpowers/sdd/` scratch
files and of a "verifier subagent" describe how the check was run at the time,
not a process the repository depends on.
