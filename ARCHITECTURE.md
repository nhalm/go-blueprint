# Architecture Layers

This document describes the clean architecture layers and patterns used in this blueprint.

## Layer Overview

```
internal/models          ← Foundation: domain entities, parameter structs (no dependencies)
    ↓
internal/repository      ← Data access: imports models, generated code
    ↓
internal/service         ← Business logic: imports models, defines repo interfaces
    ↓
internal/api             ← HTTP handlers: imports models, defines service interfaces
```

## Models Layer

Domain entities and parameter structs. **No imports from other internal packages.**

```go
// internal/models/product.go
package models

import "time"

type Product struct {
    ID          string
    Name        string
    Description *string
    Active      bool
    Metadata    map[string]string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// Request/response types for service layer
type CreateProductRequest struct {
    Name        string
    Description *string
    Active      bool
    Metadata    map[string]string
}

type UpdateProductRequest struct {
    ID          string
    Name        *string  // Pointer = optional field
    Description *string
    Active      *bool
    Metadata    map[string]string
    UpdatedAt   time.Time  // For optimistic locking
}
```

```go
// internal/models/params.go
package models

// Parameter structs for cross-layer communication
type GetProductParams struct {
    ProductID string
}

type DeleteProductParams struct {
    ProductID string
}

type ListProductsFilter struct {
    Active        *bool
    Limit         int
    StartingAfter *string
}
```

## Repository Layer

Custom repositories **embed** Skimatik-generated repositories.

```go
// internal/repository/product_repository.go
package repository

import (
    "context"
    "github.com/yourorg/myapp/internal/id"
    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository/generated"
    "github.com/nhalm/pgxkit"
)

type ProductRepository struct {
    *generated.ProductsRepository  // Embed generated CRUD
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

// Domain-specific method beyond CRUD
func (r *ProductRepository) GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
    result, err := r.queries.GetProductByID(ctx, params.ProductID)
    if err != nil {
        return nil, err
    }
    return &models.Product{
        ID:          result.Id,
        Name:        result.Name,
        Description: result.Description,
        Active:      result.Active,
        CreatedAt:   result.CreatedAt,
        UpdatedAt:   result.UpdatedAt,
    }, nil
}
```

## Service Layer

Services define their **own repository interfaces** (interface segregation).

```go
// internal/service/product_service.go
package service

import (
    "context"
    "errors"
    "github.com/yourorg/myapp/internal/apperrors"
    "github.com/yourorg/myapp/internal/models"
    "github.com/yourorg/myapp/internal/repository"
)

// Service defines ONLY the methods it needs
type ProductRepository interface {
    Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
    Update(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
    Delete(ctx context.Context, params models.DeleteProductParams) error
    ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}

type ProductService struct {
    repo ProductRepository
}

func NewProductService(repo ProductRepository) *ProductService {
    return &ProductService{repo: repo}
}

func (s *ProductService) CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
    // Business validation goes here (e.g., check for duplicates)
    // Structural validation (required fields) is handled by API layer
    return s.repo.Create(ctx, req)
}

func (s *ProductService) GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
    // Repository returns apperrors directly, so just propagate
    return s.repo.GetByID(ctx, params)
}
```

## API Layer

Handlers define their **own service interfaces** (same pattern as services defining repo interfaces). Separate API types from domain models.

```go
// internal/api/handler.go
package api

import (
    "context"
    "encoding/json"
    "net/http"
    "github.com/go-chi/chi/v5"
    "github.com/nhalm/canonlog"
    "github.com/yourorg/myapp/internal/models"
)

// Handler defines ONLY the service methods it needs
type ProductService interface {
    CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error)
    UpdateProduct(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
    DeleteProduct(ctx context.Context, params models.DeleteProductParams) error
    ListProducts(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}

type Handler struct {
    productSvc ProductService
}

func NewHandler(productSvc ProductService) *Handler {
    return &Handler{
        productSvc: productSvc,
    }
}

func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
    // Decode API request type
    var req CreateProductRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        BadRequest(w, r, err, "invalid request body", "")
        return
    }

    // Validate
    if err := ValidateStruct(req); err != nil {
        BadRequest(w, r, err, err.Error(), "")
        return
    }

    // Add request context for logging
    canonlog.AddRequestFields(r.Context(), map[string]any{
        "product_name": req.Name,
    })

    // Convert API type → domain model
    serviceReq := models.CreateProductRequest{
        Name:        req.Name,
        Description: req.Description,
        Active:      req.Active,
        Metadata:    req.Metadata,
    }

    // Call service
    product, err := h.productSvc.CreateProduct(r.Context(), &serviceReq)
    if err != nil {
        handleServiceError(w, r, err)
        return
    }

    // Convert domain model → API response
    Created(w, convertToProductResponse(product))
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    product, err := h.productSvc.GetProduct(r.Context(), models.GetProductParams{
        ProductID: id,
    })
    if err != nil {
        handleServiceError(w, r, err)
        return
    }

    Success(w, convertToProductResponse(product))
}
```

## Dependency Injection

Explicit wiring in `serve.go`. No frameworks.

```go
// cmd/myapp/cmd/serve.go
func runServe(_ *cobra.Command, _ []string) error {
    // Config
    canonlog.SetupGlobalLogger(viper.GetString("LOG_LEVEL"), viper.GetString("LOG_FORMAT"))

    // Database
    db := pgxkit.NewDB()
    if err := db.Connect(ctx, viper.GetString("DATABASE_URL")); err != nil {
        return err
    }
    defer db.Shutdown(ctx)

    // Layer 1: Repositories
    productRepo := repository.NewProductRepository(db)

    // Layer 2: Services
    productSvc := service.NewProductService(productRepo)

    // Layer 3: API
    handler := api.NewHandler(productSvc)

    // Server
    srv := &http.Server{
        Addr:    fmt.Sprintf(":%d", viper.GetInt("PORT")),
        Handler: handler.Routes(),
    }

    // Graceful shutdown...
}
```

## Error Types

Domain-specific error types for consistent error handling across layers.

```go
// internal/apperrors/errors.go
package apperrors

import "fmt"

type NotFoundError struct {
    Resource string
    ID       string
}

func (e *NotFoundError) Error() string {
    if e.ID != "" {
        return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
    }
    return fmt.Sprintf("%s not found", e.Resource)
}

func NewNotFoundError(resource, id string) *NotFoundError {
    return &NotFoundError{Resource: resource, ID: id}
}

type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    if e.Field != "" {
        return fmt.Sprintf("%s: %s", e.Field, e.Message)
    }
    return e.Message
}

func NewValidationError(field, message string) *ValidationError {
    return &ValidationError{Field: field, Message: message}
}

type ConflictError struct {
    Resource string
    Reason   string
}

func (e *ConflictError) Error() string {
    if e.Reason != "" {
        return fmt.Sprintf("%s conflict: %s", e.Resource, e.Reason)
    }
    return fmt.Sprintf("%s conflict", e.Resource)
}

func NewConflictError(resource, reason string) *ConflictError {
    return &ConflictError{Resource: resource, Reason: reason}
}

type OptimisticLockError struct {
    Resource string
    ID       string
}

func (e *OptimisticLockError) Error() string {
    return fmt.Sprintf("%s has been modified: %s", e.Resource, e.ID)
}

func NewOptimisticLockError(resource, id string) *OptimisticLockError {
    return &OptimisticLockError{Resource: resource, ID: id}
}
```

## Error Translation

Translate service errors to HTTP responses at the API layer.

```go
// internal/api/errors.go
package api

import (
    "errors"
    "net/http"

    "github.com/yourorg/myapp/internal/apperrors"
)

func handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
    var notFoundErr *apperrors.NotFoundError
    if errors.As(err, &notFoundErr) {
        NotFound(w, r, err, err.Error())
        return
    }

    var validationErr *apperrors.ValidationError
    if errors.As(err, &validationErr) {
        BadRequest(w, r, err, err.Error(), validationErr.Field)
        return
    }

    var conflictErr *apperrors.ConflictError
    if errors.As(err, &conflictErr) {
        ConflictError(w, r, err, err.Error())
        return
    }

    var optimisticLockErr *apperrors.OptimisticLockError
    if errors.As(err, &optimisticLockErr) {
        ConflictError(w, r, err, "resource has been modified, please refresh and try again")
        return
    }

    InternalError(w, r, err, "internal server error")
}
```

## Validation Strategy

Validation happens at two layers with distinct responsibilities:

### API Layer (Structural Validation)
- Request format and required fields (`validate:"required"`)
- Field length limits (`validate:"max=255"`)
- Format validation (email, URL, enum values)
- Uses `go-playground/validator` struct tags

### Service Layer (Business Validation)
- Business rules that require context or database state
- Cross-field validation logic
- Domain invariants (e.g., "can't delete a product with active orders")

**Key principle**: API layer validates structure, service layer validates business rules. Don't duplicate validation logic between layers.

```go
// API: structural validation via tags
type CreateProductRequest struct {
    Name string `json:"name" validate:"required,max=255"`
}

// Service: business rule validation
func (s *ProductService) CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
    // Business rule: check for duplicate names (requires DB)
    if exists, _ := s.repo.ExistsByName(ctx, req.Name); exists {
        return nil, apperrors.NewConflictError("product", "name already exists")
    }
    return s.repo.Create(ctx, req)
}
```

## Testing Philosophy

Test what matters, not for coverage metrics. Each layer has different testing needs.

### Repository Layer
- Integration tests against real database (use test containers or dedicated test DB)
- Test SQL queries work correctly with edge cases (NULL values, empty results)
- Mock the database only when testing other layers

### Service Layer
- Unit tests with mocked repository interfaces
- Focus on business logic and error handling
- Test edge cases and error paths

### API Layer
- Unit tests with mocked service interfaces using `httptest`
- Test request parsing, validation, and response formatting
- Integration tests for full request/response cycles

### What NOT to Test
- Generated code (trust your code generator)
- Simple getters/setters with no logic
- Framework behavior (Chi routing, Viper config loading)

### Mocking Pattern

Each layer defines its own interface, making mocking straightforward:

```go
// Service test with mock repository
type mockProductRepo struct {
    products map[string]*models.Product
}

func (m *mockProductRepo) GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
    if p, ok := m.products[params.ProductID]; ok {
        return p, nil
    }
    // Mock returns apperrors directly (same as real repository)
    return nil, apperrors.NewNotFoundError("product", params.ProductID)
}

func TestGetProduct_NotFound(t *testing.T) {
    svc := service.NewProductService(&mockProductRepo{products: map[string]*models.Product{}})
    _, err := svc.GetProduct(context.Background(), models.GetProductParams{ProductID: "missing"})

    var notFound *apperrors.NotFoundError
    if !errors.As(err, &notFound) {
        t.Errorf("expected NotFoundError, got %v", err)
    }
}
```
