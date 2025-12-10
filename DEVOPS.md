# DevOps Setup

This document covers Docker, Makefile, GitHub Actions CI, and environment configuration.

## Docker Compose

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

## Makefile

```makefile
.PHONY: help setup install-tools build run test lint clean db-up db-down migrate-up migrate-down generate swagger

help:
	@echo "Available targets:"
	@echo "  setup          - Complete project setup"
	@echo "  build          - Build the application"
	@echo "  run            - Run in development mode"
	@echo "  test           - Run tests"
	@echo "  lint           - Run linter"
	@echo "  db-up          - Start PostgreSQL"
	@echo "  db-down        - Stop PostgreSQL"
	@echo "  migrate-up     - Run migrations"
	@echo "  generate       - Generate repositories"
	@echo "  swagger        - Generate OpenAPI docs"

setup: install-tools generate
	@echo "Setup complete! Run 'make run' to start."

install-tools:
	@go install github.com/nhalm/skimatik/cmd/skimatik@latest
	@go install github.com/swaggo/swag/cmd/swag@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

build:
	@go build -o bin/myapp cmd/myapp/main.go

run:
	@go run cmd/myapp/main.go serve

test:
	@go test -v ./...

lint:
	@golangci-lint run

clean:
	@rm -rf bin/
	@go clean

db-up:
	@docker compose up -d postgres
	@sleep 3

db-down:
	@docker compose down

migrate-up: db-up
	@go run cmd/myapp/main.go migrate up

migrate-down:
	@go run cmd/myapp/main.go migrate down

generate: migrate-up
	@skimatik generate

swagger:
	@swag init -g cmd/myapp/main.go -o docs
```

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
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v4
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
      - name: Run tests
        env:
          DATABASE_URL: postgres://myapp:myapp_test@localhost:5432/myapp_test?sslmode=disable
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
      - name: Build
        run: go build -o bin/myapp cmd/myapp/main.go
```

## Environment Configuration

```bash
# .env.example

# Database
DATABASE_URL=postgres://myapp:myapp_dev@localhost:5432/myapp?sslmode=disable

# Server
PORT=8080
HOST=0.0.0.0

# Logging
LOG_LEVEL=info      # debug, info, warn, error
LOG_FORMAT=text     # text (logfmt), json

# Rate Limiting
RATE_LIMIT_READ_RPS=100
RATE_LIMIT_WRITE_RPS=20

# Request Limits
MAX_REQUEST_BODY_BYTES=1048576

# CORS
CORS_ALLOWED_ORIGINS=http://localhost:5173

# Environment
ENVIRONMENT=development
```

## Golangci-lint Configuration

```yaml
# .golangci.yml
run:
  timeout: 5m
  skip-dirs:
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
      enabled-tags:
        - diagnostic
        - performance
        - style
        - opinionated
      disabled-checks:
        - commentFormatting
        - unlambda
        - whyNoLint
        - unnamedResult
    revive:
      rules:
        - name: blank-imports
        - name: context-as-argument
        - name: context-keys-type
        - name: dot-imports
        - name: error-return
        - name: error-strings
        - name: error-naming
        - name: if-return
        - name: increment-decrement
        - name: var-naming
        - name: package-comments
        - name: range
        - name: receiver-naming
        - name: time-naming
        - name: unexported-return
        - name: indent-error-flow
        - name: errorf
        - name: empty-block
        - name: superfluous-else
        - name: unused-parameter
        - name: unreachable-code
        - name: redefines-builtin-id
    gocyclo:
      min-complexity: 15
    unparam:
      check-exported: false

formatters:
  enable:
    - gofmt
    - goimports
```

## Skimatik Configuration

```yaml
# skimatik.yaml
database:
  dsn: "postgres://myapp:myapp_dev@localhost:5432/myapp?sslmode=disable"
  schema: "public"

output:
  directory: "./internal/repository/generated"
  package: "generated"

tables:
  products:
    functions: ["create", "get", "update", "delete", "list", "paginate"]

queries:
  enabled: true
  directory: "./internal/repository/queries"
  files:
    - "products.sql"

default_functions: "all"
```

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

# Test binary
*.test

# Output of go coverage
*.out

# Dependency directories
vendor/

# IDE
.idea/
.vscode/
*.swp
*.swo

# Environment
.env
.env.local

# Generated
docs/

# OS
.DS_Store
Thumbs.db
```

## Development Workflow

### Initial Setup

```bash
# Clone or create project
mkdir myapp && cd myapp
go mod init github.com/yourorg/myapp

# Create directory structure
mkdir -p cmd/myapp/cmd internal/{models,repository,service,api,apperrors,database,id}
mkdir -p internal/database/migrations internal/repository/queries

# Copy template files
# (Copy all templates from templates/ directory)

# Create .env from example
cp .env.example .env

# Run full setup
make setup
```

### Daily Development

```bash
# Start database
make db-up

# Run server
make run

# Run tests
make test

# Lint code
make lint
```

### Database Changes

```bash
# 1. Update schema.sql with new table/columns
# 2. Create migration files in internal/database/migrations/

# Run migrations
make migrate-up

# Regenerate repositories
make generate

# Update custom repositories if needed
```

### Quick Database Reset

For development, you can reset the database quickly:

```bash
# Reset database completely
make db-down
make db-up

# Load schema directly (faster than migrations during development)
docker exec -i myapp_db psql -U myapp -d postgres -c "DROP DATABASE IF EXISTS myapp;"
docker exec -i myapp_db psql -U myapp -d postgres -c "CREATE DATABASE myapp;"
docker exec -i myapp_db psql -U myapp -d myapp < internal/database/schema.sql

# Regenerate repositories
skimatik generate
```

For production, always use migrations.
