# Plan 4 Task 4 — HTTP smoke verification

- Worktree: /Users/mich/dev/mbl-ocbench/.worktrees/plan-3-history-compare
- HEAD: 27bd9620a77c91f97f5df02b49b41cdd9e562a61
- Date (UTC): 2026-09-23T18:22:30Z
- Verifier: read-only; no source edits, no commits.

## Gates (prior run, same HEAD, clean tree)

```
$ go test ./... -count=1        # EXIT 0, all packages ok
$ go vet ./...                  # EXIT 0
$ gofmt -l cmd internal web     # empty, EXIT 0
$ make cross                    # EXIT 0, dist/ocbench-linux-{amd64,arm64} static ELF
```

## Non-loopback rejection (fresh home each case)

```
$ ocbench serve --listen 0.0.0.0:0        -> exit 2, db_created=no, "listen host \"0.0.0.0\" is not a loopback address"
$ ocbench serve --listen 192.168.1.5:8787 -> exit 2, db_created=no
$ ocbench serve --listen :8787            -> exit 2, db_created=no, "listen address \":8787\" has no host"
$ ocbench serve --listen example.com:8787 -> exit 2, db_created=no
$ ocbench serve --listen 10.0.0.1:8787    -> exit 2, db_created=no
```

## Seed data (temp DB, sensitive artifacts)

- Home: /var/folders/9r/1qfn68ds57jd6hcmhn32j47c0000gn/T/opencode/plan4-verify.zB68uJ/seed (DB created by 'ocbench history' -> migrated)
- 1 profile, 2 compatible non-dry runs (run-old-0001, run-new-0002), 10 metrics, 2 validations.
- runs.session_id = SENTINEL_SESSION_ID_abc123; runs.artifacts_dir = temp run dir.
- Sensitive files: prompt.md, session.json, events.jsonl, artifact.bin under run dir; secret-outside.txt above it.
- Validation excerpt on run-new-0002 contains raw <script>alert(1)</script> to probe escaping.

## Server start

```
$ OCBENCH_HOME=.../seed XDG_CONFIG_HOME=.../cfg ocbench serve --listen 127.0.0.1:0 &
ocbench dashboard listening on http://127.0.0.1:61667   # PID 70070
$ curl -o /dev/null -w "%{http_code}" http://127.0.0.1:61667/  -> 200 (preflight)
```

## Request matrix (curl -s --path-as-is -o body -w "%{http_code}")

```
/                                                             200
/runs/run-old-0001                                            200
/runs/run-new-0002                                            200
/compare?a=run-old-0001&b=run-new-0002                        200
/profiles/seedprofilehash0000...0001                          200
/artifacts/run-old-0001/prompt.md                             404
/events.jsonl                                                 404
/runs/run-old-0001/events.jsonl                               404
/runs/run-old-0001/prompt.md                                  404
/runs/run-old-0001/session.json                               404
/runs/../ocbench.db                                           404
/../secret-outside.txt                                        404
/%2e%2e/secret-outside.txt                                    404
/runs/%2e%2e/ocbench.db                                       404
/runs/..%2focbench.db                                         404
/static/../ocbench.db                                         404
/runs/run-old-0001/                                           404
POST /                                                        404
POST /runs/run-old-0001                                       404
PUT /compare?a=run-old-0001&b=run-new-0002                    404
DELETE /runs/run-old-0001                                     404
```

Note: mutation methods return 404 (not 405) because the no-method catch-all `/` handler wins; there are no mutation handlers and the surface stays read-only.

## Security headers (GET /)

```
Cache-Control: no-store
Content-Security-Policy: default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff
```

## Leakage / escaping (grep -F -c over all response bodies, 6530+ bytes)

```
SENTINEL_PROMPT_VALUE_9f3a           count=0
SENTINEL_SESSION_FILE_VALUE_c0ffee   count=0
SENTINEL_EVENT_VALUE_deadbeef        count=0
SENTINEL_ARTIFACT_VALUE_b00b00       count=0
SENTINEL_OUTSIDE_VALUE_outside       count=0
SENTINEL_SESSION_ID_abc123           count=0
raw  <script>alert(1)</script>       count=0
escaped &lt;script&gt;alert(1)&lt;/script&gt;  count=1
SAFE_EXCERPT_OLD                     count=1
SAFE_EXCERPT_NEW                     count=1
tokens_total                         count=2
```

Escaped context observed:

```
...<td>1500 ms</td><td><pre>&lt;script&gt;alert(1)&lt;/script&gt;SAFE_EXCERPT_NEW</pre></td>...
```

## Shutdown / listener refusal

```
$ nc -z -w 2 127.0.0.1 61667        # pre:  succeeded, exit 0
$ kill -INT 70070                   # SIGINT sent
process_exited
$ nc -z -w 2 127.0.0.1 61667        # post: exit 1 (refused)
$ curl http://127.0.0.1:61667/      # exit 7 (connection refused), http=000
serve.log: only "ocbench dashboard listening on http://127.0.0.1:61667" (no error)
$ sqlite3 .../ocbench.db "PRAGMA integrity_check;"  -> ok
$ sqlite3 .../ocbench.db "SELECT count(*) FROM runs;" -> 2
```

## Verdict

- Gates: PASS (go test, go vet, gofmt, make cross)
- Non-loopback rejection before DB creation: PASS
- Safe pages 200 / forbidden+traversal 404: PASS
- No sentinel leakage, HTML escaping: PASS
- SIGINT graceful shutdown + listener refusal + store integrity: PASS
- GET-only / no mutation handlers: PASS (mutation methods 404, not 405 — noted)
- Unproven: none for the requested Task 4 checks.

---

**Note on paths and process.** This record was written while the project used a
different workflow and directory layout; mentions of `.superpowers/sdd/` scratch
files and of a "verifier subagent" describe how the check was run at the time,
not a process the repository depends on.
