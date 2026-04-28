# DevOps

Docker, Makefile, CI, environment variables, and the development workflow.

The [`templates/`](templates/) directory contains copy-ready versions of every config file. Grab them into a new project and replace `myapp` with your binary/module name.

## Git Hooks — Lefthook

See [`templates/lefthook.yml`](templates/lefthook.yml). Pre-commit runs `fmt`, `lint`, and `test` in parallel. Pre-push runs `test-integration`.

`lefthook` is installed by `make install-tools`. After copying the template, activate the hooks once:

```bash
lefthook install
```

## Docker Compose — Dev Database

See [`templates/docker-compose.yml`](templates/docker-compose.yml).

Dev Postgres lives on `5432`; the test database uses a separate container on `15432` (see the Makefile) so both can run concurrently without port conflicts.

## Makefile

See [`templates/Makefile`](templates/Makefile). Available targets:

- `setup` — install tools + generate
- `build` / `run` — build or run the app
- `test` — unit tests only (`-short`, skips integration)
- `test-integration` — full suite against a local test container
- `lint` — golangci-lint
- `db-up` / `db-down` — start/stop dev Postgres
- `migrate-up` / `migrate-down` — run migrations
- `generate` — skimatik + go generate
- `swagger` — regenerate OpenAPI docs

**Change the port per service** when running more than one locally. The template defaults to `15432` for the test DB; pick any unused high-range port.

## GitHub Actions CI

See [`templates/.github/workflows/ci.yml`](templates/.github/workflows/ci.yml). A few things to note:

- `mockgen` is installed in CI because `go generate ./...` invokes it.
- Migrations run with `DATABASE_URL`; `pgxkit.RequireDB(t)` reads `TEST_DATABASE_URL`. Both point at the same Postgres service — separate env var names make the intent clear.
- `-race` runs the race detector. `-short` is omitted so integration tests execute against the Postgres service.

## Environment Variables

See [`templates/.env.example`](templates/.env.example). `DATABASE_URL` is the only unconditionally required env var for `serve`. All other fields have defaults defined in [CONFIG.md](CONFIG.md).

## Golangci-lint Configuration

See [`templates/.golangci.yml`](templates/.golangci.yml). `internal/repository/generated` is excluded so skimatik's output doesn't trip the lint.

## Gitignore

See [`templates/.gitignore`](templates/.gitignore). Whether to git-ignore `internal/repository/generated/` is a policy call. Committing it makes fresh clones build without running skimatik; leaving it out forces every clone to run `make generate` (which needs a live DB). Decide per-project.

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
cp path/to/go-blueprint/templates/.env.example .
cp path/to/go-blueprint/templates/.gitignore .
cp path/to/go-blueprint/templates/lefthook.yml .
mkdir -p .github/workflows && cp path/to/go-blueprint/templates/.github/workflows/ci.yml .github/workflows/

# Replace "myapp" in Makefile with your actual module/binary name
sed -i '' 's/myapp/yourapp/g' Makefile docker-compose.yml skimatik.yaml .env.example

cp .env.example .env
# Fill in DATABASE_URL and any other secrets

make setup   # install-tools + generate (requires dev DB)
lefthook install
```

### Daily Loop

```bash
make db-up           # start dev Postgres
make run             # go run cmd/myapp/main.go serve
make test            # fast unit tests, skip integration
make test-integration # full suite against the test container
make lint
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
