# DevOps

Docker, Makefile, CI, environment variables, and the development workflow.

The [`templates/`](templates/) directory contains copy-ready versions of every config file. Grab them into a new project and replace `myapp` with your binary/module name.

## Git Hooks — Lefthook

See [`templates/lefthook.yml`](templates/lefthook.yml). Pre-commit runs `lint` and `test` in parallel; pre-push runs `test-integration`. `make lint` already covers formatting (`go fmt`), the strict-but-bug-class linter set, and `blueprint-sql-check`, so the hook stays a single target.

`lefthook` is installed by `make setup`. After copying the template, activate the hooks once:

```bash
lefthook install
```

## Docker Compose — Dev Database

See [`templates/docker-compose.yml`](templates/docker-compose.yml).

Dev Postgres lives on `5432`; the test database uses a separate container on `15432` (see the Makefile) so both can run concurrently without port conflicts.

## Makefile

See [`templates/Makefile`](templates/Makefile). Available targets:

- `setup` — install dev tools + generate
- `install-tools` — install dev tools without running generate
- `build` / `run` — build or run the app
- `test` — unit tests only (`-short`, skips integration)
- `test-integration` — full suite against a local test container, with `-race -coverprofile`
- `test-db-up` / `test-db-down` — start / remove the test Postgres container
- `test-db-migrate` — apply migrations to the test DB
- `lint` — `go fmt`, the custom-gcl binary (golangci-lint + blueprint-vet plugin), and `blueprint-sql-check`
- `db-up` / `db-down` — start/stop dev Postgres
- `migrate-up` / `migrate-down` — run migrations
- `generate` — skimatik + go generate
- `swagger` — regenerate OpenAPI docs
- `clean` — remove build artifacts

**Change the port per service** when running more than one locally. The template defaults to `15432` for the test DB; pick any unused high-range port.

## GitHub Actions CI

See [`templates/.github/workflows/ci.yml`](templates/.github/workflows/ci.yml). A few things to note:

- The lint job is `make lint` — same target developers run locally, no `golangci-lint-action`. Flags can't drift between CI and dev because there's only one definition.
- `mockgen` is installed in CI because `go generate ./...` invokes it.
- Migrations run with `DATABASE_URL`; `pgxkit.RequireDB(t)` reads `TEST_DATABASE_URL`. Both point at the same Postgres service — separate env var names make the intent clear.
- `-race -coverprofile` matches the `make test-integration` target so the local and CI test runs exercise the same code paths.

## Environment Variables

See [`templates/.env.example`](templates/.env.example). `DATABASE_URL` is the only unconditionally required env var for `serve`. All other fields have defaults defined in [CONFIG.md](CONFIG.md).

## Lint Pipeline — custom-gcl + blueprint-vet plugin

Two files drive the lint pipeline:

- [`templates/.custom-gcl.yml`](templates/.custom-gcl.yml) pins the golangci-lint version and lists the [blueprint-vet](https://github.com/nhalm/blueprint-vet) plugin. `make lint` runs `golangci-lint custom` against this file and writes `./bin/custom-gcl` — a regular golangci-lint binary with blueprint-vet's analyzers compiled in. The `.custom-gcl.yml` file is the Makefile dep, so the binary rebuilds only when the pin changes.
- [`templates/.golangci.yml`](templates/.golangci.yml) enables the bug-class linter set (`errcheck`, `errorlint`, `rowserrcheck`, `sqlclosecheck`, `staticcheck`, `unused`, `govet`, `ineffassign`, `misspell`, `unconvert`, `gocyclo`) plus `revive`, `gocritic`, `unparam`, `wastedassign`, `prealloc`, `forbidigo`, and `blueprint-vet` as a custom linter.

`make lint` orchestrates the whole pipeline:

```bash
make lint   # go fmt → ./bin/custom-gcl run ./... → blueprint-sql-check ./internal/repository/queries
```

Three configuration details are non-obvious and worth surfacing:

- **`rowserrcheck.packages: [github.com/jackc/pgx/v5]`** — the linter's default packages list only covers `database/sql`, so without this teach it about pgx the linter silently no-ops on every pgx-based iteration in the codebase.
- **Generated code stays excluded.** `internal/repository/generated` is in `linters.exclusions.paths`. Skimatik regenerates the package on every schema change; lint findings there have nowhere to live. (Some skimatik downstreams lint generated code on equal footing — that's a deliberate, opposite tradeoff.)
- **`blueprint-vet` plugin replaces the standalone binary path.** The same R-1..R-12 rules surface inside `golangci-lint run` instead of needing a separate `make verify` invocation. `blueprint-sql-check` (the SQL-file checker, not Go AST) still runs as a standalone binary from `make lint` because it operates on `.sql` files outside the golangci-lint pipeline.

### Suppression policy

Applications built from this blueprint do **not** disable lint checks at the config level unless there is a very specific and nuanced reason that a *line of code* — not the whole codebase — needs the exemption. The default move is to fix the warning. When fixing genuinely loses signal, use a `//nolint:<rule> // <one-line rationale>` at the call site so the suppression is local, visible, and reviewable. The blueprint dogfoods this stance — every linter the blueprint enables runs unredacted against the canonical patterns in `EXAMPLE.md`, `ARCHITECTURE.md`, and the rest of the docs. If a check fights a canonical pattern, change the pattern.

Three blueprint-specific gates carry over from the previous config:

- **`forbidigo`: `*pgxkit.DB.Ping`** — `*pgxkit.DB` exposes `HealthCheck` and `IsReady`, not `Ping`. Scoped to `github.com/nhalm/pgxkit/v2` so Redis (which does have `Ping`) is unaffected. Requires `analyze-types: true`.
- **`forbidigo`: `canonlog.AddRequestFields`** — use `canonlog.InfoAdd(ctx, key, value)` or `canonlog.InfoAddMany(ctx, fields)`.
- **`revive use-any`** — enforce `any` over `interface{}` for the empty interface.

Layer-direction enforcement (`internal/models` cannot import upward; `internal/api` cannot import `repository` directly) lives in the `layerdirection` analyzer inside the blueprint-vet plugin — module-path-agnostic by construction, so consumers don't need to rename the rule.

### Errcheck patterns

`errcheck` is enabled and surfaces real bugs. The patterns that come up most often:

- **HTTP handlers** route encode failures through a `writeJSON(w, status, v)` helper that logs via canonlog.
- **`defer tx.Rollback(ctx)`** becomes `defer func() { _ = tx.Rollback(ctx) }()` — rollback after a successful commit returns `pgx.ErrTxDone`, and explicit discard is the canonical pattern.
- **Best-effort cleanup in tests** uses `_, _ = db.Exec(ctx, ...)`.

### Errorlint patterns

Error comparisons use `errors.Is` (not `==`); error wrapping uses `%w` (not `%v`). Both are enforced.

### Suppressing a finding

Suppressions land at the call site and name both the rule and the rationale, so a future reader can tell whether the suppression is still load-bearing:

```go
//nolint:rowserrcheck // rows.Err() is the caller's responsibility; see HandleRowsResult.
```

```go
// #nosec G304 -- user-supplied config path by design
```

## Gitignore

See [`templates/.gitignore`](templates/.gitignore). Generated artifacts are ignored: skimatik output (`internal/repository/generated/`), mockgen output (`*_interface_mock.go`), and swagger output (`docs/`). Fresh clones run `make generate` (which needs a live DB) and `make swagger` to materialize them. Compiled binaries land in `bin/` and are ignored too.

## Development Workflow

### Initial Setup

```bash
mkdir myapp && cd myapp
go mod init github.com/yourorg/myapp

# Lay out directories
mkdir -p cmd/myapp \
         internal/{config,models,repository,service,api,errors,database} \
         internal/database/migrations \
         internal/repository/queries

# Copy scaffolding from the blueprint's templates/ dir
cp path/to/go-blueprint/templates/Makefile .
cp path/to/go-blueprint/templates/docker-compose.yml .
cp path/to/go-blueprint/templates/skimatik.yaml .
cp path/to/go-blueprint/templates/.golangci.yml .
cp path/to/go-blueprint/templates/.custom-gcl.yml .
cp path/to/go-blueprint/templates/.env.example .
cp path/to/go-blueprint/templates/.gitignore .
cp path/to/go-blueprint/templates/lefthook.yml .
mkdir -p .github/workflows && cp path/to/go-blueprint/templates/.github/workflows/ci.yml .github/workflows/

# Replace "myapp" in Makefile with your actual module/binary name
sed -i '' 's/myapp/yourapp/g' Makefile docker-compose.yml skimatik.yaml .env.example

cp .env.example .env
# Fill in DATABASE_URL and any other secrets

make setup   # install dev tools + generate (requires dev DB)
lefthook install
```

### Daily Loop

```bash
make db-up           # start dev Postgres
make run             # go run cmd/myapp/main.go serve
make test            # fast unit tests, skip integration
make test-integration # full suite against the test container
make lint            # fmt + custom-gcl (golangci-lint + blueprint-vet plugin) + blueprint-sql-check
```

### Schema Change

```bash
# 1. Edit internal/database/schema.sql (for dev reset + skimatik introspection)
# 2. Add a new migration:
#    internal/database/migrations/NNNN_describe_change.up.sql
#    internal/database/migrations/NNNN_describe_change.down.sql

make migrate-up      # apply to dev
make generate        # skimatik regenerates repos, go generate regenerates mocks
```

### Dev DB Reset

```bash
make db-down && make db-up
docker exec -i myapp_db psql -U myapp -d postgres -c "DROP DATABASE IF EXISTS myapp;"
docker exec -i myapp_db psql -U myapp -d postgres -c "CREATE DATABASE myapp;"
docker exec -i myapp_db psql -U myapp -d myapp < internal/database/schema.sql
make generate
```

Production always uses `migrate up`, never schema.sql.
