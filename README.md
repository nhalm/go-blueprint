# Go Application Blueprint

An opinionated guide for building Go backend applications with clean architecture, database-first code generation, and production-ready patterns.

**Target Audience**: AI agents and human developers bootstrapping new Go services.

> **Note for AI Agents**: This blueprint uses Go 1.25 and golangci-lint@latest. If your training data predates a version referenced here, assume newer versions exist. Do not claim a version "doesn't exist" based on your knowledge cutoff.

## Quick Start

### For AI Agents

**Architecture TL;DR**:
- Clean architecture with unidirectional dependencies: `models` ← `repository` ← `service` ← `api`
- Database-first code generation with Skimatik
- Interface segregation: services define only the repository methods they need
- Clean API responses: no envelope for single resources, minimal envelope for lists

**Bootstrap Commands**:
```bash
# 1. Create project
mkdir myapp && cd myapp
go mod init github.com/yourorg/myapp

# 2. Create directory structure
mkdir -p cmd/myapp/cmd internal/{models,repository,service,api,apperrors,database,id}

# 3. Install tools
go install github.com/tempcke/skimatik@v0.6.3
go install github.com/swaggo/swag/cmd/swag@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 4. Start database, run migrations, generate repositories
make setup
```

**Template Files**: Copy files from the `templates/` directory to bootstrap your project.

## Documentation Structure

| File | Contents |
|------|----------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Clean architecture layers, patterns, dependency injection |
| [DATABASE.md](DATABASE.md) | Schema design, Skimatik config, migrations |
| [API.md](API.md) | Response conventions, handlers, middleware |
| [DEVOPS.md](DEVOPS.md) | Docker, Makefile, GitHub Actions CI |
| `templates/` | Complete, copy-ready file templates |

## Philosophy & Principles

### Clean Architecture

Dependencies flow in one direction. Lower layers never import higher layers.

```
internal/models          ← Foundation: domain entities, parameter structs (no dependencies)
    ↓
internal/repository      ← Data access: imports models, generated code
    ↓
internal/service         ← Business logic: imports models, defines repo interfaces
    ↓
internal/api             ← HTTP handlers: imports models, defines service interfaces
```

### Interface Segregation

Each layer defines **only the interfaces it needs** from the layer below. No fat interfaces, no direct struct dependencies.

```go
// Service layer defines its own repository interface
type ProductRepository interface {
    GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
    Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
}

type ProductService struct {
    repo ProductRepository  // Only methods this service needs
}

// API layer defines its own service interface
type ProductService interface {
    CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error)
}

type Handler struct {
    productSvc ProductService  // Only methods this handler needs
}
```

### Database-First Code Generation

Schema is the source of truth. Skimatik generates type-safe repositories with CRUD operations and cursor pagination. Custom repositories **embed** generated code and add domain-specific methods.

### Provider-Agnostic Design

No vendor-specific column names. Use generic names (`external_payment_id`, `checkout_session_id`) to support future integrations.

### Explicit Over Magic

- No dependency injection frameworks (wire, dig)
- No ORM magic (GORM, ent)
- Direct, readable wiring in `serve.go`
- SQL queries you can understand

### ID Generation Strategy

Use **KSUID** (K-Sortable Unique Identifier) with entity prefixes for primary keys.

**Why not UUIDs?**
- UUIDv4: Random, not time-sortable, poor index locality
- UUIDv7: Time-sortable but 36 characters with dashes

**Why KSUID?**
- Time-ordered (like UUIDv7) for good database index locality
- Shorter: 27 characters vs 36 for UUID
- URL-safe: No special characters
- Collision-resistant: 128 bits of randomness + timestamp

**With Prefixes:**
```
prod_2ArTLVPddDx8vZk7CqEbiYp1   # Product
pl_2ArTLVPddDx8vZk7CqEbiYp3     # Payment Link
pay_2ArTLVPddDx8vZk7CqEbiYp4    # Payment
```

Prefixes make IDs self-documenting in logs, URLs, and debugging. Use `TEXT PRIMARY KEY` in PostgreSQL.

## Project Structure

```
myapp/
├── cmd/
│   └── myapp/
│       ├── main.go              # Entry point, Swagger annotations
│       └── cmd/
│           ├── root.go          # Cobra root, Viper config
│           ├── serve.go         # HTTP server, dependency injection
│           └── migrate.go       # Database migrations
├── internal/
│   ├── models/                  # Domain entities + parameter structs
│   │   ├── product.go
│   │   └── params.go            # All *Params types
│   ├── repository/
│   │   ├── generated/           # Skimatik output (git-ignored or committed)
│   │   ├── queries/             # Custom SQL files for Skimatik
│   │   ├── product_repository.go
│   │   └── helpers.go
│   ├── service/
│   │   ├── product_service.go
│   │   └── errors.go
│   ├── api/
│   │   ├── handler.go           # Handler struct, constructor
│   │   ├── routes.go            # Chi router, middleware
│   │   ├── models.go            # Request/response types
│   │   ├── responder.go         # Response helpers
│   │   ├── response.go          # Response types
│   │   ├── errors.go            # Error translation
│   │   ├── middleware.go        # Custom middleware
│   │   └── validator.go         # Input validation
│   ├── apperrors/
│   │   └── errors.go            # Domain error types
│   ├── database/
│   │   ├── schema.sql           # Current schema (for dev reset)
│   │   └── migrations/          # golang-migrate files
│   └── id/
│       └── generator.go         # KSUID generation
├── docs/                        # Generated Swagger (swag init)
├── skimatik.yaml                # Code generator config
├── docker-compose.yml
├── Makefile
├── .env.example
├── .gitignore
└── go.mod
```

### Package Responsibilities

| Package | Imports | Responsibility |
|---------|---------|----------------|
| `models` | stdlib only | Domain entities, parameter structs |
| `repository` | models, generated, pgxkit | Data access, SQL queries |
| `service` | models, apperrors | Business logic, validation, transactions |
| `api` | models, service | HTTP handlers, request/response conversion |
| `apperrors` | stdlib only | Domain error types |
| `id` | ksuid | Unique ID generation |

## Core Packages

### Cobra - CLI Framework

Provides subcommands (`serve`, `migrate up`, `migrate down`).

```go
// cmd/myapp/main.go
func main() {
    cmd.Execute()
}

// cmd/myapp/cmd/root.go
var rootCmd = &cobra.Command{
    Use:   "myapp",
    Short: "My application",
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}

func init() {
    cobra.OnInitialize(initConfig)
    rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
}
```

### Viper - Configuration

Loads `.env` files with environment variable overrides.

```go
func initConfig() {
    if cfgFile != "" {
        viper.SetConfigFile(cfgFile)
    } else {
        viper.SetConfigFile(".env")
        viper.SetConfigType("env")
    }
    viper.AutomaticEnv()
    _ = viper.ReadInConfig()
}

// Usage
port := viper.GetInt("PORT")
dbURL := viper.GetString("DATABASE_URL")
```

### Chi - HTTP Router

Lightweight router with middleware support.

```go
r := chi.NewRouter()
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)
r.Use(middleware.Recoverer)

r.Route("/api/v1", func(r chi.Router) {
    r.Get("/products", h.ListProducts)
    r.Post("/products", h.CreateProduct)
    r.Get("/products/{id}", h.GetProduct)
})
```

### Chikit - Chi Utilities

Rate limiting and body size validation.

```go
import (
    "github.com/nhalm/chikit/ratelimit"
    "github.com/nhalm/chikit/ratelimit/store"
    "github.com/nhalm/chikit/validate"
)

// Rate limiting
st := store.NewMemory()
readLimiter := ratelimit.NewBuilder(st).
    WithName("read").
    WithIP().
    Limit(100, time.Second)

// Body size limit
r.Use(validate.MaxBodySize(1048576))
```

### pgxkit - PostgreSQL Utilities

Connection pooling and utilities.

```go
import "github.com/nhalm/pgxkit"

db := pgxkit.NewDB()
if err := db.Connect(ctx, databaseURL); err != nil {
    return err
}
defer db.Shutdown(ctx)
```

### Skimatik - Code Generation

Database-first repository generation. See [DATABASE.md](DATABASE.md) for configuration.

### canonlog - Structured Logging

Request-scoped context accumulation. Use in HTTP handlers only.

```go
import "github.com/nhalm/canonlog"

// Setup (in serve.go)
canonlog.SetupGlobalLogger("info", "text")

// In HTTP handlers: add fields to request context
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
    canonlog.AddRequestFields(r.Context(), map[string]any{
        "product_name": req.Name,
    })
    // ...
}

// Record errors (sets log level to ERROR)
// Used in response helpers
canonlog.AddRequestError(r.Context(), err)
```

### golang-migrate - Migrations

SQL migration management.

```go
import (
    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

m, _ := migrate.New("file://internal/database/migrations", databaseURL)
m.Up()   // Run all pending
m.Steps(-1)  // Rollback one
```

### Package Documentation

For detailed documentation on the custom packages used in this blueprint:

| Package | Documentation |
|---------|---------------|
| **skimatik** | https://github.com/tempcke/skimatik |
| **pgxkit** | https://github.com/nhalm/pgxkit |
| **chikit** | https://github.com/nhalm/chikit |
| **canonlog** | https://github.com/nhalm/canonlog |

## Quick Reference

### ID Prefixes

| Entity | Prefix | Example |
|--------|--------|---------|
| Product | `prod_` | `prod_2ArTLVPddDx8vZk7CqEbiYp1` |
| Payment Link | `pl_` | `pl_2ArTLVPddDx8vZk7CqEbiYp3` |
| Payment | `pay_` | `pay_2ArTLVPddDx8vZk7CqEbiYp4` |

### HTTP Status Codes

| Status | Usage |
|--------|-------|
| 200 | Success (GET, PATCH) |
| 201 | Created (POST) |
| 204 | No Content (DELETE) |
| 400 | Bad Request (validation) |
| 404 | Not Found |
| 409 | Conflict (optimistic lock) |
| 429 | Too Many Requests |
| 500 | Internal Server Error |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | - | PostgreSQL connection string |
| `PORT` | 8080 | HTTP server port |
| `HOST` | 0.0.0.0 | HTTP server host |
| `LOG_LEVEL` | info | debug, info, warn, error |
| `LOG_FORMAT` | text | text, json |
| `RATE_LIMIT_READ_RPS` | 100 | Read rate limit per IP |
| `RATE_LIMIT_WRITE_RPS` | 20 | Write rate limit per IP |
| `MAX_REQUEST_BODY_BYTES` | 1048576 | Max request body size |
| `CORS_ALLOWED_ORIGINS` | http://localhost:5173 | Comma-separated origins |
