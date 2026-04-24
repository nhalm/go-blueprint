# Architecture

This document describes the layer structure, package responsibilities, and dependency flow of services built on the blueprint. For a complete working reference, see [`github.com/nhalm/cloak`](https://github.com/nhalm/cloak) — it demonstrates every pattern described here.

## Layer Tree

```
cmd/<app>/                  # Cobra entry point — one command per file
  ├── main.go               # Executes root.Execute()
  ├── root.go               # Root cobra.Command, initConfig (viper .env + AutomaticEnv)
  ├── serve.go              # runServe — loads Config, wires deps, runs HTTP server
  ├── migrate.go            # runMigrateUp/Down/Version — uses config.LoadDatabaseOnly()
  └── <other>.go            # Additional commands (cleanup jobs, docs generator, etc.)

internal/
  ├── config/               # Typed Config struct + Load() / LoadDatabaseOnly()
  ├── models/               # Domain entities + input/output types (no internal deps)
  ├── repository/           # Data access — embeds skimatik-generated code
  │   ├── generated/        # skimatik output (may be git-ignored)
  │   ├── queries/          # Custom SQL files (.sql) consumed by skimatik
  │   └── *_repository.go   # Hand-written repos that embed generated CRUD
  ├── service/              # Business logic
  │   ├── interfaces.go     # Consumer-owned repo interfaces + //go:generate mockgen
  │   └── *_service.go
  ├── api/                  # HTTP layer
  │   ├── handler.go        # Handler struct, service interfaces, //go:generate mockgen
  │   ├── routes.go         # Chi router + chikit.Handler middleware stack
  │   ├── errors.go         # handleServiceError: apperrors → chikit.SetError
  │   ├── validators.go     # Custom validator tags registered with chikit
  │   └── *.go              # Per-resource handlers (aliases.go, products.go, ...)
  ├── errors/               # Domain errors (sentinel vars + ValidationError struct)
  ├── id/                   # KSUID generation with entity prefixes
  └── testutil/             # Optional: shared fixture factories (NOT a GetTestDB helper)

test/e2e/                   # Optional end-to-end tests with real httptest.Server + DB
```

## Dependency Flow

```
models  ←  repository  ←  service  ←  api  ←  cmd
errors  ←  every layer
config  ←  cmd, service (when cfg is injected)
id      ←  repository (for prefixed KSUIDs)
```

Lower layers never import higher layers. `models` and `errors` are the foundation — they import nothing from `internal/*`.

| Package | Imports from | Responsibility |
|---------|--------------|----------------|
| `models` | stdlib | Domain entities, request/response input types |
| `errors` | stdlib | Sentinel error values, `ValidationError`/`FieldError` structs |
| `id` | `github.com/segmentio/ksuid` | `NewAccountID()`, `NewProductID()`, etc. |
| `repository` | `models`, `errors`, generated, `pgxkit/v2` | SQL queries, transaction boundaries |
| `service` | `models`, `errors`, consumer-owned repo interfaces | Business logic, cross-repo orchestration |
| `api` | `models`, `errors`, consumer-owned service interfaces, `chikit` | HTTP transport, request/response mapping |
| `config` | `viper` | Typed config, validation, defaults |

## Consumer-Owned Interfaces

Each layer defines the interfaces it consumes — it never imports the concrete type from the layer below.

**Where the interfaces live is flexible** — cloak puts service interfaces in `internal/service/interfaces.go` (all service-consumed interfaces in one file) but keeps API interfaces inline at the top of `internal/api/handler.go` (all handler-consumed service interfaces there). The `//go:generate mockgen` directive points at whichever file contains them.

```go
// internal/service/interfaces.go
//go:generate mockgen -source=interfaces.go -destination=mock_interfaces_test.go -package=service

package service

type ProductRepository interface {
    Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
    // ...
}
```

```go
// internal/api/handler.go
//go:generate mockgen -source=handler.go -destination=mock_handler_test.go -package=api

package api

type ProductServiceInterface interface {
    CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    // ...
}

type Handler struct {
    productService ProductServiceInterface
    db             *pgxkit.DB
    config         config.Config
}
```

Running `go generate ./...` produces the `mock_*_test.go` files. See [TESTING.md](TESTING.md) for how they're used.

## Explicit Dependency Injection

No DI framework. Each command's `RunE` wires dependencies top-down. Reading `serve.go` should tell you the entire object graph.

```go
// cmd/<app>/serve.go
func runServe(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    cfg, err := config.Load()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    db := pgxkit.NewDB()
    if err := db.Connect(ctx, cfg.DatabaseURL,
        pgxkit.WithMaxConns(cfg.DBMaxConns),
        pgxkit.WithMinConns(cfg.DBMinConns),
        pgxkit.WithMaxConnLifetime(cfg.DBMaxConnLifetime),
        pgxkit.WithMaxConnIdleTime(cfg.DBMaxConnIdleTime),
    ); err != nil {
        return fmt.Errorf("failed to connect to database: %w", err)
    }
    defer db.Shutdown(ctx)

    productRepo := repository.NewProductRepository(db, id.NewProductID)
    productSvc := service.NewProductService(productRepo, cfg)
    handler := api.NewHandler(productSvc, db, cfg)

    rateLimitStore := store.NewMemory() // or store.NewRedis(...)
    router := api.Routes(handler, rateLimitStore)

    // ... http.Server setup + graceful shutdown ...
}
```

See [CONFIG.md](CONFIG.md) for the `config` package and per-command loaders. See [API.md](API.md) for the `api.Routes` middleware stack.

## Validation Strategy

Two layers with distinct responsibilities:

**API layer — structural validation.** Struct tags via `validator.v10` (registered through `chikit.Binder()` / `chikit.JSON()`). Catches required fields, length limits, format (email, URL, enum values), custom tags. Run before any service call. Failures map to `chikit.ErrBadRequest` or a structured `chikit.NewValidationError([]chikit.FieldError{...})`.

**Service layer — business validation.** Anything that needs database state or cross-field context (duplicate names, referenced resources exist, transitions allowed). Failures return a domain error from `internal/errors` (typically a sentinel like `errors.ErrDuplicateToken` or a `*errors.ValidationError`), which the API layer translates via `handleServiceError`. See [API.md](API.md#error-mapping) for the full translation table.

Don't duplicate. Structural validation only in the API layer, business validation only in the service layer.

## ID Strategy — KSUID with prefixes

Primary keys are `TEXT` columns holding **prefixed KSUIDs**:

```
prod_2ArTLVPddDx8vZk7CqEbiYp1   # Product
acc_2ArTLVPddDx8vZk7CqEbiYp2    # Account
tok_2ArTLVPddDx8vZk7CqEbiYp3    # Token
```

**Why KSUID over UUID**: time-ordered (good index locality, like UUIDv7), 27 chars vs 36, URL-safe, 128 bits of randomness + timestamp.

**Why prefixes**: IDs are self-documenting in logs, URLs, and debugging. You can tell `prod_...` apart from `tok_...` by eye.

The `internal/id` package exposes one constructor per entity:

```go
// internal/id/generator.go
package id

import "github.com/segmentio/ksuid"

func NewProductID() string { return "prod_" + ksuid.New().String() }
func NewAccountID() string { return "acc_"  + ksuid.New().String() }
```

Repositories take the constructor as a function value so the ID format can be substituted in tests:

```go
repository.NewProductRepository(db, id.NewProductID)
```

## What Does Not Belong in `internal/`

- Shared utility code that would be reused across services — publish it as a separate module (this is where pgxkit, chikit, canonlog, skimatik came from).
- Framework-agnostic domain logic that other services might need — same answer.
- Test doubles / mocks that aren't tied to a specific package's interfaces — put them in that package's test files.

## Working Reference

Every pattern above is implemented in [`github.com/nhalm/cloak`](https://github.com/nhalm/cloak). When a snippet here feels incomplete, look at the equivalent file in cloak.

| Pattern | Cloak file |
|---------|-----------|
| Config struct + loaders | `internal/config/config.go` |
| Command wiring | `cmd/cloak/serve.go`, `cmd/cloak/migrate.go` |
| Consumer-owned interfaces | `internal/service/interfaces.go`, `internal/api/handler.go` |
| Middleware stack | `internal/api/routes.go` |
| Error translation | `internal/api/errors.go`, `internal/errors/errors.go` |
| Handler pattern | `internal/api/aliases.go` |
| Repository pattern | `internal/repository/*_repository.go` |
| Tests per layer | `internal/service/*_test.go`, `internal/api/handler_test.go`, `internal/repository/repository_integration_test.go` |
