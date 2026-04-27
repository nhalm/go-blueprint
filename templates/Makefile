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
