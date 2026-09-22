# ocbench Plan 1 — Foundation and Resolved Profile

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a runnable `ocbench` binary that can diagnose its environment (`doctor`) and capture, hash, persist and diff the resolved OpenCode execution profile (`snapshot`).

**Architecture:** Go module `mbl/ocbench`, single binary, stdlib-first with cobra + pure-Go SQLite + yaml.v3, all vendored. OpenCode is observed exclusively through its CLI via an `opencode.Adapter` interface whose real implementation is exercised in tests by a helper process that impersonates the `opencode` binary (no inference in tests). Profile discovery is split into IO (`profile.Discover`) and a pure fingerprinting function (`profile.Fingerprint`) so hashing rules are unit-testable without a live OpenCode.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra`, `modernc.org/sqlite`, `gopkg.in/yaml.v3`, stdlib `testing`.

**Spec:** `docs/superpowers/specs/2026-09-22-ocbench-design.md` — read it before starting any task. Sections 3–5, 10 and 11 are load-bearing for this plan.

## Global Constraints

- Module path is exactly `mbl/ocbench`.
- No CI configuration files of any kind. Local git only; no remote.
- Dependencies: only the three listed above plus stdlib. Do not add others without changing the spec.
- All commands and tests must pass on macOS (dev) and compile for `linux/amd64` and `linux/arm64` with `CGO_ENABLED=0`.
- Never store secret values: keys matching `(?i)(api[_-]?key|token|secret|password|passwd|credential|authorization|cookie)` are replaced with `"<redacted>"` before hashing and before persistence. Env values are never stored, only names.
- Identical canonical profiles must produce identical `profile_hash`. Same fixture materialised twice must produce the same baseline SHA in Plan 2 — Plan 1 already must be deterministic: `snapshot` twice with unchanged inputs yields one row.
- Code style: no comments unless they explain non-obvious intent; `gofmt` clean; `go vet ./...` clean.
- Every task ends with a commit. Commit messages: `feat: …`, `test: …`, `chore: …` (imperative, lowercase).

## Review Focus

1. **Determinism of the profile hash** — map iteration order in Go, path prefixes, timestamps and redacted-but-retained values must not leak into the hash. Any nondeterminism here silently destroys the product's core promise.
2. **Redaction completeness** — a secret nested in `mcp.*.environment`, a provider header, or a plugin config must be redacted in the stored canonical JSON *and* in raw captures. Grep the raw capture files during review of the relevant task.
3. **Adapter robustness to OpenCode output drift** — non-zero exits, partial JSON, unexpected shapes must produce actionable errors, never panics. The real binary version is 1.18.32; unknown keys must be preserved where lossless retention matters and ignored where they are noise.
4. **SQLite migration idempotence and forward compatibility** — `Migrate` twice is a no-op; `schema_migrations` records each version once; all timestamps stored as RFC3339 UTC strings; UUIDs generated locally (no dependency, use `crypto/rand`).
5. **Test isolation from real OpenCode** — no test may invoke the real `opencode` binary or touch the developer's real config; all adapter tests go through the helper process and temp dirs; `HOME`/XDG env must be faked in tests that resolve paths.

---

### Task 1: Repository scaffold, CLI skeleton, build metadata

**Files:**
- Create: `cmd/ocbench/main.go`
- Create: `internal/cli/root.go`, `internal/cli/version.go`
- Create: `internal/version/version.go`, `internal/version/version_test.go`
- Create: `Makefile`, `.gitignore`
- Existing: `go.mod` (module `mbl/ocbench` already initialised), git repo already initialised

**Interfaces:**
- Consumes: nothing.
- Produces: `internal/version.String() string`, `internal/version.Info() Info`; `cli.Execute() int`; package `internal/cli` as the home for all future commands.

- [ ] **Step 1: Write the failing version test**

`internal/version/version_test.go`:

```go
package version

import "testing"

func TestInfoDefaults(t *testing.T) {
	info := Info()
	if info.Version == "" {
		t.Fatal("Version must never be empty")
	}
	if info.Commit == "" {
		t.Fatal("Commit must never be empty")
	}
	if info.Date == "" {
		t.Fatal("Date must never be empty")
	}
}

func TestStringContainsVersion(t *testing.T) {
	Version = "1.2.3"
	Commit = "abc1234"
	if got := String(); got != "ocbench 1.2.3 (abc1234)" {
		t.Fatalf("String() = %q", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/version/ -v`
Expected: FAIL — package has no files / undefined symbols.

- [ ] **Step 3: Implement version metadata**

`internal/version/version.go`:

```go
package version

import (
	"fmt"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func current() Info {
	i := Info{Version: Version, Commit: Commit, Date: Date}
	if i.Commit == "unknown" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					i.Commit = s.Value
				}
			}
		}
	}
	if i.Commit == "" {
		i.Commit = "unknown"
	}
	return i
}

func Info() Info { return current() }

func String() string {
	i := current()
	return fmt.Sprintf("ocbench %s (%s)", i.Version, i.Commit)
}
```

- [ ] **Step 4: Run the version test to verify it passes**

Run: `go test ./internal/version/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Add the CLI skeleton**

`internal/cli/root.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func Execute() int {
	root := NewRoot()
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "ocbench",
		Short:         "Benchmark the resolved OpenCode execution profile",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}
```

`internal/cli/version.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/version"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print ocbench build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				b, err := json.MarshalIndent(version.Info(), "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
```

`cmd/ocbench/main.go`:

```go
package main

import (
	"os"

	"mbl/ocbench/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
```

- [ ] **Step 6: Add Makefile and .gitignore**

`Makefile` (recipes must be tabs):

```make
BINARY := ocbench
LDFLAGS := -s -w -X mbl/ocbench/internal/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
	-X mbl/ocbench/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) \
	-X mbl/ocbench/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build test vet fmt lint cross vendor clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ocbench

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint: fmt vet

cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/ocbench
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/ocbench

vendor:
	go mod vendor

clean:
	rm -rf bin dist
```

`.gitignore`:

```
/bin/
/dist/
*.db
*.db-shm
*.db-wal
.ocbench/
```

- [ ] **Step 7: Verify the whole repo builds, vets and tests**

Run: `go mod tidy && go get github.com/spf13/cobra@latest && go mod vendor && make build && ./bin/ocbench version && make vet && make test`
Expected: `ocbench <version> (<sha>)`; vet silent; all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: scaffold ocbench CLI with version command"
```

---

### Task 2: Configuration and XDG path resolution

**Files:**
- Create: `internal/config/config.go`, `internal/config/paths.go`, `internal/config/config_test.go`, `internal/config/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.Paths{Home, ConfigFile, DB, Profiles, Runs, Suites, Cache string}`
  - `config.Resolve(env func(string) string) Paths`
  - `config.Config{OpenCodeBin string; Sandbox Sandbox; Defaults Defaults; Server Server}`
  - `config.Sandbox{InheritEnvironment bool; PassEnv []string}`
  - `config.Defaults{TimeoutSeconds int; Repeat int}`
  - `config.Server{Listen string}`
  - `config.DefaultsConfig() Config`
  - `config.Load(paths Paths) (Config, error)`
  - `config.EnsureDirs(paths Paths) error`

- [ ] **Step 1: Write failing path-resolution tests**

`internal/config/paths_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
)

func fakeEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func TestResolveDefaultsToXDG(t *testing.T) {
	env := fakeEnv(map[string]string{
		"HOME":            "/home/u",
		"XDG_CONFIG_HOME": "/home/u/.config",
		"XDG_DATA_HOME":   "/home/u/.local/share",
		"XDG_CACHE_HOME":  "/home/u/.cache",
	})
	p := Resolve(env)
	if p.Home != "/home/u/.local/share/ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.ConfigFile != "/home/u/.config/ocbench/config.yaml" {
		t.Fatalf("ConfigFile = %q", p.ConfigFile)
	}
	if p.DB != filepath.Join(p.Home, "ocbench.db") {
		t.Fatalf("DB = %q", p.DB)
	}
	if p.Profiles != filepath.Join(p.Home, "profiles") {
		t.Fatalf("Profiles = %q", p.Profiles)
	}
	if p.Runs != filepath.Join(p.Home, "runs") {
		t.Fatalf("Runs = %q", p.Runs)
	}
	if p.Suites != filepath.Join(p.Home, "suites") {
		t.Fatalf("Suites = %q", p.Suites)
	}
	if p.Cache != "/home/u/.cache/ocbench" {
		t.Fatalf("Cache = %q", p.Cache)
	}
}

func TestResolveHonoursOCBenchHome(t *testing.T) {
	env := fakeEnv(map[string]string{
		"HOME":         "/home/u",
		"OCBENCH_HOME": "/workspace/.ocbench",
	})
	p := Resolve(env)
	if p.Home != "/workspace/.ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.DB != "/workspace/.ocbench/ocbench.db" {
		t.Fatalf("DB = %q", p.DB)
	}
	if p.ConfigFile != "/home/u/.config/ocbench/config.yaml" {
		t.Fatalf("ConfigFile must not move with OCBENCH_HOME, got %q", p.ConfigFile)
	}
}

func TestResolveFallsBackWhenXDGUnset(t *testing.T) {
	p := Resolve(fakeEnv(map[string]string{"HOME": "/home/u"}))
	if p.Home != "/home/u/.local/share/ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.Cache != "/home/u/.cache/ocbench" {
		t.Fatalf("Cache = %q", p.Cache)
	}
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL — undefined `Resolve`, `Paths`.

- [ ] **Step 3: Implement `paths.go`**

```go
package config

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Home       string
	ConfigFile string
	DB         string
	Profiles   string
	Runs       string
	Suites     string
	Cache      string
}

func Resolve(env func(string) string) Paths {
	home := env("HOME")
	configHome := env("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := env("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	cacheHome := env("XDG_CACHE_HOME")
	if cacheHome == "" {
		cacheHome = filepath.Join(home, ".cache")
	}
	data := env("OCBENCH_HOME")
	if data == "" {
		data = filepath.Join(dataHome, "ocbench")
	}
	cache := filepath.Join(cacheHome, "ocbench")
	return Paths{
		Home:       data,
		ConfigFile: filepath.Join(configHome, "ocbench", "config.yaml"),
		DB:         filepath.Join(data, "ocbench.db"),
		Profiles:   filepath.Join(data, "profiles"),
		Runs:       filepath.Join(data, "runs"),
		Suites:     filepath.Join(data, "suites"),
		Cache:      cache,
	}
}

func ResolveOS() Paths { return Resolve(os.Getenv) }

func EnsureDirs(p Paths) error {
	for _, dir := range []string{p.Home, p.Profiles, p.Runs, p.Suites, p.Cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run path tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Write the failing config-load test**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(Paths{ConfigFile: filepath.Join(t.TempDir(), "nope.yaml")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultsConfig()) {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.OpenCodeBin != "opencode" || cfg.Defaults.TimeoutSeconds != 900 || cfg.Defaults.Repeat != 1 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Sandbox.InheritEnvironment {
		t.Fatal("sandbox must default to allowlist mode")
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
}

func TestLoadParsesYAMLAndFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	body := "opencode_bin: /usr/local/bin/opencode\nsandbox:\n  pass_env:\n    - NODE_EXTRA_CA_CERTS\ndefaults:\n  timeout_seconds: 120\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Paths{ConfigFile: file})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenCodeBin != "/usr/local/bin/opencode" {
		t.Fatalf("OpenCodeBin = %q", cfg.OpenCodeBin)
	}
	if cfg.Defaults.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %d", cfg.Defaults.TimeoutSeconds)
	}
	if cfg.Defaults.Repeat != 1 {
		t.Fatalf("Repeat default not filled: %d", cfg.Defaults.Repeat)
	}
	if len(cfg.Sandbox.PassEnv) != 1 || cfg.Sandbox.PassEnv[0] != "NODE_EXTRA_CA_CERTS" {
		t.Fatalf("PassEnv = %v", cfg.Sandbox.PassEnv)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("Listen default not filled: %q", cfg.Server.Listen)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte(": : :"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Paths{ConfigFile: file}); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
```

- [ ] **Step 6: Run and watch it fail, then implement `config.go`**

Run: `go test ./internal/config/ -v` — expect FAIL.

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Sandbox struct {
	InheritEnvironment bool     `yaml:"inherit_environment"`
	PassEnv            []string `yaml:"pass_env"`
}

type Defaults struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	Repeat         int `yaml:"repeat"`
}

type Server struct {
	Listen string `yaml:"listen"`
}

type Config struct {
	OpenCodeBin string   `yaml:"opencode_bin"`
	Sandbox     Sandbox  `yaml:"sandbox"`
	Defaults    Defaults `yaml:"defaults"`
	Server      Server   `yaml:"server"`
}

func DefaultsConfig() Config {
	return Config{
		OpenCodeBin: "opencode",
		Sandbox:     Sandbox{},
		Defaults:    Defaults{TimeoutSeconds: 900, Repeat: 1},
		Server:      Server{Listen: "127.0.0.1:8787"},
	}
}

func Load(paths Paths) (Config, error) {
	cfg := DefaultsConfig()
	raw, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", paths.ConfigFile, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", paths.ConfigFile, err)
	}
	if cfg.OpenCodeBin == "" {
		cfg.OpenCodeBin = "opencode"
	}
	if cfg.Defaults.TimeoutSeconds <= 0 {
		cfg.Defaults.TimeoutSeconds = 900
	}
	if cfg.Defaults.Repeat <= 0 {
		cfg.Defaults.Repeat = 1
	}
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8787"
	}
	return cfg, nil
}
```

- [ ] **Step 7: Run all config tests**

Run: `go test ./internal/config/ -v`
Expected: PASS (6 tests).

- [ ] **Step 8: Commit**

```bash
go mod tidy && go mod vendor
git add -A
git commit -m "feat: add XDG path resolution and config loading"
```

---

### Task 3: SQLite store and embedded migrations

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`
- Create: `internal/store/migrations/0001_init.sql`
- Create: `internal/store/store_test.go`, `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `store.Open(path string) (*Store, error)`
  - `store.Store.Close() error`, `store.Store.DB() *sql.DB`
  - `store.Store.Migrate(ctx context.Context) (int, error)` — applies pending migrations, returns resulting version
  - `store.Store.SchemaVersion(ctx context.Context) (int, error)`
- Later tasks add repository methods (`InsertProfile`, `GetProfileByHash`, `LatestProfile`, `InsertProfileChanges`, `ListProfileChanges`) in `internal/store/profile.go`.

- [ ] **Step 1: Write the failing migration tests**

`internal/store/migrate_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesSchemaV1(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	for _, table := range []string{
		"schema_migrations", "profiles", "profile_components", "suites",
		"tasks", "runs", "run_metrics", "run_validations", "profile_changes", "experiments",
	} {
		var name string
		err := st.DB().QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d", v)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1", n)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = st.DB().ExecContext(ctx,
		`INSERT INTO profile_components (profile_id, kind, name, hash, canonical_json)
		 VALUES ('missing', 'agent', 'build', 'h', '{}')`)
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/store/ -v`
Expected: FAIL — undefined `Open`, `Store`.

- [ ] **Step 3: Write the migration SQL**

`internal/store/migrations/0001_init.sql` — exact contents from spec section 6, preceded by `PRAGMA foreign_keys` handling left to the connection (see store.go). Copy the ten `CREATE TABLE` statements verbatim from the spec.

- [ ] **Step 4: Implement the store**

`internal/store/store.go`:

```go
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }
```

`internal/store/migrate.go`:

```go
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected NNNN_name.sql", e.Name())
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		body, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (s *Store) Migrate(ctx context.Context) (int, error) {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return current, fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return current, err
		}
		if err := tx.Commit(); err != nil {
			return current, err
		}
		current = m.version
	}
	return current, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}
```

- [ ] **Step 5: Run migration tests**

Run: `go get modernc.org/sqlite@latest && go mod tidy && go test ./internal/store/ -v`
Expected: PASS (3 tests). If `modernc.org/sqlite` fails to resolve, report the error instead of substituting another driver.

- [ ] **Step 6: Commit**

```bash
go mod vendor
git add -A
git commit -m "feat: add sqlite store with embedded schema migrations"
```

---

### Task 4: Canonical JSON, redaction, path normalisation, hashing

**Files:**
- Create: `internal/canon/canon.go`, `internal/canon/redact.go`, `internal/canon/normalize.go`
- Create: `internal/canon/canon_test.go`, `internal/canon/redact_test.go`, `internal/canon/normalize_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `canon.JSON(v any) ([]byte, error)` — deterministic canonical JSON; object keys sorted, HTML escaping disabled, trailing newline stripped, numbers via `json.Marshal` defaults.
  - `canon.Hash(v any) (string, error)` — hex sha256 of `canon.JSON(v)`.
  - `canon.HashBytes(b []byte) string`
  - `canon.Redact(v any) (any, error)` — deep copy with sensitive keys replaced by `"<redacted>"`; matches keys case-insensitively against `api[_-]?key|token|secret|password|passwd|credential|authorization|cookie` (also matches when the key merely *contains* these substrings, e.g. `X-Api-Key`).
  - `canon.NormalizePaths(v any, home, runDir string) (any, error)` — replaces the home prefix with `~`, `runDir` with `<run-dir>`; only whole leading path segments are replaced (no substring edits inside arbitrary text — implement by checking each string value: if it equals a prefix path or starts with prefix+"/").
  - `canon.SHA256Hex(b []byte) string`

- [ ] **Step 1: Write the failing tests**

`internal/canon/canon_test.go`:

```go
package canon

import (
	"encoding/json"
	"testing"
)

func TestJSONSortsKeysDeterministically(t *testing.T) {
	a := map[string]any{"b": 1, "a": map[string]any{"y": 2, "x": 3}}
	b := map[string]any{"a": map[string]any{"x": 3, "y": 2}, "b": 1}
	ba, err := JSON(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := JSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ba) != string(bb) {
		t.Fatalf("nondeterministic:\n%s\n%s", ba, bb)
	}
	if string(ba) != `{"a":{"x":3,"y":2},"b":1}` {
		t.Fatalf("canonical form = %s", ba)
	}
}

func TestHashIsStableAcrossReencode(t *testing.T) {
	raw := []byte(`{"z":1,"a":{"nested":[1,2,{"k":"v"}]}}`)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	h1, err := Hash(v)
	if err != nil {
		t.Fatal(err)
	}
	var v2 any
	if err := json.Unmarshal(raw, &v2); err != nil {
		t.Fatal(err)
	}
	h2, err := Hash(v2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %s %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d", len(h1))
	}
}
```

`internal/canon/redact_test.go`:

```go
package canon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactNestedSecrets(t *testing.T) {
	raw := `{
	  "mcp": {"gitlab": {"type": "remote", "url": "https://x/mcp",
	          "headers": {"Authorization": "Bearer abc", "X-Api-Key": "k"},
	          "environment": {"GITLAB_TOKEN": "glpat-xxx", "SAFE_VAR": "public"}}},
	  "provider": {"anthropic": {"apiKey": "sk-ant", "options": {"timeout": 30}}},
	  "plugin": ["superpowers@git+https://x"],
	  "tokens": {"input": 5}
	}`
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	out, err := Redact(v)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, leaked := range []string{"Bearer abc", "glpat-xxx", "sk-ant", `"k"`} {
		if strings.Contains(s, leaked) {
			t.Fatalf("secret leaked: %s in %s", leaked, s)
		}
	}
	if !strings.Contains(s, `"SAFE_VAR":"public"`) {
		t.Fatalf("non-secret value was redacted: %s", s)
	}
	if !strings.Contains(s, `"timeout":30`) {
		t.Fatalf("non-secret key redacted: %s", s)
	}
	if !strings.Contains(s, `"input":5`) {
		t.Fatalf("tokens metric redacted: %s", s)
	}
}

func TestRedactDoesNotMutateInput(t *testing.T) {
	v := map[string]any{"apiKey": "secret"}
	if _, err := Redact(v); err != nil {
		t.Fatal(err)
	}
	if v["apiKey"] != "secret" {
		t.Fatal("input mutated")
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	v := map[string]any{"a": map[string]any{"password": "p", "keep": 1}}
	once, err := Redact(v)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Redact(once)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(once)
	b2, _ := json.Marshal(twice)
	if string(b1) != string(b2) {
		t.Fatalf("not idempotent: %s vs %s", b1, b2)
	}
}
```

`internal/canon/normalize_test.go`:

```go
package canon

import (
	"encoding/json"
	"testing"
)

func TestNormalizePaths(t *testing.T) {
	home := "/Users/mich"
	runDir := "/tmp/ocbench/run-1"
	v := map[string]any{
		"skill":    "/Users/mich/.agents/skills/ruff/SKILL.md",
		"run":      "/tmp/ocbench/run-1/worktree/src/a.py",
		"other":    "/etc/hosts",
		"homeish":  "/Users/michelle/x",
		"embedded": "see /Users/mich/.config for details",
	}
	out, err := NormalizePaths(v, home, runDir)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["skill"] != "~/.agents/skills/ruff/SKILL.md" {
		t.Fatalf("skill = %v", m["skill"])
	}
	if m["run"] != "<run-dir>/worktree/src/a.py" {
		t.Fatalf("run = %v", m["run"])
	}
	if m["other"] != "/etc/hosts" {
		t.Fatalf("other = %v", m["other"])
	}
	if m["homeish"] != "/Users/michelle/x" {
		t.Fatalf("prefix over-matched: %v", m["homeish"])
	}
	if m["embedded"] != "see /Users/mich/.config for details" {
		t.Fatalf("embedded path should be untouched, got %v", m["embedded"])
	}
}

func TestNormalizePathsEmptyInputsAreNoOps(t *testing.T) {
	v := map[string]any{"a": "/x/y"}
	out, err := NormalizePaths(v, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"a":"/x/y"}` {
		t.Fatalf("out = %s", b)
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/canon/ -v`
Expected: FAIL — undefined `JSON`, `Redact`, `NormalizePaths`.

- [ ] **Step 3: Implement `canon.go`**

```go
package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

func JSON(v any) ([]byte, error) {
	normalized, err := toCanonical(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalized); err != nil {
		return nil, fmt.Errorf("encode canonical json: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func toCanonical(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			cv, err := toCanonical(t[k])
			if err != nil {
				return nil, err
			}
			out[k] = cv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			cv, err := toCanonical(item)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	default:
		return v, nil
	}
}

func Hash(v any) (string, error) {
	b, err := JSON(v)
	if err != nil {
		return "", err
	}
	return SHA256Hex(b), nil
}

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func HashBytes(b []byte) string { return SHA256Hex(b) }
```

Note: `encoding/json` marshals map keys sorted already, but the explicit sort makes the guarantee local and survives future changes to Go's encoder. Numbers: keep `json.Unmarshal` default (`float64`) consistently — never mix in `json.Number` per call site.

- [ ] **Step 4: Implement `redact.go`**

```go
package canon

import (
	"regexp"
	"strings"
)

const RedactedValue = "<redacted>"

var sensitiveKey = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|passwd|credential|authorization|cookie)`)

func IsSensitiveKey(key string) bool { return sensitiveKey.MatchString(key) }

func Redact(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSensitiveKey(k) {
				out[k] = RedactedValue
				continue
			}
			rv, err := Redact(val)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			rv, err := Redact(item)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

// RedactRaw decodes, redacts and returns the redacted value.
func RedactRaw(raw []byte) (any, error) {
	var v any
	if err := jsonUnmarshalStrictish(raw, &v); err != nil {
		return nil, err
	}
	return Redact(v)
}
```

Use a small helper `jsonUnmarshalStrictish` that wraps `json.Unmarshal` with a clearer error (`fmt.Errorf("decode json: %w", err)`); do not add strict unknown-field rejection — resolved config gains keys between versions and must not fail.

- [ ] **Step 5: Implement `normalize.go`**

```go
package canon

import "strings"

func NormalizePaths(v any, home, runDir string) (any, error) {
	replacements := make([][2]string, 0, 2)
	if home != "" {
		replacements = append(replacements, [2]string{home, "~"})
	}
	if runDir != "" {
		replacements = append(replacements, [2]string{runDir, "<run-dir>"})
	}
	return normalize(v, replacements)
}

func normalize(v any, replacements [][2]string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nv, err := normalize(val, replacements)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			nv, err := normalize(item, replacements)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case string:
		return normalizeString(t, replacements), nil
	default:
		return v, nil
	}
}

func normalizeString(s string, replacements [][2]string) string {
	for _, r := range replacements {
		from, to := r[0], r[1]
		if from == "" {
			continue
		}
		if s == from {
			s = to
			continue
		}
		if strings.HasPrefix(s, from+"/") {
			s = to + s[len(from):]
		}
	}
	return s
}
```

- [ ] **Step 6: Run all canon tests**

Run: `go test ./internal/canon/ -v`
Expected: PASS (6 tests).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: add canonical json, redaction, path normalization and hashing"
```

---

### Task 5: OpenCode adapter with helper-process tests

**Files:**
- Create: `internal/opencode/adapter.go`, `internal/opencode/real.go`, `internal/opencode/parse.go`
- Create: `internal/opencode/real_test.go`, `internal/opencode/helper_test.go`, `internal/opencode/testdata/` (created by tests at runtime, not committed)
- Create: `internal/opencode/parse_test.go`

**Interfaces:**
- Consumes: `canon` (for nothing at this layer; parsing only), `config` only via a plain options struct so this package does not import config.
- Produces:

```go
package opencode

type Adapter interface {
	Version(ctx context.Context) (string, error)
	ResolvedConfig(ctx context.Context, dir string) ([]byte, error)
	Skills(ctx context.Context, dir string) ([]SkillInfo, error)
	Agent(ctx context.Context, dir, name string) (AgentInfo, error)
	MCPStatus(ctx context.Context, dir string) ([]MCPStatus, error)
}

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Content     string `json:"content"`
}

type AgentInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mode        string          `json:"mode"`
	Native      bool            `json:"native"`
	Model       string          `json:"model"`
	Variant     string          `json:"variant"`
	Steps       *int            `json:"steps"`
	Temperature *float64        `json:"temperature"`
	Tools       json.RawMessage `json:"tools"`
	Options     json.RawMessage `json:"options"`
	Permission  json.RawMessage `json:"permission"`
	Prompt      json.RawMessage `json:"prompt"`
	Raw         json.RawMessage `json:"-"`
}

type MCPStatus struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Raw      json.RawMessage `json:"-"`
}

type Options struct {
	Bin     string
	Timeout time.Duration
	Env     []string // nil means os.Environ()
	Dir     string
}

func NewReal(opts Options) *Real
func (r *Real) Version(ctx context.Context) (string, error)
func (r *Real) ResolvedConfig(ctx context.Context, dir string) ([]byte, error)
func (r *Real) Skills(ctx context.Context, dir string) ([]SkillInfo, error)
func (r *Real) Agent(ctx context.Context, dir, name string) (AgentInfo, error)
func (r *Real) MCPStatus(ctx context.Context, dir string) ([]MCPStatus, error)

// Exported for testing and doctor diagnostics.
func (r *Real) Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
```

- Verified CLI contracts this task must satisfy (from spec section 2):
  - `opencode --version` → single line like `1.18.32`.
  - `opencode debug config` → JSON object on stdout (ignore stderr noise).
  - `opencode debug skill` → JSON array of `{name,description,location,content}`.
  - `opencode debug agent <name>` → JSON object with `name`, `mode`, `native`, `model`, `variant`, `steps`, `temperature`, `tools`, `options`, `permission`.
  - `opencode mcp list` → human-readable ANSI table; additionally `opencode mcp list --json` if available is tried first and falls back to text parsing. Implement text parsing for lines matching `●  ○ <name> disabled` / `●  <name>` followed by an indented target line. If parsing yields zero servers, return an empty slice and no error.
  - All subcommands accept `--print-logs` and `--log-level` for diagnostics.

- [ ] **Step 1: Write the helper-process fake**

`internal/opencode/helper_test.go` (complete):

```go
package opencode

import (
	"fmt"
	"os"
	"testing"
)

// TestHelperProcess is the fake opencode binary. It is only active when
// GO_WANT_HELPER_PROCESS=1, which the parent test sets on the child env.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode := os.Getenv("FAKE_MODE")
	write := func(s string) {
		fmt.Fprint(os.Stdout, s)
		os.Exit(0)
	}
	switch {
	case len(args) > 0 && args[0] == "--version":
		write("1.18.32\n")
	case len(args) > 1 && args[0] == "debug" && args[1] == "config":
		if mode == "garbage" {
			write("not json")
		}
		write(`{"default_agent":"build","model":"p/m","agent":{"build":{"model":"p/m","variant":"high","steps":60,"options":{}}}}`)
	case len(args) > 1 && args[0] == "debug" && args[1] == "skill":
		if mode == "empty" {
			write("[]")
		}
		write(`[{"name":"ruff","description":"lint","location":"/home/u/.agents/skills/ruff/SKILL.md","content":"# ruff"}]`)
	case len(args) > 2 && args[0] == "debug" && args[1] == "agent":
		write(fmt.Sprintf(`{"name":%q,"mode":"subagent","native":false,"model":"p/m","variant":"low","steps":20,"tools":{"read":true},"options":{},"permission":[]}`, args[2]))
	case len(args) > 1 && args[0] == "mcp" && args[1] == "list":
		if mode == "fail" {
			os.Exit(3)
		}
		write("\u2502\n\u25cf  \u25cb gitlab \u001b[90mdisabled\n\u2502      https://mcp.example/v2/mcp\n")
	}
	os.Exit(42)
}

func helperEnv() []string {
	return append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
}
```

Note for implementer: the canonical Go helper pattern is `exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", args...)` with `GO_WANT_HELPER_PROCESS=1` in the environment. Tests in later steps must construct the adapter as `NewReal(Options{Bin: os.Args[0], Timeout: 5 * time.Second, Env: helperEnv()})`. Because the helper is exercised through `go test`, all subprocess invocations must include `-test.run=TestHelperProcess --` before the fake opencode argv. To keep this clean, `Real.Run` must support an `extraArgsPrefix` injected via `Options.TestPrefix []string` (empty in production). Add that field now; it exists solely for tests.

Adjust the `Options` struct accordingly:

```go
type Options struct {
	Bin        string
	Timeout    time.Duration
	Env        []string
	TestPrefix []string // test-only: argv inserted before the opencode args
}
```

- [ ] **Step 2: Write the failing parser tests**

`internal/opencode/parse_test.go`:

```go
package opencode

import "testing"

func TestParseVersionTrimsNoise(t *testing.T) {
	got, err := parseVersion([]byte("1.18.32\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.18.32" {
		t.Fatalf("got %q", got)
	}
}

func TestParseMCPListText(t *testing.T) {
	out := "\x1b[0m\n\u250c  MCP Servers\n\u2502\n\u25cf  \u25cb firecrawl \x1b[90mdisabled\n\u2502      https://mcp.firecrawl.dev//v2/mcp\n\u2502\n\u25cf  \u25cf gitlab \x1b[90mconnected\n\u2502      npx -y gitlab-mcp\n"
	got, err := parseMCPList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("servers = %d: %+v", len(got), got)
	}
	if got[0].Name != "firecrawl" || got[0].Enabled || got[0].Target != "https://mcp.firecrawl.dev//v2/mcp" {
		t.Fatalf("server 0 = %+v", got[0])
	}
	if got[1].Name != "gitlab" || !got[1].Enabled || got[1].Target != "npx -y gitlab-mcp" {
		t.Fatalf("server 1 = %+v", got[1])
	}
}

func TestParseMCPListEmpty(t *testing.T) {
	got, err := parseMCPList([]byte("\x1b[0m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 3: Run and watch them fail, then implement `parse.go`**

Run: `go test ./internal/opencode/ -v` — expect FAIL.

`parse.go` must contain `parseVersion([]byte) (string, error)` (first non-empty line trimmed) and `parseMCPList([]byte) ([]MCPStatus, error)` implementing: strip ANSI escapes, scan lines; a server line is one containing `\u25cf` (filled circle) and optionally `\u25cb`/`\u25cf` before the name; enabled means the second marker is the filled circle or the line lacks the literal `disabled`; the name is the first token after the markers; the target is the next line's trimmed content that is not a box-drawing line. Return `nil` slice for zero servers. Include a focused unit test for ANSI stripping.

- [ ] **Step 4: Implement the real adapter**

`real.go` requirements:

```go
func NewReal(opts Options) *Real

func (r *Real) Run(ctx context.Context, args ...string) (stdout, stderr []byte, err error)
```

- Resolve `Bin` (default `opencode`), `Timeout` (default 60s), `Env` (default `os.Environ()`).
- Build argv: `append(append([]string{}, r.opts.TestPrefix...), args...)`.
- Use `exec.CommandContext`; on timeout the context error must be wrapped as `fmt.Errorf("opencode %s timed out after %s: %w", strings.Join(args, " "), timeout, ctx.Err())`.
- Capture stdout and stderr into separate buffers.
- On non-zero exit return an error that includes the exit code and a trimmed stderr excerpt (max 2 KiB) and the stdout excerpt; define `type ExitError struct{ Args []string; Code int; Stderr, Stdout string }` implementing `error`.
- Each method: `Version` runs `--version` and calls `parseVersion`; `ResolvedConfig` runs `debug config` and returns raw stdout after `json.Valid` check; `Skills` runs `debug skill`, `json.Unmarshal` into `[]SkillInfo` and errors on invalid JSON with a stderr/stdout excerpt; `Agent` runs `debug agent <name>` and unmarshals into `AgentInfo`, storing the raw bytes in `Raw`; `MCPStatus` runs `mcp list` and parses text.

- [ ] **Step 5: Write the failing adapter tests**

`internal/opencode/real_test.go` — table tests calling the adapter with `Bin: os.Args[0]`, `Env: helperEnv()`, `TestPrefix: []string{"-test.run=TestHelperProcess", "--"}`, and `FAKE_MODE` appended per case. Cover: version; resolved config JSON validity; skills parse; agent parse (verify `Steps` pointer is 20 and `Tools` contains `read`); mcp list two-server parse; `FAKE_MODE=empty` → zero skills; `FAKE_MODE=garbage` → error mentioning "invalid JSON"; `FAKE_MODE=fail` → `ExitError` with code 3. Also a timeout test using a helper mode that sleeps 5s and an adapter timeout of 200ms.

To support the sleep mode add to the helper: `case mode == "sleep": time.Sleep(5 * time.Second)`.

- [ ] **Step 6: Run adapter tests**

Run: `go test ./internal/opencode/ -v`
Expected: PASS for all cases. Confirm no test invokes the real `opencode` binary (they must pass with `OCBENCH_OPENCODE_BIN=/nonexistent` set).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: add opencode CLI adapter with helper-process tests"
```

---

### Task 6: Profile discovery, fingerprinting, persistence and diff

**Files:**
- Create: `internal/profile/sources.go`, `internal/profile/fingerprint.go`, `internal/profile/instructions.go`, `internal/profile/persist.go`, `internal/profile/diff.go`
- Create: `internal/store/profile.go` (store-side SQL)
- Create: `internal/profile/fingerprint_test.go`, `internal/profile/discover_test.go`, `internal/profile/diff_test.go`, `internal/store/profile_test.go`
- Testdata: `internal/profile/testdata/resolved_config.json`, `internal/profile/testdata/skills.json`, `internal/profile/testdata/agents/*.json` — realistic captures trimmed from the shapes in spec section 2; include one MCP entry with a secret, one skill directory with a `references/` file, and one local `file://` plugin.

**Interfaces:**
- Consumes: `opencode.Adapter`, `canon`, `store`, `config.Paths`.
- Produces:

```go
package profile

type Sources struct {
	OpenCodeVersion string
	ResolvedConfig  []byte
	Skills          []opencode.SkillInfo
	Agents          []opencode.AgentInfo
	Instructions    map[string][]byte // scope → content, key like "global:AGENTS.md"
	Dir             string
	Home            string // used for path normalisation; tests set it explicitly
}

type Options struct {
	Dir     string
	Agent   string
	Model   string
	Variant string
	Auto    bool
	Pure    bool
	EnvNames []string
}

type Component struct {
	Kind          string
	Name          string
	Hash          string
	CanonicalJSON []byte
}

type Profile struct {
	ID            string
	Hash          string
	OpenCodeVersion string
	CanonicalJSON []byte
	Components    []Component
	Snapshot      map[string]any // full canonical structure, redacted+normalized
}

type Change struct {
	Kind, Name, Change string
	FromHash, ToHash   string
}

func Discover(ctx context.Context, a opencode.Adapter, dir string) (*Sources, error)
func Fingerprint(s *Sources, opts Options) (*Profile, error)
func Persist(ctx context.Context, st *store.Store, paths config.Paths, p *Profile) (created bool, err error)
func Diff(from, to *Profile) []Change
func Latest(ctx context.Context, st *store.Store) (*Profile, error)
```

- Component keys are exactly `kind` (singletons) or `kind/name` (named), per spec section 5.1: `primary`, `agent/<name>`, `skill/<name>`, `mcp/<name>`, `plugin/<spec>`, `instructions/<scope>`, `permissions`, `config`, `environment`.

- [ ] **Step 1: Write the failing Fingerprint determinism test**

`internal/profile/fingerprint_test.go` — build `Sources` from testdata twice with deliberately shuffled map/slice order and assert identical `Hash`; assert that changing only one skill's content changes `skill/<name>` and the overall hash but not `agent/build`; assert redaction: the MCP secret from testdata never appears in `CanonicalJSON`; assert path normalisation: with `Sources.Dir=/tmp/ocbench/run-x` and a skill location under the fake home, canonical JSON contains `~` and `<run-dir>` and no absolute home path.

- [ ] **Step 2: Run and watch it fail, then implement `fingerprint.go`**

Implementation requirements:

1. Decode `ResolvedConfig` into `map[string]any`.
2. Redact the decoded config, then normalise paths (`home` from `Sources.Instructions` global path's directory or an injected field — add `Home string` to `Sources`, populated by `Discover` from `os.UserHomeDir()`/`HOME`; tests set it explicitly).
3. Build components:
   - `primary`: `default_agent`, `model`, `small_model` from resolved config; apply `Options.Agent/Model/Variant` overrides (an override replaces the effective value and sets `environment.overrides`).
   - `agent/<name>` for every agent in resolved config `.agent`, enriched with the matching `AgentInfo` from `Sources.Agents` (`mode`, `native`, `tools`); include `model`, `variant`, `temperature`, `steps`, `options`, and `prompt_sha256` (hash of the raw prompt bytes if present); exclude `permission` (see next bullet).
   - `permissions`: `{global: <resolved config .permission>, by_agent: {<name>: <agent .permission>}}`, normalized.
   - `skill/<name>`: `description`, `content_sha256` (sha256 of `Content`), `files_sha256` (sha256 over sorted `relpath\0bytes` for all regular files under `filepath.Dir(Location)`), `source` (normalized `filepath.Dir(Location)`). If the directory is unreadable, fall back to `files_sha256 = content_sha256` and record `source_error: true` — never fail the whole fingerprint for one skill.
   - `mcp/<name>`: redacted+normalized MCP entry from resolved config; keep `environment_keys` (sorted key names) instead of the environment map.
   - `plugin/<spec>`: from `plugin` array; for `file://` specs compute `local_sha256` over file bytes (or directory walk if the plugin is a directory); include origin from `plugin_origins` when present (normalized).
   - `instructions/<scope>`: sha256 of each `Sources.Instructions` entry.
   - `config`: resolved config minus the keys consumed above (`model`, `small_model`, `default_agent`, `agent`, `mcp`, `plugin`, `plugin_origins`, `skills`, `permission`, `username`, `$schema`), redacted+normalized.
   - `environment`: `{sandbox, env_names (sorted), auto, pure, overrides}`.
4. Assemble `map[string]any{"schema":1,"opencode_version":...,"components":{key: subtree}}`; compute `profile.Hash = canon.Hash(snapshot)`; per component `Hash = canon.Hash(subtree)`; `CanonicalJSON = canon.JSON(snapshot)`.
5. Components are returned sorted by key for stable output.

- [ ] **Step 3: Write `sources.go`/`instructions.go` (Discover) and its test**

`Discover` calls the adapter (`Version`, `ResolvedConfig(dir)`, `Skills(dir)`, `Agent(dir,name)` for every agent name found in the resolved config `.agent` map plus every name from `agent list` output if available — in v1 use the resolved config map only) and reads instructions: `os.ReadFile(filepath.Join(home, ".config/opencode/AGENTS.md"))` plus `AGENTS.md` at each ancestor of `dir` (key `project:AGENTS.md` for the first ancestor match, `global:AGENTS.md` for the home config). Missing files are skipped, read errors are returned. `Home` comes from `os.UserHomeDir()`.

Test with the Task 5 helper adapter, pointing `Dir` at `t.TempDir()` containing an `AGENTS.md`; assert scopes discovered and that the adapter was consulted for every agent named in the canned resolved config.

- [ ] **Step 4: Write the store profile repository and its test**

`internal/store/profile.go`:

```go
type ProfileRow struct {
	ID              string
	ProfileHash     string
	OpenCodeVersion string
	OCBenchVersion  string
	CanonicalJSON   string
	CreatedAt       string
}

type ComponentRow struct {
	Kind          string
	Name          string
	Hash          string
	CanonicalJSON string
}

func (s *Store) InsertProfile(ctx context.Context, p ProfileRow, comps []ComponentRow) error // INSERT OR IGNORE semantics via ON CONFLICT DO NOTHING; single transaction
func (s *Store) GetProfileByHash(ctx context.Context, hash string) (*ProfileRow, []ComponentRow, error)
func (s *Store) LatestProfile(ctx context.Context) (*ProfileRow, []ComponentRow, error) // ORDER BY created_at DESC, id DESC LIMIT 1
func (s *Store) InsertProfileChanges(ctx context.Context, fromID, toID string, changes []ChangeRow) error
func (s *Store) ListProfileChanges(ctx context.Context, toID string) ([]ChangeRow, error)
```

Tests: insert twice with the same hash → second returns no error, table has one row; foreign keys cascade on delete; `LatestProfile` ordering; `InsertProfileChanges` round-trip.

- [ ] **Step 5: Implement `persist.go` and `diff.go` plus tests**

`Persist`: serialize profile to rows; call `InsertProfile`; if `GetProfileByHash` already existed before insert, return `created=false`. Raw captures: write `resolved-config.json` (redacted), `skills.json` (redacted metadata + hashes only — do NOT write full skill content), `agents.json`, `snapshot.json` (canonical JSON) under `paths.Profiles/<hash>/`. `os.MkdirAll` idempotent. `Diff` compares components by `(Kind,Name)`, returning `added`/`removed`/`changed` entries sorted by kind then name. Tests: diff of two fixtures yields exactly one change when one skill changes; added/removed cases; `Diff(p,p)` empty.

- [ ] **Step 6: Run all profile + store tests**

Run: `go test ./internal/profile/ ./internal/store/ -v`
Expected: PASS. Grep the temp profile directory in the test for the testdata secret and fail the test if found (assert in code, not manually).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: resolve, fingerprint, persist and diff OpenCode execution profiles"
```

---

### Task 7: `ocbench doctor`

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/doctor_test.go`, `internal/cli/doctor.go`, `internal/cli/deps.go`
- Modify: `internal/cli/root.go` (register `doctor`, introduce injectable deps)

**Interfaces:**
- Consumes: `config`, `store`, `opencode`, `profile`, `version`.
- Produces:

```go
package doctor

type Status string
const (StatusOK Status = "ok"; StatusWarn Status = "warn"; StatusFail Status = "fail")

type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Detail  string `json:"detail"`
}

type Report struct {
	Checks []Check `json:"checks"`
	ProfileHash string `json:"profile_hash,omitempty"`
	OpenCodeVersion string `json:"opencode_version,omitempty"`
	AgentCount int `json:"agent_count"`
	SkillCount int `json:"skill_count"`
	EnabledMCPs int `json:"enabled_mcps"`
}

func (r Report) Healthy() bool // no Check with StatusFail
func Run(ctx context.Context, a opencode.Adapter, paths config.Paths, cfg config.Config) (Report, error)
```

Checks: `opencode` binary resolvable and version parseable (fail if not); `git` present (warn if missing — Plan 2 needs it); data dirs creatable and DB openable + `Migrate` (fail on error); config file parse (fail); profile discovery + fingerprint (fail on adapter error); skills/agents/MCP counts (warn when zero); sandbox mode summary (ok, informational). `Run` returns the report and a non-nil error only for programming errors; check failures are reported in `Report`.

- [ ] **Step 1: Write failing tests** using the helper adapter and temp `Paths`; assert: all-ok report against the fake; fail entry when `Bin` points at a missing file; DB migration applied; counts match canned data.
- [ ] **Step 2: Introduce injectable CLI dependencies** in `internal/cli/deps.go`:

```go
type Deps struct {
	Adapter opencode.Adapter // nil → real adapter built from config
	Paths   config.Paths     // zero → config.ResolveOS()
	Config  config.Config    // zero → config.DefaultsConfig()
}

func NewRootWithDeps(d Deps) *cobra.Command
```

`NewRoot()` becomes `NewRootWithDeps(Deps{})`. `NewRootWithDeps` resolves zero fields lazily: paths via `config.ResolveOS()`, config via `config.Load(paths)` (returning an error from `RunE` validation), adapter via `opencode.NewReal(opencode.Options{Bin: cfg.OpenCodeBin})`. Every subsequent command (doctor, snapshot, and later run/history/compare/serve) receives `Deps` and never reaches for the environment directly. `execute()` must return an error rather than calling `os.Exit` so command-level tests can assert on it; `Execute()` in `root.go` maps non-nil errors to exit code 1.
- [ ] **Step 3: Implement `doctor.go` and the CLI command** (`ocbench doctor [--json]`, human output is an aligned table `NAME  STATUS  DETAIL`, exit code `1` when `!Healthy()`, `0` otherwise). `doctor_test.go` covers `doctor.Run`; `doctor.go` in `internal/cli` builds a `doctor.Report` from `Deps` and renders it.
- [ ] **Step 4: Run tests + manual smoke**: `go run ./cmd/ocbench doctor --json` against the real environment; it must report ok for opencode/git/db/config and non-zero counts. This is the first real-OpenCode execution; if the adapter fails against the real binary, fix the adapter (not the test) and record what was learned.
- [ ] **Step 5: Commit** `feat: add doctor command`

---

### Task 8: `ocbench snapshot`

**Files:**
- Create: `internal/cli/snapshot.go`, `internal/cli/snapshot_test.go`, `internal/profile/render.go`, `internal/profile/render_test.go`
- Modify: `internal/cli/root.go` (register command)

**Interfaces:**
- Consumes: everything from Tasks 1–7.
- Produces: `profile.RenderSummary(p *Profile) string`, `profile.RenderChanges(changes []Change) string`.

Behaviour:

```
ocbench snapshot [--json] [--dir D] [--agent A] [--model M] [--variant V]
```

1. Load config + paths; ensure dirs; open + migrate DB.
2. `Discover` + `Fingerprint` with overrides.
3. `Persist` (dedupe by hash). If created, compare against the previous latest profile and `InsertProfileChanges`. If not created, still diff against the most recent *different* profile for display.
4. Human output:

```
profile 9a814d91  (new | existing)
opencode 1.18.32

agent/build        c788e3f1
skill/pydantic     fae71b2a
mcp/gitlab         a827ab19
...

changes vs 4e921fcc
  skill/kubernetes-debugging  changed  2e11f4a1 -> 671ab0c2
  mcp/kubernetes              added
```

5. `--json` prints `{profile:{id,hash,opencode_version,created}, components:[...], changes:[...]}`.
6. Exit 0 on success; 1 on adapter/db failure.

- [ ] **Step 1: Write failing render tests** (golden strings for a fixture profile with two components and one change).
- [ ] **Step 2: Implement `render.go`; run tests.**
- [ ] **Step 3: Wire the snapshot command through the `Deps`/`NewRootWithDeps` mechanism introduced in Task 7.** No new injection mechanism: `snapshot.go` reads `d.Adapter`, `d.Paths`, `d.Config` and uses a package-level `newSnapshotCmd(d Deps)` constructor registered by `NewRootWithDeps`.
- [ ] **Step 4: Command-level tests** with fake adapter + temp paths: two snapshots → one profile row, no changes; mutate a fake skill content between runs → second snapshot reports exactly that one change and creates a second profile; `--json` shape parses.
- [ ] **Step 5: Real smoke (manual, by the implementer on the dev machine):**

```bash
go run ./cmd/ocbench snapshot
go run ./cmd/ocbench snapshot
sqlite3 "$HOME/.local/share/ocbench/ocbench.db" 'select count(*) from profiles;'
```
Expected: first run prints `(new)`, second prints `(existing)`, count = 1. Record the observed profile hash in the commit message body.

- [ ] **Step 6: Commit** `feat: add snapshot command with profile diff output`

---

### Task 9: Verification pass (verifier subagent, no new features)

**Files:** none created; this task produces evidence.

- [ ] **Step 1:** `make lint && make test && make cross` — record exact output.
- [ ] **Step 2:** Confirm determinism against the real environment: run `snapshot` three times; exactly one profile row; identical hash each time.
- [ ] **Step 3:** Confirm change detection without touching the user's real config: copy the resolved config to a temp dir? (Adapter reads live config; instead use `--model` override.) Run `snapshot --variant low` and `snapshot --variant high`; confirm two profiles and that `compare` data (`profile_changes`) shows only `environment` and `agent/*` variant differences — exactly the components that legitimately changed.

Wait: `--variant` override only affects `primary`/`environment.overrides` in v1; document the observed behaviour precisely rather than asserting a specific diff shape.
- [ ] **Step 4:** Grep the real `profiles/<hash>/` captures for `sk-`, `glpat-`, `Bearer `, `apiKey` and report findings (auth.json contents must never appear).
- [ ] **Step 5:** Report the evidence table in the task result; do not fix anything as part of verification without filing the finding back to the coordinator.

---

## Self-Review Record

- Spec coverage: architecture/deps (T1), paths/config (T2, spec §3), store/schema (T3, spec §6), canonicalisation/redaction/normalisation (T4, spec §5.2–5.3), adapter (T5, spec §4), profile discovery/fingerprint/persistence/diff (T6, spec §5), doctor (T7, spec §10), snapshot (T8, spec §10), verification (T9, spec §11). Suite/runner/history/compare/serve are deliberately Plan 2–4.
- Review Focus tests: determinism (T6 Step 1, T9 Step 2), redaction (T6 Step 6 assert, T9 Step 4), adapter robustness (T5 Steps 5–6), migration idempotence (T3 Step 1), test isolation (T5 Step 6, T9).
- No placeholders: every task names exact files, interfaces, commands and expected results; code is given for all non-obvious logic.
