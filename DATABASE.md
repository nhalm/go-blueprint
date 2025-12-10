# Database Patterns

This document covers database schema design, Skimatik code generation, and migrations.

## Prerequisites

Install the required tools:

```bash
# Skimatik - generates type-safe repositories from PostgreSQL schemas
go install github.com/nhalm/skimatik/cmd/skimatik@latest

# pgxkit - PostgreSQL toolkit used by generated code
go get github.com/nhalm/pgxkit
```

## Schema Design Principles

1. **Provider-agnostic**: Use generic column names
2. **Soft deletes**: Use `deleted_at TIMESTAMPTZ` column
3. **Timestamps**: Always include `created_at` and `updated_at`
4. **Text IDs**: Use `TEXT PRIMARY KEY` with prefixed KSUIDs

```sql
CREATE TABLE products (
    id TEXT PRIMARY KEY,                    -- Prefixed KSUID: prod_2ArTLVP...
    name VARCHAR(255) NOT NULL,
    description TEXT,
    active BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                  -- Soft delete
);

CREATE INDEX idx_products_active ON products(active) WHERE deleted_at IS NULL;
```

## Skimatik Configuration

Skimatik generates type-safe repositories from your database schema.

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

### Generated Output

Skimatik generates:
- `generated/products_generated.go` - CRUD operations
- `generated/products_queries_generated.go` - Custom SQL queries
- `generated/id_generators.go` - ID generation interfaces
- `generated/pagination.go` - Cursor pagination utilities

## Custom SQL Queries

Define custom queries in SQL files that Skimatik will generate Go code for.

```sql
-- internal/repository/queries/products.sql

-- name: GetProductByID :one
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListProducts :many
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE deleted_at IS NULL
  AND ($1::boolean IS NULL OR active = $1)
  AND ($3::text IS NULL OR id > $3)
  AND ($4::text IS NULL OR id < $4)
ORDER BY id ASC
LIMIT $2;
```

### Query Naming Conventions

- `:one` - Returns a single row (generates `func() (*Row, error)`)
- `:many` - Returns multiple rows (generates `func() ([]Row, error)`)
- `:exec` - No return value (generates `func() error`)

## Migration Workflow

Use golang-migrate for schema migrations.

### Creating Migrations

```bash
# Create migration files
# Files: internal/database/migrations/YYYYMMDDHHMMSS_create_products.up.sql
#        internal/database/migrations/YYYYMMDDHHMMSS_create_products.down.sql
```

### Running Migrations

```bash
# Run all pending migrations
go run cmd/myapp/main.go migrate up

# Rollback one migration
go run cmd/myapp/main.go migrate down
```

### Example Migration Files

**Up migration** (`000001_create_products.up.sql`):
```sql
CREATE TABLE products (
    id TEXT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    active BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_products_active ON products(active) WHERE deleted_at IS NULL;
```

**Down migration** (`000001_create_products.down.sql`):
```sql
DROP INDEX IF EXISTS idx_products_active;
DROP TABLE IF EXISTS products;
```

## Repository Pattern

Custom repositories embed generated code and add domain-specific methods.

```go
// internal/repository/product_repository.go
package repository

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/nhalm/pgxkit"
    "github.com/yourorg/myapp/internal/id"
    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository/generated"
)

type ProductRepository struct {
    *generated.ProductsRepository
    queries *generated.ProductsQueries
    db      *pgxkit.DB
}

func NewProductRepository(db *pgxkit.DB) *ProductRepository {
    idGen := func() string {
        return id.GenerateIDWithPrefix("prod_")
    }

    return &ProductRepository{
        ProductsRepository: generated.NewProductsRepository(db, idGen),
        queries:            generated.NewProductsQueries(db),
        db:                 db,
    }
}

func (r *ProductRepository) Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
    metadataJSON, err := marshalToRawMessage(req.Metadata)
    if err != nil {
        return nil, err
    }

    createParams := generated.CreateProductsParams{
        Name:        req.Name,
        Description: req.Description,
        Metadata:    metadataJSON,
    }

    product, err := r.ProductsRepository.Create(ctx, createParams)
    if err != nil {
        return nil, err
    }

    return r.GetByID(ctx, models.GetProductParams{
        ProductID: product.Id,
    })
}

func (r *ProductRepository) GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
    result, err := r.queries.GetProductByID(ctx, params.ProductID)
    if err != nil {
        return nil, err
    }

    var metadata map[string]string
    if result.Metadata != nil && len(*result.Metadata) > 0 {
        if err := json.Unmarshal(*result.Metadata, &metadata); err != nil {
            return nil, fmt.Errorf("unmarshal metadata: %w", err)
        }
    }

    return &models.Product{
        ID:          result.Id,
        Name:        result.Name,
        Description: result.Description,
        Active:      result.Active,
        Metadata:    metadata,
        CreatedAt:   result.CreatedAt,
        UpdatedAt:   result.UpdatedAt,
    }, nil
}

func (r *ProductRepository) Update(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error) {
    metadataJSON, err := marshalToRawMessage(req.Metadata)
    if err != nil {
        return nil, err
    }

    updateParams := generated.UpdateProductsParams{
        Name:        req.Name,
        Description: req.Description,
        Active:      req.Active,
        Metadata:    metadataJSON,
    }

    if _, err := r.ProductsRepository.Update(ctx, req.ID, updateParams); err != nil {
        return nil, err
    }

    return r.GetByID(ctx, models.GetProductParams{ProductID: req.ID})
}

func (r *ProductRepository) ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error) {
    results, err := r.queries.ListProducts(ctx, filter.Active, filter.Limit+1, filter.StartingAfter, filter.EndingBefore)
    if err != nil {
        return nil, err
    }

    hasMore := len(results) > filter.Limit
    if hasMore {
        results = results[:filter.Limit]
    }

    products := make([]*models.Product, len(results))
    for i, result := range results {
        var metadata map[string]string
        if result.Metadata != nil && len(*result.Metadata) > 0 {
            if err := json.Unmarshal(*result.Metadata, &metadata); err != nil {
                return nil, fmt.Errorf("unmarshal metadata: %w", err)
            }
        }
        products[i] = &models.Product{
            ID:          result.Id,
            Name:        result.Name,
            Description: result.Description,
            Active:      result.Active,
            Metadata:    metadata,
            CreatedAt:   result.CreatedAt,
            UpdatedAt:   result.UpdatedAt,
        }
    }

    var nextCursor, prevCursor *string
    if len(products) > 0 {
        last := products[len(products)-1].ID
        first := products[0].ID
        if hasMore {
            nextCursor = &last
        }
        if filter.StartingAfter != nil {
            prevCursor = &first
        }
    }

    return &models.ListProductsResult{
        Products:   products,
        HasMore:    hasMore,
        NextCursor: nextCursor,
        PrevCursor: prevCursor,
    }, nil
}

func (r *ProductRepository) Delete(ctx context.Context, params models.DeleteProductParams) error {
    return r.ProductsRepository.Delete(ctx, params.ProductID)
}
```

## Helper Functions

```go
// internal/repository/helpers.go
package repository

import (
    "encoding/json"
    "fmt"
)

func marshalToRawMessage(v any) (*json.RawMessage, error) {
    if v == nil {
        return nil, nil
    }

    data, err := json.Marshal(v)
    if err != nil {
        return nil, fmt.Errorf("marshal metadata: %w", err)
    }

    raw := json.RawMessage(data)
    return &raw, nil
}
```

## ID Generation

```go
// internal/id/generator.go
package id

import "github.com/segmentio/ksuid"

// GenerateIDWithPrefix creates a new KSUID with the given prefix.
// KSUIDs are time-ordered, collision-resistant, and URL-safe.
//
// Format: <prefix><27-char-ksuid>
// Example: prod_2ArTLVPddDx8vZk7CqEbiYp1
func GenerateIDWithPrefix(prefix string) string {
    return prefix + ksuid.New().String()
}
```

## Development Workflow

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
