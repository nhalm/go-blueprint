# Database Layer

Schema design, repositories, queries, and migrations — built on **pgxkit v2**, **skimatik v0.7+**, and **golang-migrate**.

## Tool Prerequisites

```bash
# skimatik — generates type-safe repositories from your PostgreSQL schema
go install github.com/nhalm/skimatik/cmd/skimatik@latest

# pgxkit v2 — connection pooling + Executor interface the generated code uses
go get github.com/nhalm/pgxkit/v2

# google/uuid — UUID type used by the generated code; skimatik's generator
# package embeds a UUIDv7() helper backed by uuid.NewV7()
go get github.com/google/uuid

# golang-migrate — migration driver (used inside cmd/<app>/migrate.go; no binary needed)
go get github.com/golang-migrate/migrate/v4
```

## Schema Principles

1. **`UUID PRIMARY KEY`** (no `DEFAULT`). UUIDv7 values are generated app-side by skimatik; the DB column is the plain `UUID` type. See [ARCHITECTURE.md](ARCHITECTURE.md#id-strategy--uuidv7--shortuuid).
2. **Always** `created_at` + `updated_at` (`TIMESTAMPTZ NOT NULL DEFAULT NOW()`).
3. **Soft deletes** via `deleted_at TIMESTAMPTZ` (nullable). Filter `WHERE deleted_at IS NULL` in every read query. Hard deletes are a separate cleanup job.
4. **Provider-agnostic column names**: `external_payment_id`, `checkout_session_id` — not `stripe_id`, `adyen_ref`. Keeps integrations swappable.
5. **Partial indexes for active rows**: index on `(column) WHERE deleted_at IS NULL`.

```sql
CREATE TABLE products (
    id            UUID PRIMARY KEY,
    account_id    UUID NOT NULL REFERENCES accounts(id),
    name          VARCHAR(255) NOT NULL,
    description   TEXT,
    active        BOOLEAN NOT NULL DEFAULT true,
    metadata      JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX idx_products_account_active
    ON products(account_id, active)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX idx_products_account_name
    ON products(account_id, name)
    WHERE deleted_at IS NULL;
```

## skimatik Configuration

One `skimatik.yaml` at the project root. Lists tables to generate CRUD for, plus `.sql` files holding custom queries.

```yaml
# skimatik.yaml
database:
  dsn: "postgres://myapp:myapp_dev@localhost:5432/myapp?sslmode=disable"
  schema: "public"

output:
  directory: "./internal/repository/generated"
  package:   "generated"

default_functions: "all"

tables:
  accounts:
  products:

queries:
  enabled: true
  directory: "./internal/repository/queries"
  files:
    - "products.sql"
```

`default_functions: "all"` gives you `Create`/`Get`/`Update`/`Delete`/`List`/`Paginate` per table. Override per table with `functions: [get, list]` when you don't want writes generated (read-only tables, for example).

Generation runs against a live database — the dev Postgres must be up with migrations applied:
```bash
make db-up
make migrate-up
make generate   # runs: skimatik generate && go generate ./...
```

## Custom SQL Queries

Queries that can't be expressed as plain CRUD live in `.sql` files with skimatik annotations. Each query becomes a method on a generated `*Queries` struct.

```sql
-- internal/repository/queries/products.sql

-- name: GetProductByAccountAndID :one
SELECT id, account_id, name, description, active, metadata, created_at, updated_at
FROM products
WHERE account_id = $1
  AND id = $2
  AND deleted_at IS NULL;

-- name: ListProductsPaginated :paginated
SELECT id, account_id, name, description, active, created_at, updated_at
FROM products
WHERE account_id = $1
  AND deleted_at IS NULL
  AND ($2::boolean IS NULL OR active = $2)
ORDER BY id ASC;

-- name: SoftDeleteProduct :exec
UPDATE products
SET deleted_at = NOW(), updated_at = NOW()
WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL;
```

### Annotation Reference

| Annotation | Generates | Notes |
|------------|-----------|-------|
| `:one`       | `func(ctx, exec, params...) (*Row, error)` | Returns one row or `ErrNotFound`. |
| `:many`      | `func(ctx, exec, params...) ([]Row, error)` | Returns all matching rows. |
| `:paginated` | `func(ctx, exec, params..., pagination PaginationParams) (*PaginationResult[Row], error)` | Cursor pagination in both directions. Requires an `ORDER BY` clause. |
| `:exec`      | `func(ctx, exec, params...) error` | No result set — inserts, updates, deletes. |

`:paginated` requires an explicit `ORDER BY`. Ascending order uses `>` forward / `<` backward; descending flips that. The generated `PaginationResult[T]`:

```go
type PaginationParams struct {
    Limit        int    // Default 20, max 100
    NextCursor   string // For forward pagination
    BeforeCursor string // For backward pagination
}

type PaginationResult[T any] struct {
    Items        []T
    HasMore      bool   // More items exist forward
    HasPrevious  bool   // Items exist backward
    NextCursor   string
    BeforeCursor string
}
```

## Repository Pattern

Hand-written repos **embed** the generated `Repository` (CRUD) and `Queries` (custom SQL) structs. Domain methods return `*models.X`, not `*generated.X`.

```go
// internal/repository/product.go
package repository

import (
    "context"

    "github.com/google/uuid"
    "github.com/nhalm/pgxkit/v2"
    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository/generated"
)

type ProductRepository struct {
    *generated.ProductsRepository
    *generated.ProductsQueries
    db *pgxkit.DB
}

func NewProductRepository(db *pgxkit.DB) *ProductRepository {
    return &ProductRepository{
        ProductsRepository: generated.NewProductsRepository(nil), // nil = default UUIDv7
        ProductsQueries:    generated.NewProductsQueries(),
        db:                 db,
    }
}

func (r *ProductRepository) GetByAccountAndID(ctx context.Context, accountID, id uuid.UUID) (*models.Product, error) {
    row, err := r.GetProductByAccountAndID(ctx, executorFromContext(ctx, r.db), accountID, id)
    if err != nil {
        return nil, translateError(err)
    }
    return toProductModel(row), nil
}

func toProductModel(p *generated.Products) *models.Product {
    return &models.Product{
        ID:          p.Id,
        AccountID:   p.AccountId,
        Name:        p.Name,
        Description: p.Description,
        Active:      p.Active,
        CreatedAt:   p.CreatedAt,
        UpdatedAt:   p.UpdatedAt,
    }
}
```

`generated.NewProductsRepository(nil)` wires in skimatik's default `UUIDv7()` ID generator — every `Create*` call produces a time-sortable UUIDv7. Pass a `func() uuid.UUID` instead of `nil` to override (for deterministic test IDs, or to swap in `UUIDv4` for non-primary-key use cases).

The generated repo stores **only** the ID generator, not the db. The db (or transaction) is supplied **per call** via a `pgxkit.Executor` — that's what `executorFromContext` returns. This is what makes transactional orchestration clean at the service layer.

## ID Generation

Every skimatik run produces a `generated/id_generators.go` with `UUIDv7()` and `UUIDv4()` helpers:

```go
// internal/repository/generated/id_generators.go (generated)
package generated

import "github.com/google/uuid"

func UUIDv7() uuid.UUID { id, _ := uuid.NewV7(); return id }
func UUIDv4() uuid.UUID { return uuid.New() }
```

When a repo is constructed with `nil`, the generated `Create*` methods call `UUIDv7()` for each insert. This is the default for everything — primary keys across all tables end up as UUIDv7.

**Overriding the default** (rare — for tests or non-PK identifiers):

```go
// UUIDv4 instead (non-primary-key case, e.g., correlation IDs)
repo := &MyRepository{
    MyRepo: generated.NewMyRepository(func() uuid.UUID { return generated.UUIDv4() }),
    // ...
}

// Deterministic for tests
fixed := uuid.MustParse("01903abc-1234-7000-8000-000000000001")
repo := &MyRepository{
    MyRepo: generated.NewMyRepository(func() uuid.UUID { return fixed }),
    // ...
}
```

Because IDs are application-generated, the `UUID PRIMARY KEY` column in the schema has **no** `DEFAULT` clause — the `Create*` params always supply the id.

## Transactions — Context-Carried

Services that need to span multiple repositories in one transaction use a `TxManager`:

```go
// internal/repository/tx.go
package repository

import (
    "context"

    "github.com/jackc/pgx/v5"
    "github.com/nhalm/pgxkit/v2"
)

type ctxKey struct{}

func ContextWithTx(ctx context.Context, tx *pgxkit.Tx) context.Context {
    return context.WithValue(ctx, ctxKey{}, tx)
}

func TxFromContext(ctx context.Context) *pgxkit.Tx {
    tx, _ := ctx.Value(ctxKey{}).(*pgxkit.Tx)
    return tx
}

// executorFromContext returns the active transaction if present, else the db.
// Generated repository methods accept this executor.
func executorFromContext(ctx context.Context, db *pgxkit.DB) pgxkit.Executor {
    if tx := TxFromContext(ctx); tx != nil {
        return tx
    }
    return db
}

type TxManager struct{ db *pgxkit.DB }

func NewTxManager(db *pgxkit.DB) *TxManager { return &TxManager{db: db} }

func (m *TxManager) BeginTx(ctx context.Context) (context.Context, func() error, func(context.Context) error, error) {
    tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return ctx, nil, nil, err
    }
    txCtx := ContextWithTx(ctx, tx)
    commit   := func() error                 { return tx.Commit(ctx) }
    rollback := func(ctx context.Context) error { return tx.Rollback(ctx) }
    return txCtx, commit, rollback, nil
}
```

Service usage:

```go
func (s *ProductService) CreateWithAudit(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
    txCtx, commit, rollback, err := s.tx.BeginTx(ctx)
    if err != nil {
        return nil, err
    }
    defer rollback(ctx)

    product, err := s.products.Create(txCtx, req)
    if err != nil { return nil, err }

    if err := s.audit.Create(txCtx, &models.AuditLog{ /* ... */ }); err != nil {
        return nil, err
    }

    if err := commit(); err != nil { return nil, err }
    return product, nil
}
```

Because both `products.Create` and `audit.Create` read the transaction out of `ctx`, they automatically participate. No transaction argument threading.

## Error Translation

The generated code exposes predicate helpers (`generated.IsNotFound`, `generated.IsAlreadyExists`, etc.). Translate them to repository-level sentinels, then let the service/API layers map those to domain errors.

```go
// internal/repository/errors.go
package repository

import (
    "errors"

    "github.com/yourorg/myapp/internal/repository/generated"
)

var (
    ErrNotFound      = errors.New("not found")
    ErrAlreadyExists = errors.New("already exists")
)

func translateError(err error) error {
    if generated.IsNotFound(err)      { return ErrNotFound }
    if generated.IsAlreadyExists(err) { return ErrAlreadyExists }
    return err
}
```

The service layer then maps `repository.ErrNotFound` → `errors.ErrProductNotFound` (a domain sentinel from `internal/errors`) so the API layer's `handleServiceError` can translate it to the correct HTTP response without importing anything DB-specific. See [API.md](API.md#error-mapping).

skimatik's full predicate set:

| Helper | Meaning | Typical mapping |
|--------|---------|-----------------|
| `IsNotFound`           | No row matched     | `ErrNotFound` |
| `IsAlreadyExists`      | Unique-constraint violation | `ErrAlreadyExists` / `ErrDuplicate*` |
| `IsInvalidReference`   | Foreign-key violation | domain-specific conflict |
| `IsValidationError`    | CHECK constraint / NOT NULL violation | `ValidationError` with field detail |
| `IsConnectionError`    | Connection dropped / refused | `ErrServiceUnavailable` |
| `IsTimeout`            | Statement timeout | `ErrRequestTimeout` |
| `IsDatabaseError`      | Catch-all database error | `ErrDatabaseFailed` |

## Migrations — golang-migrate

Files live in `internal/database/migrations/` with the standard naming convention:

```
000001_create_products.up.sql
000001_create_products.down.sql
000002_add_products_active_index.up.sql
000002_add_products_active_index.down.sql
```

Run via your app's `migrate` subcommand:

```bash
myapp migrate up       # apply all pending
myapp migrate down     # rollback one
myapp migrate version  # show current version + dirty flag
```

Full `migrate up` command:

```go
func runMigrateUp(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    cfg, err := config.LoadDatabaseOnly()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    m, err := migrate.New("file://./migrations", cfg.DatabaseURL)
    if err != nil {
        return fmt.Errorf("failed to create migrator: %w", err)
    }
    defer m.Close()

    if err := m.Up(); err != nil {
        if errors.Is(err, migrate.ErrNoChange) {
            log := canonlog.New()
            log.InfoAdd("component", "migrate").InfoAdd("direction", "up")
            log.Flush(ctx)
            fmt.Println("Database is up to date")
            return nil
        }
        return fmt.Errorf("migration up failed: %w", err)
    }

    version, dirty, _ := m.Version()
    log := canonlog.New()
    log.InfoAdd("component", "migrate").InfoAdd("direction", "up").
        InfoAdd("version", version).InfoAdd("dirty", dirty)
    log.Flush(ctx)
    fmt.Printf("Migrations applied successfully. Current version: %d\n", version)
    return nil
}
```

Note the dual output: structured `canonlog` event for observability + plain `fmt.Printf` for the human running the command. Both are correct — see the [CONFIG.md logging note](CONFIG.md#logging-during-cli-commands).

## Schema.sql vs Migrations

- **`internal/database/schema.sql`** — current schema as one file. Used by skimatik introspection *and* for dev reset (drop + recreate + reload in one shot).
- **`internal/database/migrations/*.sql`** — incremental changes. What runs in production.

The two must agree. When you add a migration, also update `schema.sql` to the post-migration state so skimatik generates against the right schema.

## Dev Reset

```bash
make db-down && make db-up

docker exec -i myapp_db psql -U myapp -d postgres -c "DROP DATABASE IF EXISTS myapp;"
docker exec -i myapp_db psql -U myapp -d postgres -c "CREATE DATABASE myapp;"
docker exec -i myapp_db psql -U myapp -d myapp < internal/database/schema.sql

make generate
```

**Never** use this path in production — always use `migrate up`.
