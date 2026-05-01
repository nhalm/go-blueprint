.PHONY: smoke smoke-clean extract-docs

# Maintainer-side targets for the blueprint repo. These are not what consumers
# see — consumer-facing targets live in templates/Makefile.

SMOKE_DIR    ?= build/smoke
SMOKE_MODULE ?= github.com/example/smoketest
SMOKE_DOCS   := EXAMPLE.md,ARCHITECTURE.md,CONFIG.md,DATABASE.md,ERRORS.md,API.md

extract-docs:
	@go run ./scripts/extract-docs \
	    --docs $(SMOKE_DOCS) \
	    --templates templates \
	    --fixtures examples/_smoke-fixtures \
	    --module $(SMOKE_MODULE) \
	    --output $(SMOKE_DIR) \
	    --force

# Full bootstrap smoke test: extract canonical docs, scaffold a complete
# service, stand up Postgres, run migrations, run skimatik, build, and run
# `make lint` (gofmt + custom-gcl with the blueprint-vet plugin +
# blueprint-sql-check) end-to-end against the scaffolded service.
smoke: extract-docs
	@cd $(SMOKE_DIR) && \
	  echo "→ Seeding .env from .env.example…" && \
	  cp .env.example .env && \
	  echo "→ Installing tools…" && \
	  make install-tools >/dev/null && \
	  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest && \
	  echo "→ Starting Postgres…" && \
	  docker compose up -d postgres >/dev/null && \
	  until docker compose exec -T postgres pg_isready -U myapp >/dev/null 2>&1; do sleep 1; done && \
	  echo "→ Applying migrations…" && \
	  migrate -path internal/database/migrations -database "postgres://myapp:myapp_dev@localhost:5432/myapp?sslmode=disable" up && \
	  echo "→ Running skimatik…" && \
	  skimatik generate && \
	  echo "→ go mod tidy (1/2)…" && \
	  go mod tidy && \
	  echo "→ go generate (mocks)…" && \
	  go generate ./... && \
	  echo "→ go mod tidy (2/2 — pick up mock imports)…" && \
	  go mod tidy && \
	  echo "→ goimports -w (normalize indentation; markdown uses spaces, Go uses tabs)…" && \
	  goimports -w . && \
	  echo "→ go build…" && \
	  go build ./... && \
	  echo "→ make lint (custom-gcl + blueprint-vet plugin + blueprint-sql-check)…" && \
	  make lint && \
	  echo "→ make migrate-up (re-run via cmd/myapp to verify)…" && \
	  make migrate-up && \
	  echo "→ Tearing down Postgres…" && \
	  docker compose down -v >/dev/null && \
	  echo "✔ Smoke test green."

smoke-clean:
	@cd $(SMOKE_DIR) 2>/dev/null && docker compose down -v >/dev/null 2>&1 || true
	@rm -rf $(SMOKE_DIR)
