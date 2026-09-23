# Read-only Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add a loopback-only `ocbench serve` dashboard for safe benchmark history, run summaries, profile summaries, and comparisons.

**Architecture:** Build on Plan 3’s `internal/history` read model. `internal/web` owns embedded stdlib HTTP handlers and templates; `internal/cli/serve.go` owns lifecycle, listen validation, store setup, and graceful shutdown.

**Tech Stack:** Go stdlib `net/http`, `html/template`, `embed`; no JavaScript build, CDN, or new dependency.

**Spec:** `docs/superpowers/specs/2026-09-22-ocbench-design.md` (§§3, 6, 10).

## Global Constraints

- Plan 3 must be complete first; reuse its query and comparison types rather than duplicating SQL or selector logic.
- Read-only dashboard: no run creation, mutation endpoints, raw event/session/prompt artifact serving, downloads, or arbitrary filesystem paths.
- Listen only on loopback (`127.0.0.1`, `::1`, or `localhost`); reject all other hosts because no authentication exists.
- Default listen address remains `config.server.listen` (`127.0.0.1:8787`).
- Use `//go:embed` for all templates/styles; no external assets.
- Responses set `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, and a restrictive CSP.
- Tests use `httptest`, temp SQLite data, and no real OpenCode/network.

## Review Focus

- A non-loopback bind must fail before opening a listener.
- Route/path traversal and raw-artifact URLs must return 404 and never read the filesystem.
- HTML must escape database/profile/validation content; no template injection.
- Server shutdown on SIGINT/SIGTERM must close promptly without corrupting the SQLite store.
- SQLite busy errors must yield a bounded 503-style response rather than a hanging request.

### Task 1: Embedded web foundation

**Files:**
- Create: `internal/web/embed.go`, `internal/web/server.go`, `internal/web/server_test.go`
- Create: `web/templates/base.html`, `web/templates/runs.html`, `web/templates/run.html`, `web/templates/compare.html`, `web/templates/profile.html`, `web/static/site.css`

**Consumes:** `history.List` and `history.Compare` from Plan 3.

- [ ] Write failing `httptest` cases for `/` returning HTML, security headers, escaped content, and unknown path 404.
- [ ] Run `go test ./internal/web -count=1` and observe failures.
- [ ] Implement `NewHandler(st *store.Store) http.Handler` with embedded templates/static CSS. Keep all data view models explicit; expose only run metadata, metrics, validation excerpts, changed-file summaries, diff statistics, and redacted profile component summaries.
- [ ] Re-run `go test ./internal/web -count=1`.
- [ ] Commit: `feat: add embedded read-only web foundation`.

### Task 2: Dashboard routes and safe summaries

**Files:**
- Modify: `internal/web/server.go`, `internal/web/server_test.go`

- [ ] Write failing tests for `/runs/{uuid}`, `/compare?a=<selector>&b=<selector>`, `/profiles/{hash}`, invalid IDs, incompatible comparisons, and forbidden paths such as `/artifacts/...`, `/events.jsonl`, `/../ocbench.db`.
- [ ] Run `go test ./internal/web -count=1` and observe failures.
- [ ] Implement routes using Plan 3 service methods. `GET /` lists recent runs; `GET /runs/{id}` shows safe result summary; `GET /compare` mirrors CLI comparison; `GET /profiles/{hash}` renders redacted component information. Do not add a generic file handler.
- [ ] Map `sql.ErrNoRows` to 404, selector usage errors to 400, and SQLite busy/timeout errors to 503.
- [ ] Re-run `go test ./internal/web -count=1`.
- [ ] Commit: `feat: add dashboard run and comparison views`.

### Task 3: Serve CLI and lifecycle

**Files:**
- Create: `internal/cli/serve.go`, `internal/cli/serve_test.go`
- Modify: `internal/cli/root.go`

- [ ] Write failing tests for default/config listen resolution, `--listen`, non-loopback rejection as `UsageError`, and context cancellation shutting down an injected `http.Server`.
- [ ] Run `go test ./internal/cli -run TestServe -count=1` and observe failures.
- [ ] Implement `ocbench serve [--listen ADDRESS]`: resolve deps, ensure dirs, open/migrate store, validate `net.SplitHostPort` + loopback host, construct `http.Server`, and call `Shutdown` with a five-second context when command context ends.
- [ ] Re-run `go test ./internal/cli -run TestServe -count=1`.
- [ ] Commit: `feat: add loopback dashboard server command`.

### Task 4: End-to-end verification and docs

**Files:**
- Modify: `docs/superpowers/specs/2026-09-22-ocbench-design.md` only if an approved implementation detail differs.

- [ ] Run `go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web`, and `make cross`.
- [ ] Start `ocbench serve --listen 127.0.0.1:0` against a temp seeded DB, fetch `/`, `/runs/<id>`, and `/compare`, then cancel it; assert no listener remains.
- [ ] Verify responses never contain prompt text, `session.json`, `events.jsonl`, or arbitrary artifact file content.
- [ ] Review all routes for loopback enforcement and absence of mutation handlers.
