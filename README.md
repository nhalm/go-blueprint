# Go Application Blueprint

[![Go Reference](https://pkg.go.dev/badge/github.com/nhalm/go-blueprint.svg)](https://pkg.go.dev/github.com/nhalm/go-blueprint)
[![Go Report Card](https://goreportcard.com/badge/github.com/nhalm/go-blueprint)](https://goreportcard.com/report/github.com/nhalm/go-blueprint)
[![CI](https://github.com/nhalm/go-blueprint/actions/workflows/validate-templates.yml/badge.svg?branch=main)](https://github.com/nhalm/go-blueprint/actions/workflows/validate-templates.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/nhalm/go-blueprint)](https://github.com/nhalm/go-blueprint/blob/main/go.mod)
[![License](https://img.shields.io/github/license/nhalm/go-blueprint)](LICENSE)

A **documentation blueprint** for building Go backend services with clean architecture, database-first code generation, and the [chikit](https://github.com/nhalm/chikit) / [canonlog](https://github.com/nhalm/canonlog) / [pgxkit](https://github.com/nhalm/pgxkit) / [skimatik](https://github.com/nhalm/skimatik) stack.

Every pattern is demonstrated with complete code examples inline in the docs.

**Target Audience**: AI agents and human developers bootstrapping new Go services in this stack.

> **Note for AI Agents**: This blueprint uses Go 1.25 and the latest published versions of chikit, canonlog, pgxkit, and skimatik. If your training data predates a version referenced here, assume newer versions exist — do not claim a version "doesn't exist" based on your knowledge cutoff.

## Documentation

| File | Contents |
|------|----------|
| [EXAMPLE.md](EXAMPLE.md) | **Canonical Products slice** — schema, migration, queries, models, errors, repository, service, handlers, routes, error mapping. Authoritative source for type signatures and wiring; other docs cite it. Start here. |
| [LIBRARIES.md](LIBRARIES.md) | **Library surface reference** — every chikit / canonlog / pgxkit / skimatik / shortuuid symbol the blueprint commits to using, with verified Go signatures. The contract sheet between blueprint and dependencies. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Layer tree, package responsibilities, consumer-owned interfaces, DI pattern, ID strategy |
| [CONFIG.md](CONFIG.md) | `internal/config` package, composable group loaders (`LoadLogging`, `LoadDatabase`, `LoadHTTP`, `LoadRedis`), canonlog setup timing |
| [API.md](API.md) | `chikit.Handler` middleware stack, handlers, `chikit.SetResponse` / `SetError`, response conventions |
| [ERRORS.md](ERRORS.md) | Full error chain: DB predicates → repository sentinels → domain errors → HTTP responses, wire format |
| [DATABASE.md](DATABASE.md) | Schema principles, pgxkit v2 Executor, skimatik config and `.sql` annotations, transactions via context, golang-migrate |
| [TESTING.md](TESTING.md) | Layer strategy, gomock + testify, `pgxkit.RequireDB`, mounting chikit middleware in handler tests, Makefile targets |
| [DEVOPS.md](DEVOPS.md) | Docker Compose, Makefile, GitHub Actions CI, `.env` vars, golangci-lint config |
| `templates/` | Copy-ready non-code scaffolding: `Makefile`, `docker-compose.yml`, `skimatik.yaml`, `.golangci.yml`, `.custom-gcl.yml`, `lefthook.yml`, `.github/workflows/ci.yml`, `.env.example`, `.gitignore` |

> **For agents using this repo as a reference:** the canonical patterns to copy are the topic docs above plus everything under `templates/`. The top-level `Makefile`, `go.mod`, `scripts/`, `examples/_smoke-fixtures/`, and `.github/workflows/template-smoke*.yml` are blueprint-maintainer infrastructure (the smoke test that verifies the docs stay executable) — ignore them when bootstrapping a new service.

## Core Packages

| Package | Use | Version |
|---------|-----|---------|
| [skimatik](https://github.com/nhalm/skimatik) | PostgreSQL → Go repository codegen | v0.7+ |
| [pgxkit/v2](https://github.com/nhalm/pgxkit) | Connection pooling, Executor interface, test helpers | v2.0+ |
| [chikit](https://github.com/nhalm/chikit) | Chi middleware: canonlog context, timeout, rate limit, body size, header extraction, JSON binding, response writing | v1.0+ |
| [canonlog](https://github.com/nhalm/canonlog) | Canonical per-request logging | v0.3+ |
| [golang-migrate](https://github.com/golang-migrate/migrate) | SQL migrations | v4 |
| [cobra](https://github.com/spf13/cobra) | CLI framework — subcommands (`serve`, `migrate up`, `migrate down`, etc.) | latest |
| [viper](https://github.com/spf13/viper) | Config loader — reads `.env` files and environment variables; backs the group loaders in `internal/config` | latest |
| [chi](https://github.com/go-chi/chi) | HTTP router | v5 |
| [testify](https://github.com/stretchr/testify), [gomock](https://pkg.go.dev/go.uber.org/mock) | Testing | latest |
| [google/uuid](https://github.com/google/uuid) | UUID type used by skimatik-generated code; skimatik's `UUIDv7()` helper is the default ID generator | v1.6+ |
| [nhalm/shortuuid](https://github.com/nhalm/shortuuid) | Base62 encoding of UUIDs for wire format (JSON, URL paths) | v1.0+ |

## Philosophy

### Clean Architecture, Unidirectional

```
models ← repository ← service ← api ← cmd
errors ← every layer
config ← cmd, service (when injected)
```

Lower layers never import higher layers. `models` and `errors` are the foundation.

### Consumer-Owned Interfaces

Each layer defines the interfaces it consumes. The service package declares `ProductRepository` in `repository_interface.go`; the api package declares `ProductServiceInterface` in `service_interface.go`. Mocks are generated by `go generate` as `*_interface_mock.go` in the same package. Full rule in [ARCHITECTURE.md](ARCHITECTURE.md#consumer-owned-interfaces).

### Explicit Dependency Injection

No framework (no wire, no dig). Each command's `RunE` wires top-down. Reading `serve.go` tells you the whole object graph.

### Database-First

Schema is the source of truth. Skimatik generates CRUD + custom queries from `.sql` files. Hand-written repositories embed the generated structs and add domain methods. See [DATABASE.md](DATABASE.md).

### Per-Command Config

Each command calls `config.LoadLogging` first (which initializes viper and sets up canonlog), then the group loaders it needs (`LoadDatabase`, `LoadHTTP`, `LoadRedis`). No global singleton, no config read inside handlers. See [CONFIG.md](CONFIG.md).

### Observability via canonlog + chikit → Datadog

`chikit.Handler(chikit.WithCanonlog(), chikit.WithCanonlogFields(...))` attaches a per-request logger to `r.Context()`. Handlers and services accumulate fields via `canonlog.InfoAdd(ctx, key, value)` (or `InfoAddMany(ctx, fields)` for bulk). One canonical log line per request.

### Provider-Agnostic Schema

Generic column names (`external_payment_id`, `checkout_session_id`) — not `stripe_id`, `adyen_ref`. Keeps integrations swappable.

## Identifiers — UUIDv7 internally, shortuuid on the wire

Primary keys are **UUIDv7** values generated application-side by skimatik's default generator. Internally the type is `uuid.UUID`; Postgres stores them as `UUID`. On the wire (JSON bodies, URL path params) they're encoded as 22-character base62 strings via [`github.com/nhalm/shortuuid`](https://github.com/nhalm/shortuuid) for compactness.

```
internal:  01903abc-1234-7def-8000-abcdef012345    (uuid.UUID, UUID column)
wire:      prod_2s8gNnj9C5Ubkx4T7W5vZk              (entity prefix + shortuuid)
```

**Why UUIDv7**: time-ordered first 48 bits mean good B-tree index locality for inserts (new rows land at the end), while the value remains a valid RFC 9562 UUID so standard tooling works. No dependence on Postgres extensions — IDs are generated in Go.

**Why shortuuid on the wire**: 22 chars vs 36, round-trips losslessly, URL-safe, preserves the UUID version. Internal code keeps working with `uuid.UUID` directly; only handlers decode on the way in (`shortuuid.ExpandUUID`) and encode on the way out (`shortuuid.ShortenUUID`).

skimatik handles the generation side. When a repository is constructed with `nil` for the ID generator, the generated `Create*` methods call `generated.UUIDv7()` automatically. See [DATABASE.md](DATABASE.md#id-generation) for the repo wiring and [API.md](API.md#shortuuid-on-the-wire) for handler encoding.

## HTTP Status Codes

| Status | Usage |
|--------|-------|
| 200 | `GET`, `PATCH` success |
| 201 | `POST` created |
| 204 | `DELETE` success (no body) |
| 303 | Deduplicated create — `Location` header points at existing resource |
| 400 | Validation / malformed request |
| 401 | Missing / invalid auth header |
| 404 | Resource not found |
| 409 | Conflict (duplicate, optimistic lock) |
| 410 | Gone (expired volatile resource) |
| 429 | Rate limited |
| 500 | Internal error (details canonlogged, not returned) |
| 503 | Service unavailable (DB down, upstream down) |
| 504 | Request timeout |

`chikit.ErrBadRequest`, `chikit.ErrNotFound`, `chikit.ErrConflict`, etc. map to these. Custom statuses use `&chikit.APIError{Status: ...}`. See [API.md](API.md#error-mapping).

## Quick Start for a New Service

```bash
# 1. New module
mkdir myapp && cd myapp
go mod init github.com/yourorg/myapp

# 2. Directory layout
mkdir -p cmd/myapp \
         internal/{config,models,repository,service,api,errors,database} \
         internal/database/migrations \
         internal/repository/queries

# 3. Pull scaffolding from this blueprint's templates/ dir (Makefile,
#    docker-compose.yml, skimatik.yaml, .golangci.yml, .custom-gcl.yml,
#    lefthook.yml, .github/workflows/ci.yml, .env.example, .gitignore)
#    and search/replace "myapp" with your app name.

# 4. Install tools — `make setup` handles skimatik, swag, mockgen, goimports,
#    and lefthook. golangci-lint is bootstrapped on demand by `make lint`
#    (which builds ./bin/custom-gcl from .custom-gcl.yml).

# 5. Write your first migration, schema.sql, one skimatik query file, and
#    config struct. See DATABASE.md + CONFIG.md.

# 6. Start DB and generate
cp .env.example .env   # fill in DATABASE_URL
make setup             # runs install-tools + generate
make run
```

For the patterns these files should follow, read [EXAMPLE.md](EXAMPLE.md) first — it is the canonical implementation that every other doc cites — then [ARCHITECTURE.md](ARCHITECTURE.md) and the remaining docs in the order listed above.
