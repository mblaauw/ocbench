# Working in this repository

`ocbench` is a Go 1.26, CGO-free single binary. The binding design is
[docs/design.md](docs/design.md); outstanding work is
[docs/roadmap.md](docs/roadmap.md); past decisions and their trade-offs are
[docs/decisions.md](docs/decisions.md).

## Gates

Every change must pass all four before it is committed:

```sh
go test ./... -count=1
go vet ./...
gofmt -l cmd internal web        # must print nothing
make cross                       # CGO_ENABLED=0 linux amd64 + arm64
```

`make build` produces `bin/ocbench`; `make lint` runs format and vet.

## Workflow

- **Test first.** Write the failing test, watch it fail for the right reason,
  then implement the minimum that passes. A behaviour change without a test that
  failed first is not finished.
- **One commit per task.** Stage only the files the task owns. Subjects follow
  `feat:`, `fix:`, `docs:`, `test:` or `chore:` with a lowercase summary, for
  example `fix: bound post-run context by max(task timeout, 5s)`.
- **Do not commit** `.worktrees/`, `bin/`, `dist/`, databases or scratch
  directories; they are gitignored.
- **Update the docs with the code.** A new command goes in the README table; a
  decision with a trade-off goes in `docs/decisions.md`; work that is deferred
  goes in `docs/roadmap.md` under technical debt, not in a comment.

## Testing rules

- Tests use `t.TempDir()`, never a real user directory, the network, or the real
  `opencode` binary.
- Adapter behaviour is faked with the helper-process convention: a test binary
  re-executes itself behind `GO_WANT_HELPER_PROCESS`-style guards.
- CLI tests inject `cli.Deps` with a temp `config.Paths`, a fake adapter and the
  embedded suite FS.
- Golden fixtures are copied verbatim and never edited; a test that reads a
  source-tree path (for example the embedded/on-disk suite parity check) says so
  in a comment.

## Dependencies

- Standard library first. Adding a direct dependency needs a reason that would
  survive review, and `go mod vendor` must be re-run so `vendor/` stays in sync.
- No JavaScript build step, no CDN assets, no template engine beyond
  `html/template`; the dashboard is embedded with `//go:embed`.

## Design constraints worth remembering

- A run must never write to the user's OpenCode data directory, and a benchmark
  child sees only the sandbox allowlist plus explicit overlay variables.
- Fixture repositories are content-addressed: the same fixture must produce the
  same baseline commit SHA on any machine.
- `success` is binary; partial credit is `score`. Statistics are derived at read
  time, never stored.
- Answer validators see only the final text event; process validators may only
  check something the task prompt asks for.
