.PHONY: help setup install-tools build run test test-integration test-db-up test-db-down test-db-migrate lint clean db-up db-down migrate-up migrate-down generate swagger

GOLANGCI_LINT_VERSION ?= v2.11.4
BLUEPRINT_VET_VERSION ?= v0.2.0
CUSTOM_GCL            ?= ./bin/custom-gcl

TEST_DB_PORT      ?= 15432
TEST_DB_NAME      ?= myapp_test
TEST_DB_USER      ?= myapp
TEST_DB_PASS      ?= myapp_test
TEST_DB_CONTAINER ?= myapp_test_db
TEST_DATABASE_URL ?= postgres://$(TEST_DB_USER):$(TEST_DB_PASS)@localhost:$(TEST_DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

help:
	@echo "Available targets:"
	@echo "  setup            - Install dev tools and generate code"
	@echo "  install-tools    - Install dev tools without running generate"
	@echo "  build            - Build the application"
	@echo "  run              - Run in development mode"
	@echo "  test             - Run unit tests (skips integration)"
	@echo "  test-integration - Run full suite against test DB"
	@echo "  test-db-up       - Start test PostgreSQL container"
	@echo "  test-db-down     - Stop and remove test PostgreSQL container"
	@echo "  test-db-migrate  - Apply migrations to the test database"
	@echo "  lint             - Format, run custom-gcl, run blueprint-sql-check"
	@echo "  db-up            - Start development PostgreSQL"
	@echo "  db-down          - Stop development PostgreSQL"
	@echo "  migrate-up       - Run migrations against dev DB"
	@echo "  migrate-down     - Roll back the last migration"
	@echo "  generate         - Generate repositories and mocks"
	@echo "  swagger          - Generate OpenAPI docs"
	@echo "  clean            - Remove build artifacts"

setup: install-tools generate
	@echo "Setup complete! Run 'make run' to start."

# golangci-lint and blueprint-sql-check are bootstrapped on demand by `make
# lint`, so they're intentionally absent from this list.
install-tools:
	@go install github.com/nhalm/skimatik/v2/cmd/skimatik@latest
	@go install github.com/swaggo/swag/cmd/swag@latest
	@go install go.uber.org/mock/mockgen@latest
	@go install golang.org/x/tools/cmd/goimports@latest
	@go install github.com/evilmartians/lefthook@latest

build:
	@go build -o bin/myapp ./cmd/myapp

run:
	@go run ./cmd/myapp serve

test:
	@go test -v -short ./...

test-integration: test-db-up test-db-migrate
	@TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -v -race -coverprofile=coverage.txt ./...

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
	@DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/myapp migrate up

lint: $(CUSTOM_GCL)
	@go fmt ./...
	@$(CUSTOM_GCL) run ./...
	@command -v blueprint-sql-check >/dev/null 2>&1 || go install github.com/nhalm/blueprint-vet/cmd/blueprint-sql-check@$(BLUEPRINT_VET_VERSION)
	@blueprint-sql-check ./internal/repository/queries

# File target so custom-gcl rebuilds only when .custom-gcl.yml changes.
$(CUSTOM_GCL): .custom-gcl.yml
	@command -v golangci-lint >/dev/null 2>&1 || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@golangci-lint custom

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
	@go run ./cmd/myapp migrate up

migrate-down:
	@go run ./cmd/myapp migrate down

generate: migrate-up
	@skimatik generate
	@go generate ./...

swagger:
	@swag init -g ./cmd/myapp/main.go -o docs
