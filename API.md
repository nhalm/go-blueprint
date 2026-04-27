# API Layer

Handlers, middleware, request/response conventions, and error mapping — built on **chikit v1.x** and **canonlog v0.3+**.

## Middleware Stack

Define the router in `internal/api/routes.go`. Order matters: `chikit.Handler` must run first so every downstream middleware (including auth/header extraction) accumulates into the canonical log for that request.

```go
package api

import (
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/nhalm/chikit"
    "github.com/nhalm/chikit/store"
)

func Routes(h *Handler, rateLimitStore store.Store) chi.Router {
    r := chi.NewRouter()

    // 1. Request timeout + canonlog context.
    //    WithCanonlogFields closure pulls already-extracted header values out of
    //    context and adds them to the canonical log just before Flush.
    r.Use(chikit.Handler(
        chikit.WithTimeout(h.config.HTTPRequestTimeout),
        chikit.WithCanonlog(),
        chikit.WithCanonlogFields(func(r *http.Request) map[string]any {
            fields := make(map[string]any)
            if v, ok := chikit.HeaderFromContext(r.Context(), "account_id"); ok {
                fields["account_id"] = v
            }
            if v, ok := chikit.HeaderFromContext(r.Context(), "request_id"); ok {
                fields["request_id"] = v
            }
            if v, ok := chikit.HeaderFromContext(r.Context(), "client_ip"); ok {
                fields["client_ip"] = v
            }
            return fields
        }),
    ))

    // 2. Real client IP from X-Forwarded-For.
    r.Use(middleware.RealIP)

    // 3. Lift useful request headers into context so handlers and the canonlog
    //    closure above can read them via chikit.HeaderFromContext.
    r.Use(chikit.ExtractHeader("X-Request-ID", "request_id"))
    r.Use(chikit.ExtractHeader("X-Forwarded-For", "client_ip"))
    r.Use(chikit.ExtractHeader("User-Agent", "user_agent"))

    // 4. Global rate limit (memory store for dev, Redis store for multi-instance prod).
    globalLimiter := chikit.NewRateLimiter(
        rateLimitStore,
        h.config.RateLimitRequests,
        h.config.RateLimitWindow,
        chikit.RateLimitWithIP(),
    )
    r.Use(globalLimiter.Handler)

    // 5. Public routes — no account header required.
    r.Get("/health", h.Health)
    r.Get("/ready", h.Ready)

    // 6. Authenticated routes — require X-Account-ID, enforce body size, bind JSON.
    r.Route("/v1", func(r chi.Router) {
        r.Use(chikit.ExtractHeader("X-Account-ID", "account_id", chikit.ExtractRequired()))
        r.Use(chikit.MaxBodySize(int64(h.config.MaxRequestBodyBytes)))
        r.Use(chikit.Binder())

        r.Post("/products", h.CreateProduct)
        r.Get("/products/{id}", h.GetProduct)
        r.Patch("/products/{id}", h.UpdateProduct)
        r.Delete("/products/{id}", h.DeleteProduct)
        r.Get("/products", h.ListProducts)
    })

    return r
}
```

**Stores.** Use `store.NewMemory()` for single-instance dev. Use `store.NewRedis(store.RedisConfig{URL, Password, DB, Prefix})` for multi-instance production so rate limit counts stay consistent across replicas.

**ExtractHeader options.** `chikit.ExtractRequired()` rejects the request with 400 if the header is missing. Without it, the extraction is best-effort (absent header = nothing in context).

## Handler Shape

The interface the handler consumes lives in its own file with the mockgen directive:

```go
// internal/api/service_interface.go
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

Domain types in `internal/models` use `uuid.UUID` for ID fields:

```go
// internal/models/product.go
package models

import (
    "time"

    "github.com/google/uuid"
)

type Product struct {
    ID          uuid.UUID
    AccountID   uuid.UUID
    Name        string
    Description *string
    Active      bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type GetProductParams struct {
    AccountID uuid.UUID
    ProductID uuid.UUID
}
```

The handler itself is a plain struct with a constructor:

```go
// internal/api/handler.go
package api

import (
    "github.com/nhalm/pgxkit/v2"
    "github.com/yourorg/myapp/internal/config"
)

type Handler struct {
    productService ProductServiceInterface
    db             *pgxkit.DB
    config         config.Config
}

func NewHandler(productSvc ProductServiceInterface, db *pgxkit.DB, cfg config.Config) *Handler {
    return &Handler{
        productService: productSvc,
        db:             db,
        config:         cfg,
    }
}
```

## Request Binding — `chikit.JSON`, `chikit.Query`

`chikit.Binder()` (applied as middleware) wires up body reading, decoding, and validation. Handlers then use short helpers that return `bool` — `false` means an error response was already written and the handler should return.

```go
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
    val, _ := chikit.HeaderFromContext(r.Context(), "account_id")
    accountID := val.(string)

    var req CreateProductRequest
    if !chikit.JSON(r, &req) {
        return // error response already written by chikit
    }

    product, err := h.productService.CreateProduct(r.Context(), req.ToServiceModel(accountID))
    if err != nil {
        handleServiceError(r, err)
        return
    }

    chikit.SetResponse(r, http.StatusCreated, ProductResponseFromModel(product))
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
    accountIDVal, _ := chikit.HeaderFromContext(r.Context(), "account_id")
    accountID, err := shortuuid.ExpandUUID(accountIDVal.(string))
    if err != nil {
        chikit.SetError(r, chikit.ErrBadRequest.WithParam("Invalid account id", "X-Account-ID"))
        return
    }

    productID, err := shortuuid.ExpandUUID(chi.URLParam(r, "id"))
    if err != nil {
        chikit.SetError(r, chikit.ErrBadRequest.WithParam("Invalid product id", "id"))
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
```

Key points:
- Handlers don't call `w.WriteHeader` or `json.NewEncoder(w).Encode(...)` — `chikit.SetResponse` does both.
- Handlers don't call `w.WriteHeader(http.StatusBadRequest)` on errors — `chikit.SetError(r, ...)` does.
- IDs enter the handler as short-form strings (path param, header, JSON field) and are decoded to `uuid.UUID` via `shortuuid.ExpandUUID` before being passed to the service.
- Error responses are never `200 + {error: ...}` — always non-2xx with a structured body (see *Error Responses* below).

## shortuuid on the Wire

IDs travel over the wire as prefixed 22-character base62 strings: `prod_2s8gNnj9C5Ubkx4T7W5vZk`. The prefix is the entity type; the suffix is the shortuuid encoding of the internal UUIDv7. Internally every layer below the handler uses `uuid.UUID`.

Prefix constants live in `internal/models` alongside the entity they identify:

```go
// internal/models/product.go
const PrefixProduct = "prod_"
```

```go
import (
    "strings"

    "github.com/nhalm/shortuuid"
    "github.com/yourorg/myapp/internal/models"
)

// Inbound — strip prefix, then expand
raw := chi.URLParam(r, "id") // "prod_2s8gNnj9C5Ubkx4T7W5vZk"
productID, err := shortuuid.ExpandUUID(strings.TrimPrefix(raw, models.PrefixProduct))
if err != nil {
    chikit.SetError(r, chikit.ErrBadRequest.WithParam("Invalid product id", "id"))
    return
}

// Outbound — shorten, then prepend prefix
short, _ := shortuuid.ShortenUUID(product.ID)
id := models.PrefixProduct + short // "prod_2s8gNnj9C5Ubkx4T7W5vZk"
```

A small helper on the response type keeps encoding in one place:

```go
type ProductResponse struct {
    ID        string `json:"id"         example:"prod_2s8gNnj9C5Ubkx4T7W5vZk"`
    AccountID string `json:"account_id" example:"acc_2s8gNnj9C5Ubkx4T7W5vZk"`
    // ...
}

func ProductResponseFromModel(p models.Product) ProductResponse {
    id, _       := shortuuid.ShortenUUID(p.ID)
    accountID, _ := shortuuid.ShortenUUID(p.AccountID)
    return ProductResponse{
        ID:        models.PrefixProduct + id,
        AccountID: models.PrefixAccount + accountID,
        // ...
    }
}
```

Response-type `json` fields carrying IDs are always `string` (the encoded form). Domain `models.X` types use `uuid.UUID` for ID fields. The boundary between the two is the response converter / handler.

## Request / Response Types

Request types have validation tags; response types don't.

```go
// internal/api/products.go

type CreateProductRequest struct {
    Name        string  `json:"name" validate:"required,max=255"`
    Description *string `json:"description" validate:"omitempty,max=1000"`
    Active      bool    `json:"active"`
}

func (r CreateProductRequest) ToServiceModel(accountID string) models.CreateProductRequest {
    return models.CreateProductRequest{
        AccountID:   accountID,
        Name:        r.Name,
        Description: r.Description,
        Active:      r.Active,
    }
}

type ProductResponse struct {
    ID          string  `json:"id"          example:"prod_2s8gNnj9C5Ubkx4T7W5vZk"`
    Name        string  `json:"name"`
    Description *string `json:"description,omitempty"`
    Active      bool    `json:"active"`
    CreatedAt   string  `json:"created_at"  example:"2024-01-15T10:00:00Z"`
    UpdatedAt   string  `json:"updated_at"`
}

func ProductResponseFromModel(p models.Product) ProductResponse {
    id, _ := shortuuid.ShortenUUID(p.ID)
    return ProductResponse{
        ID:          models.PrefixProduct + id,
        Name:        p.Name,
        Description: p.Description,
        Active:      p.Active,
        CreatedAt:   p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
        UpdatedAt:   p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
    }
}
```

## Response Conventions

### Single resource — no envelope
```json
{
  "id": "prod_2s8gNnj9C5Ubkx4T7W5vZk",
  "name": "Premium Plan",
  "active": true,
  "created_at": "2025-01-15T10:30:00Z"
}
```

### Collections — minimal envelope
```json
{
  "data": [
    { "id": "prod_2s8gNnj9C5Ubkx4T7W5vZk", "name": "Plan A" },
    { "id": "prod_4RfK9mBvL3XpN2wYq8aEcT", "name": "Plan B" }
  ],
  "has_more": true,
  "next_cursor": "prod_4RfK9mBvL3XpN2wYq8aEcT",
  "prev_cursor": "prod_2s8gNnj9C5Ubkx4T7W5vZk"
}
```

Cursors are shortuuid strings because paginated queries return UUIDs and the handler encodes them on the way out.

### Errors — chikit-shaped
`chikit.SetError` emits the standard shape:
```json
{
  "error": {
    "type":    "invalid_request_error",
    "code":    "validation_error",
    "message": "name is required",
    "param":   "name"
  }
}
```

Multi-field validation errors use a `fields` array instead of `param`:
```json
{
  "error": {
    "type":    "invalid_request_error",
    "code":    "validation_error",
    "message": "validation failed",
    "fields": [
      { "field": "name", "code": "required", "message": "name is required" },
      { "field": "description", "code": "max", "message": "description must be at most 1000 characters" }
    ]
  }
}
```

## Error Mapping

Services return domain errors from `internal/errors`. The API layer translates them in one place:

```go
// internal/api/errors.go
package api

import (
    "errors"
    "net/http"

    "github.com/nhalm/canonlog"
    "github.com/nhalm/chikit"
    apierrors "github.com/yourorg/myapp/internal/errors"
)

func handleServiceError(r *http.Request, err error) {
    // Structured validation errors carry per-field details.
    var validationErr *apierrors.ValidationError
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
    case errors.Is(err, apierrors.ErrProductNotFound):
        chikit.SetError(r, chikit.ErrNotFound.With("Product not found"))
    case errors.Is(err, apierrors.ErrDuplicateName):
        chikit.SetError(r, chikit.ErrConflict.With("Product with that name already exists"))
    case errors.Is(err, apierrors.ErrForbidden):
        chikit.SetError(r, chikit.ErrForbidden.With("Operation not permitted"))
    case errors.Is(err, apierrors.ErrInvalidInput):
        chikit.SetError(r, chikit.ErrBadRequest.With("Invalid input"))

    // Server errors — log full detail via canonlog, return a generic response.
    case errors.Is(err, apierrors.ErrDatabaseFailed),
        errors.Is(err, apierrors.ErrEncryptionFailed),
        errors.Is(err, apierrors.ErrDependencyFailed):
        canonlog.ErrorAdd(r.Context(), err)
        chikit.SetError(r, chikit.ErrInternal)

    // Custom status codes.
    case errors.Is(err, apierrors.ErrServiceUnavailable):
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

Rule of thumb: **client-facing message** → `chikit.SetError(r, chikit.ErrXxx.With(...))`. **Server-side diagnostic** → `canonlog.ErrorAdd(r.Context(), err)` AND a generic `chikit.ErrInternal` / custom `APIError` to the client. Never leak SQL/provider errors to the response body.

The sentinel error values (`ErrProductNotFound`, `ErrDatabaseFailed`, etc.) live in `internal/errors/errors.go`:

```go
// internal/errors/errors.go
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
```

Plus the structured validation error for multi-field responses:

```go
// internal/errors/validation.go
package errors

type FieldError struct {
    Field   string `json:"field"`
    Code    string `json:"code"`
    Message string `json:"message"`
}

type ValidationError struct {
    Fields []FieldError
}

func (e *ValidationError) Error() string { /* ... */ }
func NewValidationError(fields ...FieldError) *ValidationError { return &ValidationError{Fields: fields} }
```

## Custom Validators

`chikit.Binder()` uses `go-playground/validator`. Register custom tags at startup, once, after handler construction but before `Routes(...)`:

```go
// internal/api/validators.go
package api

import "github.com/nhalm/chikit"

func RegisterValidators() {
    chikit.RegisterValidation("format", validateFormat)
    chikit.RegisterValidation("storage", validateStorage)
}

func validateFormat(fl validator.FieldLevel) bool { /* ... */ }
```

```go
// cmd/<app>/serve.go
api.RegisterValidators()
handler := api.NewHandler(...)
router := api.Routes(handler, rateLimitStore)
```

## Pagination

Cursor-based — never offset. The repository layer returns a `PaginationResult[T]` from a skimatik `:paginated` query; the handler maps it to the collection envelope:

```go
func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
    val, _ := chikit.HeaderFromContext(r.Context(), "account_id")
    accountID := val.(string)

    filter, err := parseListFilter(r, accountID)
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
        Data:       responses,
        HasMore:    result.HasMore,
        NextCursor: ptrOrEmpty(result.NextCursor),
        PrevCursor: ptrOrEmpty(result.PrevCursor),
    })
}

type ListResponse[T any] struct {
    Data       []T    `json:"data"`
    HasMore    bool   `json:"has_more"`
    NextCursor string `json:"next_cursor,omitempty"`
    PrevCursor string `json:"prev_cursor,omitempty"`
}
```

## Swagger

Annotate handlers with standard swaggo tags. Generate with `make swagger` (`swag init -g cmd/<app>/main.go -o docs`).

```go
// CreateProduct godoc
// @Summary     Create a new product
// @Tags        Products
// @Accept      json
// @Produce     json
// @Param       X-Account-ID header  string                true "Account ID"
// @Param       request      body    CreateProductRequest  true "Product fields"
// @Success     201 {object} ProductResponse
// @Failure     400 {object} chikit.ValidationErrorResponse
// @Failure     401 {object} chikit.ErrorResponse "Missing X-Account-ID"
// @Failure     409 {object} chikit.ErrorResponse "Duplicate name"
// @Failure     500 {object} chikit.ErrorResponse
// @Router      /v1/products [post]
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) { /* ... */ }
```

