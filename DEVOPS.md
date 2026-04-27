# DevOps

Docker, Makefile, CI, environment variables, and the development workflow.

The [`templates/`](templates/) directory contains copy-ready versions of every file shown here (`Makefile`, `docker-compose.yml`, `.github/workflows/ci.yml`, `.golangci.yml`, `skimatik.yaml`, `.env.example`, `.gitignore`). Grab them into a new project and edit the name/port/module path.

## Docker Compose — Dev Database

```yaml
# docker-compose.yml
services:
  postgres:
    image: postgres:17-alpine
    container_name: myapp_db
    environment:
      POSTGRES_DB: myapp
      POSTGRES_USER: myapp
      POSTGRES_PASSWORD: myapp_dev
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U myapp"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  postgres_data:
```

Dev Postgres lives on `5432`; the test database uses a separate container on `15432` (see the Makefile below) so both can run concurrently without fighting over a port.

## Makefile

```makefile
.PHONY: help setup install-tools build run test test-integration test-db-up test-db-down test-db-migrate lint clean db-up db-down migrate-up migrate-down generate swagger

TEST_DB_PORT      ?= 15432
TEST_DB_NAME      ?= myapp_test
TEST_DB_USER      ?= myapp
TEST_DB_PASS      ?= myapp_test
TEST_DB_CONTAINER ?= myapp_test_db
TEST_DATABASE_URL ?= postgres://$(TEST_DB_USER):$(TEST_DB_PASS)@localhost:$(TEST_DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

help:
	@echo "Available targets:"
	@echo "  setup            - Complete project setup"
	@echo "  build            - Build the application"
	@echo "  run              - Run in development mode"
	@echo "  test             - Run unit tests (skips integration)"
	@echo "  test-integration - Run full suite against test DB"
	@echo "  test-db-up       - Start test PostgreSQL container"
	@echo "  test-db-down     - Stop and remove test PostgreSQL container"
	@echo "  test-db-migrate  - Apply migrations to the test database"
	@echo "  lint             - Run linter"
	@echo "  db-up            - Start development PostgreSQL"
	@echo "  db-down          - Stop development PostgreSQL"
	@echo "  migrate-up       - Run migrations against dev DB"
	@echo "  generate         - Generate repositories and mocks"
	@echo "  swagger          - Generate OpenAPI docs"

setup: install-tools generate
	@echo "Setup complete! Run 'make run' to start."

install-tools:
	@go install github.com/nhalm/skimatik/cmd/skimatik@latest
	@go install github.com/swaggo/swag/cmd/swag@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install go.uber.org/mock/mockgen@latest

build:
	@go build -o bin/myapp cmd/myapp/main.go

run:
	@go run cmd/myapp/main.go serve

test:
	@go test -v -short ./...

test-integration: test-db-up test-db-migrate
	@TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -v ./...

test-db-up:
	@docker run --name $(TEST_DB_CONTAINER) \
		-e POSTGRES_DB=$(TEST_DB_NAME) \
		-e POSTGRES_USER=$(TEST_DB_USER) \
		-e POSTGRES_PASSWORD=$(TEST_DB_PASS) \
		-p $(TEST_DB_PORT):5432 \
		-d postgres:17-alpine 2>/dev/null || true
	@echo "Waiting for test PostgreSQL on port $(TEST_DB_PORT)..."
	@for i in $$(seq 1 30); do \
		if docker exec $(TEST_DB_CONTAINER) pg_isready -U $(TEST_DB_USER) >/dev/null 2>&1; then \
			echo "Test PostgreSQL is ready"; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "Test PostgreSQL did not become ready"; exit 1

test-db-down:
	@docker rm -f $(TEST_DB_CONTAINER) 2>/dev/null || true

test-db-migrate:
	@DATABASE_URL="$(TEST_DATABASE_URL)" go run cmd/myapp/main.go migrate up

lint:
	@golangci-lint run

clean:
	@rm -rf bin/
	@go clean

db-up:
	@docker compose up -d postgres
	@echo "Waiting for PostgreSQL..."
	@until docker compose exec -T postgres pg_isready -U myapp > /dev/null 2>&1; do sleep 1; done
	@echo "PostgreSQL is ready"

db-down:
	@docker compose down

migrate-up: db-up
	@go run cmd/myapp/main.go migrate up

migrate-down:
	@go run cmd/myapp/main.go migrate down

generate: migrate-up
	@skimatik generate
	@go generate ./...

swagger:
	@swag init -g cmd/myapp/main.go -o docs
```

**Change the port per service** when you run more than one locally. The template defaults to `15432`; pick any unused port in the high range.

## GitHub Actions CI

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:17-alpine
        env:
          POSTGRES_DB: myapp_test
          POSTGRES_USER: myapp
          POSTGRES_PASSWORD: myapp_test
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true

      - name: Install mockgen
        run: go install go.uber.org/mock/mockgen@latest

      - name: Generate mocks
        run: go generate ./...

      - name: Apply migrations
        env:
          DATABASE_URL: postgres://myapp:myapp_test@localhost:5432/myapp_test?sslmode=disable
        run: go run cmd/myapp/main.go migrate up

      - name: Run tests
        env:
          TEST_DATABASE_URL: postgres://myapp:myapp_test@localhost:5432/myapp_test?sslmode=disable
        run: go test -v -race -coverprofile=coverage.txt ./...

      - name: Upload coverage
        uses: codecov/codecov-action@v4

  build:
    runs-on: ubuntu-latest
    needs: [lint, test]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true
      - name: Build
        run: go build -o bin/myapp cmd/myapp/main.go
```

A few things to note:
- `mockgen` is installed in CI because `go generate ./...` invokes it.
- Migrations run with `DATABASE_URL`; `pgxkit.RequireDB(t)` reads `TEST_DATABASE_URL`. Both point at the same Postgres service — separate env var names make the intent clear.
- `-race` runs the race detector. Skip `-short` so integration tests execute; the Postgres service is available.

## Environment Variables

```bash
# .env.example

# Database
DATABASE_URL=postgres://myapp:myapp_dev@localhost:5432/myapp?sslmode=disable
DB_MAX_CONNS=25
DB_MIN_CONNS=5
DB_MAX_CONN_LIFETIME_MINS=60
DB_MAX_CONN_IDLE_MINS=30

# HTTP server
HTTP_PORT=8080
HTTP_READ_TIMEOUT_SECONDS=15
HTTP_WRITE_TIMEOUT_SECONDS=15
HTTP_IDLE_TIMEOUT_SECONDS=60
HTTP_REQUEST_TIMEOUT_SECONDS=30

# Logging
LOG_LEVEL=info      # debug, info, warn, error
LOG_FORMAT=text     # text, json

# Rate limiting
RATE_LIMIT_REQUESTS=100
RATE_LIMIT_WINDOW_SECONDS=60

# Request body
MAX_REQUEST_BODY_BYTES=1048576

# Redis (optional — enables distributed rate limiting across replicas)
# REDIS_URL=redis://localhost:6379
# REDIS_PASSWORD=
# REDIS_DB=0
# REDIS_PREFIX=myapp:rl:
```

Values with defaults in [CONFIG.md](CONFIG.md) can be omitted. `DATABASE_URL` is the only unconditionally required env var for `serve`.

## Golangci-lint Configuration

```yaml
# .golangci.yml
run:
  timeout: 5m

issues:
  exclude-dirs:
    - internal/repository/generated

linters:
  default: none
  enable:
    - govet
    - unused
    - misspell
    - gocritic
    - revive
    - gocyclo
    - ineffassign
    - unconvert
    - unparam
    - wastedassign
    - prealloc
  settings:
    gocritic:
      enabled-tags: [diagnostic, performance, style, opinionated]
      disabled-checks: [commentFormatting, unlambda, whyNoLint, unnamedResult]
    revive:
      rules:
        - { name: blank-imports }
        - { name: context-as-argument }
        - { name: context-keys-type }
        - { name: dot-imports }
        - { name: error-return }
        - { name: error-strings }
        - { name: error-naming }
        - { name: if-return }
        - { name: var-naming }
        - { name: range }
        - { name: receiver-naming }
        - { name: time-naming }
        - { name: indent-error-flow }
        - { name: errorf }
        - { name: empty-block }
        - { name: superfluous-else }
        - { name: unused-parameter }
        - { name: unreachable-code }
        - { name: redefines-builtin-id }
    gocyclo:
      min-complexity: 15

formatters:
  enable:
    - gofmt
    - goimports
```

`internal/repository/generated` is excluded so skimatik's output doesn't trip the lint. Generated code is skimatik's problem, not yours.

## Gitignore

```
# .gitignore

# Binaries
bin/
*.exe
*.exe~
*.dll
*.so
*.dylib
*.test

# Coverage
*.out
coverage.txt

# Vendor
vendor/

# IDE
.idea/
.vscode/
*.swp
*.swo

# Local env
.env
.env.local

# Generated Swagger
docs/

# OS
.DS_Store
Thumbs.db
```

Whether to git-ignore `internal/repository/generated/` is a policy call. Committing it makes fresh clones build without running skimatik; leaving it out forces every clone to run `make generate` (which needs a live DB). Cloak commits it. Decide per-project.

## Development Workflow

### Initial Setup

```bash
mkdir myapp && cd myapp
go mod init github.com/yourorg/myapp

# Lay out directories
mkdir -p cmd/myapp \
         internal/{config,models,repository,service,api,errors,id,database} \
         internal/database/migrations \
         internal/repository/queries

# Copy scaffolding from the blueprint's templates/ dir
cp path/to/go-blueprint/templates/Makefile .
cp path/to/go-blueprint/templates/docker-compose.yml .
cp path/to/go-blueprint/templates/skimatik.yaml .
cp path/to/go-blueprint/templates/.golangci.yml .
cp path/to/go-blueprint/templates/.env.example .
cp path/to/go-blueprint/templates/.gitignore .
mkdir -p .github/workflows && cp path/to/go-blueprint/templates/.github/workflows/ci.yml .github/workflows/

# Replace "myapp" in Makefile with your actual module/binary name
sed -i '' 's/myapp/yourapp/g' Makefile docker-compose.yml skimatik.yaml .env.example

cp .env.example .env
# Fill in DATABASE_URL and any other secrets

make setup   # install-tools + generate (requires dev DB)
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
