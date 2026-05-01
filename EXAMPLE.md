# Products — Canonical Slice

The single reference implementation of a CRUD resource. Every other doc that shows a `Product*` type, a `*ProductService` signature, or a product handler is illustrative — this file is authoritative. When the two disagree, fix the other doc.

What lives here:

- **Schema** — `internal/database/schema.sql`
- **Migration** — `internal/database/migrations/000001_create_products.{up,down}.sql`
- **Queries** — `internal/repository/queries/products.sql`
- **Models** — `internal/models/product.go` (entity + I/O types + prefix constant)
- **Errors** — `internal/errors/errors.go` (domain sentinels + `ValidationError`)
- **Repository** — `internal/repository/product_repository.go`
- **Service interface** (consumer-owned) — `internal/service/repository_interface.go`
- **Service** — `internal/service/product_service.go`
- **API service interface** (consumer-owned) — `internal/api/service_interface.go`
- **Handlers** — `internal/api/products.go`
- **Routes** — `internal/api/routes.go` (products subsection)
- **Error mapping** — `internal/api/errors.go`

For surrounding context — middleware stack ([API.md](API.md)), error chain explanation ([ERRORS.md](ERRORS.md)), testing patterns ([TESTING.md](TESTING.md)), transactional services ([DATABASE.md](DATABASE.md#transactions--context-carried)), custom validators ([API.md](API.md#custom-validators)) — read the topic doc that owns it.

## Schema

`internal/database/schema.sql` — current schema as one file (used for skimatik introspection and dev reset). Includes the `accounts` table because Products references it via foreign key.

```sql {file=internal/database/schema.sql}
CREATE TABLE accounts (
    id            UUID PRIMARY KEY,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

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

`UUID PRIMARY KEY` with no `DEFAULT` — IDs are generated app-side by skimatik (UUIDv7). The unique index on `(account_id, name)` filters on `deleted_at IS NULL` so soft-deleted rows don't block re-creation.

## Migration

`internal/database/migrations/000001_create_accounts.up.sql`:

```sql {file=internal/database/migrations/000001_create_accounts.up.sql}
CREATE TABLE accounts (
    id            UUID PRIMARY KEY,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
```

`internal/database/migrations/000001_create_accounts.down.sql`:

```sql {file=internal/database/migrations/000001_create_accounts.down.sql}
DROP TABLE IF EXISTS accounts;
```

`internal/database/migrations/000002_create_products.up.sql`:

```sql {file=internal/database/migrations/000002_create_products.up.sql}
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

`internal/database/migrations/000002_create_products.down.sql`:

```sql {file=internal/database/migrations/000002_create_products.down.sql}
DROP TABLE IF EXISTS products;
```

The up migrations match `schema.sql` cumulatively. Adding a future change means writing `000003_*.up.sql` / `.down.sql` and updating `schema.sql` to the post-migration state.

## Queries

`internal/repository/queries/products.sql` — custom SQL consumed by skimatik. Each `-- name: …` annotation generates a method on the `*ProductsQueries` struct.

```sql {file=internal/repository/queries/products.sql}
-- name: GetProductByAccountAndID :one
SELECT id, account_id, name, description, active, metadata, created_at, updated_at
FROM products
WHERE account_id = $1
  AND id = $2
  AND deleted_at IS NULL;

-- name: ListProductsByAccount :paginated
-- param: $1 account_id uuid.UUID
-- param: $2 active *bool
SELECT id, account_id, name, description, active, created_at, updated_at
FROM products
WHERE account_id = $1
  AND deleted_at IS NULL
  AND ($2::boolean IS NULL OR active = $2)
ORDER BY id ASC;

-- name: UpdateProductByAccountAndID :one
UPDATE products
SET name        = $3,
    description = $4,
    active      = $5,
    updated_at  = NOW()
WHERE account_id = $1
  AND id          = $2
  AND deleted_at IS NULL
RETURNING id, account_id, name, description, active, metadata, created_at, updated_at;

-- name: SoftDeleteProduct :exec
UPDATE products
SET deleted_at = NOW(),
    updated_at = NOW()
WHERE account_id = $1
  AND id          = $2
  AND deleted_at IS NULL;
```

`Create` is generated automatically from the table — no custom SQL needed for it. Read paths always filter `deleted_at IS NULL` because skimatik's auto-generated `List` / `Paginate` ignore soft deletes.

## Models

`internal/models/product.go` — domain types. Internal IDs are `uuid.UUID`; nothing in this package knows about shortuuid.

```go {file=internal/models/product.go}
// Package models defines the domain entities and request/response input types
// shared across the repository, service, and api layers. It imports nothing
// from internal/* — every other internal package may import models.
package models

import (
    "time"

    "github.com/google/uuid"
)

const PrefixProduct = "prod_"

// PrefixAccount is shown here for cross-reference; in a real project it lives
// in models/account.go alongside the Account entity.
const PrefixAccount = "acc_"

type Product struct {
    ID          uuid.UUID
    AccountID   uuid.UUID
    Name        string
    Description *string
    Active      bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type CreateProductRequest struct {
    AccountID   uuid.UUID
    Name        string
    Description *string
    Active      bool
}

type GetProductParams struct {
    AccountID uuid.UUID
    ProductID uuid.UUID
}

type UpdateProductRequest struct {
    AccountID   uuid.UUID
    ProductID   uuid.UUID
    Name        *string
    Description *string
    Active      *bool
}

// ProductUpdate is the full target state of a product after a partial-update
// request has been merged with the current persisted state. The repository
// writes these values directly. Building this from UpdateProductRequest is the
// service layer's job (read current → apply non-nil fields → write).
type ProductUpdate struct {
    AccountID   uuid.UUID
    ProductID   uuid.UUID
    Name        string
    Description *string
    Active      bool
}

type DeleteProductParams struct {
    AccountID uuid.UUID
    ProductID uuid.UUID
}

type ListProductsFilter struct {
    AccountID    uuid.UUID
    Active       *bool
    Limit        int
    NextCursor   string
    BeforeCursor string
}

type ListProductsResult struct {
    Products     []Product
    HasMore      bool
    HasPrevious  bool
    NextCursor   string
    BeforeCursor string
}
```

`*string` / `*bool` on `UpdateProductRequest` mark fields as optional for partial updates. The service layer reads the current product, merges non-nil fields, and writes the full state via `models.ProductUpdate` — keeping the SQL plain (`SET col = $N`) so skimatik's parameter inference works (it can't see parameters wrapped in `COALESCE` / `CASE` inside the SET clause). Cursors are **opaque base64-encoded JSON tokens emitted by skimatik** — the handler passes them through unchanged. They are not IDs and are not shortuuid-encoded; field names match `generated.PaginationParams` / `PaginationResult` to avoid translation churn at the repo boundary.

## Errors

`internal/errors/errors.go` — domain sentinels and structured `ValidationError`. Compared with `errors.Is` / `errors.As` upstream.

```go {file=internal/errors/errors.go}
// Package errors holds the domain sentinel errors and the structured
// ValidationError used across the codebase. Imported as `apperrors` to avoid
// colliding with the stdlib `errors` package.
package errors

import "errors"

var (
    ErrProductNotFound    = errors.New("product not found")
    ErrDuplicateName      = errors.New("product with that name already exists")
    ErrForbidden          = errors.New("operation forbidden")
    ErrInvalidInput       = errors.New("invalid input")
    ErrDatabaseFailed     = errors.New("database operation failed")
    ErrEncryptionFailed   = errors.New("encryption failed")
    ErrDependencyFailed   = errors.New("upstream dependency failed")
    ErrServiceUnavailable = errors.New("service temporarily unavailable")
)

type FieldError struct {
    Field   string
    Code    string
    Message string
}

type ValidationError struct {
    Fields []FieldError
}

func (e *ValidationError) Error() string {
    if len(e.Fields) == 0 {
        return "validation failed"
    }
    return e.Fields[0].Message
}

func NewValidationError(fields ...FieldError) *ValidationError {
    return &ValidationError{Fields: fields}
}
```

The package is imported across the codebase as `apperrors` (the natural name `errors` collides with the stdlib package).

## Repository

`internal/repository/product_repository.go` — embeds skimatik's generated CRUD and custom-query structs, exposes domain-shaped methods that return `models.X` values. Every call passes `executorFromContext(ctx, r.db)` so the same method works inside or outside a transaction.

```go {file=internal/repository/product_repository.go}
// Package repository wraps skimatik-generated CRUD and custom-query structs,
// exposing domain-shaped methods that return models.X values. It owns the
// repository-layer error sentinels and transaction plumbing.
package repository

import (
    "context"

    "github.com/nhalm/pgxkit/v2"

    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository/generated"
)

type ProductRepository struct {
    db *pgxkit.DB
    *generated.ProductsRepository
    *generated.ProductsQueries
}

func NewProductRepository(db *pgxkit.DB) *ProductRepository {
    return &ProductRepository{
        db:                 db,
        ProductsRepository: generated.NewProductsRepository(nil), // nil = default UUIDv7 generator
        ProductsQueries:    generated.NewProductsQueries(),
    }
}

func (r *ProductRepository) Create(ctx context.Context, req models.CreateProductRequest) (models.Product, error) {
    row, err := r.ProductsRepository.Create(ctx, executorFromContext(ctx, r.db), generated.CreateProductsParams{
        AccountId:   req.AccountID,
        Name:        req.Name,
        Description: req.Description,
    })
    if err != nil {
        return models.Product{}, translateError(err)
    }
    return toProductModel(row), nil
}

func (r *ProductRepository) GetByID(ctx context.Context, params models.GetProductParams) (models.Product, error) {
    row, err := r.GetProductByAccountAndID(ctx, executorFromContext(ctx, r.db), params.AccountID, params.ProductID)
    if err != nil {
        return models.Product{}, translateError(err)
    }
    return models.Product{
        ID:          row.Id,
        AccountID:   row.AccountId,
        Name:        row.Name,
        Description: row.Description,
        Active:      row.Active,
        CreatedAt:   row.CreatedAt,
        UpdatedAt:   row.UpdatedAt,
    }, nil
}

func (r *ProductRepository) Update(ctx context.Context, upd models.ProductUpdate) (models.Product, error) {
    row, err := r.UpdateProductByAccountAndID(ctx, executorFromContext(ctx, r.db), upd.AccountID, upd.ProductID, upd.Name, upd.Description, upd.Active)
    if err != nil {
        return models.Product{}, translateError(err)
    }
    return models.Product{
        ID:          row.Id,
        AccountID:   row.AccountId,
        Name:        row.Name,
        Description: row.Description,
        Active:      row.Active,
        CreatedAt:   row.CreatedAt,
        UpdatedAt:   row.UpdatedAt,
    }, nil
}

func (r *ProductRepository) Delete(ctx context.Context, params models.DeleteProductParams) error {
    if err := r.SoftDeleteProduct(ctx, executorFromContext(ctx, r.db), params.AccountID, params.ProductID); err != nil {
        return translateError(err)
    }
    return nil
}

func (r *ProductRepository) ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (models.ListProductsResult, error) {
    page, err := r.ListProductsByAccountPaginated(
        ctx,
        executorFromContext(ctx, r.db),
        filter.AccountID,
        filter.Active,
        generated.PaginationParams{
            Limit:        filter.Limit,
            NextCursor:   filter.NextCursor,
            BeforeCursor: filter.BeforeCursor,
        },
    )
    if err != nil {
        return models.ListProductsResult{}, translateError(err)
    }
    products := make([]models.Product, len(page.Items))
    for i := range page.Items {
        item := page.Items[i]
        products[i] = models.Product{
            ID:          item.Id,
            AccountID:   item.AccountId,
            Name:        item.Name,
            Description: item.Description,
            Active:      item.Active,
            CreatedAt:   item.CreatedAt,
            UpdatedAt:   item.UpdatedAt,
        }
    }
    return models.ListProductsResult{
        Products:     products,
        HasMore:      page.HasMore,
        HasPrevious:  page.HasPrevious,
        NextCursor:   page.NextCursor,
        BeforeCursor: page.BeforeCursor,
    }, nil
}

func toProductModel(p *generated.Products) models.Product {
    return models.Product{
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

`translateError`, `executorFromContext`, `ContextWithTx`, `TxManager`, and the `ErrNotFound`/`ErrAlreadyExists` repository sentinels live in `internal/repository/{errors,tx}.go` — see [DATABASE.md](DATABASE.md#error-translation) and [DATABASE.md](DATABASE.md#transactions--context-carried) for the full implementations.

## Service Interface (consumer-owned by `service`)

`internal/service/repository_interface.go` — what `ProductService` consumes from the repository layer. The `mockgen` directive lives at the top of this file; running `go generate ./...` produces `repository_interface_mock.go` in the same package.

```go {file=internal/service/repository_interface.go}
//go:generate mockgen -source=repository_interface.go -destination=repository_interface_mock.go -package=service

package service

import (
    "context"

    "github.com/yourorg/myapp/internal/models"
)

type ProductRepository interface {
    Create(ctx context.Context, req models.CreateProductRequest) (models.Product, error)
    GetByID(ctx context.Context, params models.GetProductParams) (models.Product, error)
    Update(ctx context.Context, upd models.ProductUpdate) (models.Product, error)
    Delete(ctx context.Context, params models.DeleteProductParams) error
    ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (models.ListProductsResult, error)
}
```

## Service

`internal/service/product_service.go` — business logic. Translates repository sentinels to domain sentinels; clamps the pagination limit. The constructor takes only what it uses.

```go {file=internal/service/product_service.go}
// Package service holds business logic. It depends on consumer-owned
// repository interfaces (see repository_interface.go), translates repository
// sentinels into domain sentinels, and is the layer that orchestrates
// cross-resource operations.
package service

import (
    "context"
    "errors"

    apperrors "github.com/yourorg/myapp/internal/errors"
    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository"
)

type ProductService struct {
    repo ProductRepository
}

func NewProductService(repo ProductRepository) *ProductService {
    return &ProductService{repo: repo}
}

func (s *ProductService) CreateProduct(ctx context.Context, req models.CreateProductRequest) (models.Product, error) {
    product, err := s.repo.Create(ctx, req)
    switch {
    case errors.Is(err, repository.ErrAlreadyExists):
        return models.Product{}, apperrors.ErrDuplicateName
    case err != nil:
        return models.Product{}, err
    }
    return product, nil
}

func (s *ProductService) GetProduct(ctx context.Context, params models.GetProductParams) (models.Product, error) {
    product, err := s.repo.GetByID(ctx, params)
    switch {
    case errors.Is(err, repository.ErrNotFound):
        return models.Product{}, apperrors.ErrProductNotFound
    case err != nil:
        return models.Product{}, err
    }
    return product, nil
}

func (s *ProductService) UpdateProduct(ctx context.Context, req models.UpdateProductRequest) (models.Product, error) {
    current, err := s.repo.GetByID(ctx, models.GetProductParams{
        AccountID: req.AccountID,
        ProductID: req.ProductID,
    })
    if errors.Is(err, repository.ErrNotFound) {
        return models.Product{}, apperrors.ErrProductNotFound
    }
    if err != nil {
        return models.Product{}, err
    }

    upd := models.ProductUpdate{
        AccountID:   req.AccountID,
        ProductID:   req.ProductID,
        Name:        current.Name,
        Description: current.Description,
        Active:      current.Active,
    }
    if req.Name != nil {
        upd.Name = *req.Name
    }
    if req.Description != nil {
        upd.Description = req.Description
    }
    if req.Active != nil {
        upd.Active = *req.Active
    }

    product, err := s.repo.Update(ctx, upd)
    switch {
    case errors.Is(err, repository.ErrNotFound):
        return models.Product{}, apperrors.ErrProductNotFound
    case errors.Is(err, repository.ErrAlreadyExists):
        return models.Product{}, apperrors.ErrDuplicateName
    case err != nil:
        return models.Product{}, err
    }
    return product, nil
}

func (s *ProductService) DeleteProduct(ctx context.Context, params models.DeleteProductParams) error {
    err := s.repo.Delete(ctx, params)
    if errors.Is(err, repository.ErrNotFound) {
        return apperrors.ErrProductNotFound
    }
    return err
}

func (s *ProductService) ListProducts(ctx context.Context, filter models.ListProductsFilter) (models.ListProductsResult, error) {
    if filter.Limit <= 0 {
        filter.Limit = 20
    }
    if filter.Limit > 100 {
        filter.Limit = 100
    }
    return s.repo.ListWithFilters(ctx, filter)
}
```

**Constructor rule:** `NewProductService(repo)` — minimal. Add fields and constructor parameters only when the service actually uses them. Common additions:

- `tx *repository.TxManager` for methods that span multiple repos in one transaction — see [DATABASE.md](DATABASE.md#transactions--context-carried).
- `cfg config.Config` (or a typed subset) for business logic that depends on config values — feature flags, rate-limit budgets, encryption-key references.
- Other repos / other services for cross-resource orchestration.

## API Service Interface (consumer-owned by `api`)

`internal/api/service_interface.go` — what handlers consume from the service layer. Mirrors the public `*ProductService` method set. Generates `service_interface_mock.go`.

```go {file=internal/api/service_interface.go}
//go:generate mockgen -source=service_interface.go -destination=service_interface_mock.go -package=api

package api

import (
    "context"

    "github.com/yourorg/myapp/internal/models"
)

type ProductServiceInterface interface {
    CreateProduct(ctx context.Context, req models.CreateProductRequest) (models.Product, error)
    GetProduct(ctx context.Context, params models.GetProductParams) (models.Product, error)
    UpdateProduct(ctx context.Context, req models.UpdateProductRequest) (models.Product, error)
    DeleteProduct(ctx context.Context, params models.DeleteProductParams) error
    ListProducts(ctx context.Context, filter models.ListProductsFilter) (models.ListProductsResult, error)
}
```

## Handlers

`internal/api/products.go` — request types (with validation tags), response types, converter functions, and the five HTTP handlers. IDs cross the wire as prefixed shortuuid strings; this file is the only place encoding/decoding happens.

```go {file=internal/api/products.go}
package api

import (
    "fmt"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/nhalm/chikit"
    "github.com/nhalm/shortuuid"

    "github.com/yourorg/myapp/internal/models"
)

// ─── Request types ───────────────────────────────────────────────────────────

type CreateProductRequest struct {
    Name        string  `json:"name"        validate:"required,max=255"`
    Description *string `json:"description" validate:"omitempty,max=1000"`
    Active      bool    `json:"active"`
}

func (r CreateProductRequest) ToServiceModel(accountID uuid.UUID) models.CreateProductRequest {
    return models.CreateProductRequest{
        AccountID:   accountID,
        Name:        r.Name,
        Description: r.Description,
        Active:      r.Active,
    }
}

type UpdateProductRequest struct {
    Name        *string `json:"name,omitempty"        validate:"omitempty,max=255"`
    Description *string `json:"description,omitempty" validate:"omitempty,max=1000"`
    Active      *bool   `json:"active,omitempty"`
}

func (r UpdateProductRequest) ToServiceModel(accountID, productID uuid.UUID) models.UpdateProductRequest {
    return models.UpdateProductRequest{
        AccountID:   accountID,
        ProductID:   productID,
        Name:        r.Name,
        Description: r.Description,
        Active:      r.Active,
    }
}

// ─── Response types ──────────────────────────────────────────────────────────

type ProductResponse struct {
    ID          string  `json:"id"          example:"prod_2s8gNnj9C5Ubkx4T7W5vZk"`
    AccountID   string  `json:"account_id"  example:"acc_2s8gNnj9C5Ubkx4T7W5vZk"`
    Name        string  `json:"name"`
    Description *string `json:"description,omitempty"`
    Active      bool    `json:"active"`
    CreatedAt   string  `json:"created_at"`
    UpdatedAt   string  `json:"updated_at"`
}

func ProductResponseFromModel(p models.Product) ProductResponse {
    id, _ := shortuuid.ShortenUUID(p.ID)
    accountID, _ := shortuuid.ShortenUUID(p.AccountID)
    return ProductResponse{
        ID:          models.PrefixProduct + id,
        AccountID:   models.PrefixAccount + accountID,
        Name:        p.Name,
        Description: p.Description,
        Active:      p.Active,
        CreatedAt:   p.CreatedAt.Format(time.RFC3339),
        UpdatedAt:   p.UpdatedAt.Format(time.RFC3339),
    }
}

type ListResponse[T any] struct {
    Data         []T    `json:"data"`
    HasMore      bool   `json:"has_more"`
    NextCursor   string `json:"next_cursor,omitempty"`
    BeforeCursor string `json:"before_cursor,omitempty"`
}

// ─── ID decoding helpers ─────────────────────────────────────────────────────

// accountIDFromContext decodes the X-Account-ID header (extracted by chikit
// middleware as a shortuuid string) into a uuid.UUID. On error it writes a 400
// response and returns ok=false; callers should return immediately.
func accountIDFromContext(r *http.Request) (uuid.UUID, bool) {
    val, ok := chikit.HeaderFromContext(r.Context(), "account_id")
    if !ok {
        chikit.SetError(r, chikit.ErrBadRequest.WithParam("Missing account id", "X-Account-ID"))
        return uuid.Nil, false
    }
    id, err := shortuuid.ExpandUUID(strings.TrimPrefix(val.(string), models.PrefixAccount))
    if err != nil {
        chikit.SetError(r, chikit.ErrBadRequest.WithParam("Invalid account id", "X-Account-ID"))
        return uuid.Nil, false
    }
    return id, true
}

func productIDFromPath(r *http.Request) (uuid.UUID, bool) {
    raw := chi.URLParam(r, "id")
    id, err := shortuuid.ExpandUUID(strings.TrimPrefix(raw, models.PrefixProduct))
    if err != nil {
        chikit.SetError(r, chikit.ErrBadRequest.WithParam("Invalid product id", "id"))
        return uuid.Nil, false
    }
    return id, true
}

// Cursors are opaque base64-encoded JSON tokens emitted by skimatik. They
// pass through the handler unchanged in both directions — no encoding, no
// decoding. Clients echo whatever they received in `next_cursor` /
// `before_cursor` back as a query parameter on the next request.

// ─── Handlers ────────────────────────────────────────────────────────────────

func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
    accountID, ok := accountIDFromContext(r)
    if !ok {
        return
    }

    var req CreateProductRequest
    if !chikit.JSON(r, &req) {
        return
    }

    product, err := h.productService.CreateProduct(r.Context(), req.ToServiceModel(accountID))
    if err != nil {
        handleServiceError(r, err)
        return
    }

    chikit.SetResponse(r, http.StatusCreated, ProductResponseFromModel(product))
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
    accountID, ok := accountIDFromContext(r)
    if !ok {
        return
    }
    productID, ok := productIDFromPath(r)
    if !ok {
        return
    }

    product, err := h.productService.GetProduct(r.Context(), models.GetProductParams{
        AccountID: accountID,
        ProductID: productID,
    })
    if err != nil {
        handleServiceError(r, err)
        return
    }

    chikit.SetResponse(r, http.StatusOK, ProductResponseFromModel(product))
}

func (h *Handler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
    accountID, ok := accountIDFromContext(r)
    if !ok {
        return
    }
    productID, ok := productIDFromPath(r)
    if !ok {
        return
    }

    var req UpdateProductRequest
    if !chikit.JSON(r, &req) {
        return
    }

    product, err := h.productService.UpdateProduct(r.Context(), req.ToServiceModel(accountID, productID))
    if err != nil {
        handleServiceError(r, err)
        return
    }

    chikit.SetResponse(r, http.StatusOK, ProductResponseFromModel(product))
}

func (h *Handler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
    accountID, ok := accountIDFromContext(r)
    if !ok {
        return
    }
    productID, ok := productIDFromPath(r)
    if !ok {
        return
    }

    if err := h.productService.DeleteProduct(r.Context(), models.DeleteProductParams{
        AccountID: accountID,
        ProductID: productID,
    }); err != nil {
        handleServiceError(r, err)
        return
    }

    chikit.SetResponse(r, http.StatusNoContent, nil)
}

func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
    accountID, ok := accountIDFromContext(r)
    if !ok {
        return
    }

    filter, err := parseListProductsFilter(r, accountID)
    if err != nil {
        chikit.SetError(r, chikit.ErrBadRequest.With(err.Error()))
        return
    }

    result, err := h.productService.ListProducts(r.Context(), filter)
    if err != nil {
        handleServiceError(r, err)
        return
    }

    responses := make([]ProductResponse, len(result.Products))
    for i, p := range result.Products {
        responses[i] = ProductResponseFromModel(p)
    }

    chikit.SetResponse(r, http.StatusOK, ListResponse[ProductResponse]{
        Data:         responses,
        HasMore:      result.HasMore,
        NextCursor:   result.NextCursor,
        BeforeCursor: result.BeforeCursor,
    })
}

func parseListProductsFilter(r *http.Request, accountID uuid.UUID) (models.ListProductsFilter, error) {
    q := r.URL.Query()
    filter := models.ListProductsFilter{AccountID: accountID, Limit: 20}

    if v := q.Get("limit"); v != "" {
        n, err := strconv.Atoi(v)
        if err != nil || n < 1 || n > 100 {
            return filter, fmt.Errorf("limit must be 1-100")
        }
        filter.Limit = n
    }
    if v := q.Get("active"); v != "" {
        b, err := strconv.ParseBool(v)
        if err != nil {
            return filter, fmt.Errorf("active must be true or false")
        }
        filter.Active = &b
    }
    filter.NextCursor = q.Get("next_cursor")
    filter.BeforeCursor = q.Get("before_cursor")

    return filter, nil
}
```

The `Handler` struct, `NewHandler`, and `Pinger` interface (used by `/ready`) live in `internal/api/handler.go` — see [API.md](API.md#handler-shape).

## Routes

`internal/api/routes.go` — products subsection. Mounts under the `/v1` group, which already requires `X-Account-ID`, enforces body size, and binds JSON.

```go
r.Route("/v1", func(r chi.Router) {
    r.Use(chikit.ExtractHeader("X-Account-ID", "account_id", chikit.ExtractRequired()))
    r.Use(chikit.MaxBodySize(int64(h.config.MaxRequestBodyBytes)))
    r.Use(chikit.Binder())

    r.Post  ("/products",      h.CreateProduct)
    r.Get   ("/products/{id}", h.GetProduct)
    r.Patch ("/products/{id}", h.UpdateProduct)
    r.Delete("/products/{id}", h.DeleteProduct)
    r.Get   ("/products",      h.ListProducts)
})
```

The full middleware stack — `chikit.Handler` with canonlog, real-IP, header extraction, global rate limiter, public `/health` and `/ready` — lives in [API.md](API.md#middleware-stack).

## Error Mapping

`internal/api/errors.go` — single switch translating domain errors to HTTP responses. Every handler calls `handleServiceError(r, err)`; no other file in `api/` does the translation.

```go {file=internal/api/errors.go}
package api

import (
    "errors"
    "net/http"

    "github.com/nhalm/canonlog"
    "github.com/nhalm/chikit"

    apperrors "github.com/yourorg/myapp/internal/errors"
)

func handleServiceError(r *http.Request, err error) {
    // Structured validation errors carry per-field detail.
    var validationErr *apperrors.ValidationError
    if errors.As(err, &validationErr) {
        fields := make([]chikit.FieldError, len(validationErr.Fields))
        for i, f := range validationErr.Fields {
            fields[i] = chikit.FieldError{Param: f.Field, Code: f.Code, Message: f.Message}
        }
        chikit.SetError(r, chikit.NewValidationError(fields))
        return
    }

    switch {
    // Client errors — message is safe to show the caller.
    case errors.Is(err, apperrors.ErrProductNotFound):
        chikit.SetError(r, chikit.ErrNotFound.With("Product not found"))
    case errors.Is(err, apperrors.ErrDuplicateName):
        chikit.SetError(r, chikit.ErrConflict.With("Product with that name already exists"))
    case errors.Is(err, apperrors.ErrForbidden):
        chikit.SetError(r, chikit.ErrForbidden.With("Operation not permitted"))
    case errors.Is(err, apperrors.ErrInvalidInput):
        chikit.SetError(r, chikit.ErrBadRequest.With("Invalid input"))

    // Server errors — log full detail, return a generic response.
    case errors.Is(err, apperrors.ErrDatabaseFailed),
        errors.Is(err, apperrors.ErrEncryptionFailed),
        errors.Is(err, apperrors.ErrDependencyFailed):
        canonlog.ErrorAdd(r.Context(), err)
        chikit.SetError(r, chikit.ErrInternal)

    // Custom status codes.
    case errors.Is(err, apperrors.ErrServiceUnavailable):
        canonlog.ErrorAdd(r.Context(), err)
        chikit.SetError(r, &chikit.APIError{
            Type:    "internal_error",
            Code:    "service_unavailable",
            Message: "Service temporarily unavailable",
            Status:  http.StatusServiceUnavailable,
        })

    // Unknown — always log the detail, never leak it.
    default:
        canonlog.ErrorAdd(r.Context(), err)
        chikit.SetError(r, chikit.ErrInternal)
    }
}
```

The full error chain — DB predicate → repository sentinel → domain sentinel → HTTP — is documented in [ERRORS.md](ERRORS.md).

## Wiring

The slice plugs into `cmd/<app>/serve.go` like this:

```go
productRepo := repository.NewProductRepository(db)
productSvc  := service.NewProductService(productRepo)

if err := api.RegisterValidators(); err != nil {
    return err
}
handler := api.NewHandler(productSvc, db, nil, cfg) // pass a Pinger instead of nil when Redis is configured

rateLimitStore := store.NewMemory() // or store.NewRedis(...)
router := api.Routes(handler, rateLimitStore)
```

The full `runServe` (config loading, pgxkit setup, graceful shutdown) is in [ARCHITECTURE.md](ARCHITECTURE.md#explicit-dependency-injection).

## Tests

Test files for this slice — `repository/product_repository_integration_test.go`, `service/product_service_test.go`, `api/products_test.go` — follow the patterns in [TESTING.md](TESTING.md). The mock types referenced there (`MockProductRepository`, `MockProductServiceInterface`) are generated from the interface files above.
