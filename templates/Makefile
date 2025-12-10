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

swagger:
	@swag init -g cmd/myapp/main.go -o docs
