# API Conventions

This document covers API response patterns, handlers, middleware, and routing.

## Response Structure

### Single Resources

No envelope, returned at root level.

```json
{
  "id": "prod_2ArTLVPddDx8vZk7CqEbiYp1",
  "name": "Premium Plan",
  "active": true,
  "created_at": "2025-01-15T10:30:00Z"
}
```

### Collections

Minimal envelope with pagination.

```json
{
  "data": [
    { "id": "prod_123", "name": "Plan A" },
    { "id": "prod_456", "name": "Plan B" }
  ],
  "has_more": true,
  "next_cursor": "prod_456",
  "prev_cursor": "prod_123"
}
```

### Errors

Separate structure, never mixed with success.

```json
{
  "error": {
    "type": "invalid_request_error",
    "code": "validation_error",
    "message": "name is required",
    "param": "name"
  }
}
```

## Response Helpers

```go
// internal/api/responder.go

func Success(w http.ResponseWriter, data any) {
    renderJSON(w, http.StatusOK, data)
}

func Created(w http.ResponseWriter, data any) {
    renderJSON(w, http.StatusCreated, data)
}

func List(w http.ResponseWriter, data any, hasMore bool, nextCursor, prevCursor string) {
    renderJSON(w, http.StatusOK, NewListResponse(data, hasMore, nextCursor, prevCursor))
}

func BadRequest(w http.ResponseWriter, r *http.Request, err error, message, param string) {
    renderError(w, r, http.StatusBadRequest, err, message, param)
}

func NotFound(w http.ResponseWriter, r *http.Request, err error, message string) {
    renderError(w, r, http.StatusNotFound, err, message, "")
}

func InternalError(w http.ResponseWriter, r *http.Request, err error, message string) {
    renderError(w, r, http.StatusInternalServerError, err, message, "")
}
```

## Response Types

```go
// internal/api/response.go

// ListResponse wraps collection responses with pagination metadata.
type ListResponse struct {
    Data       any    `json:"data"`
    HasMore    bool   `json:"has_more"`
    NextCursor string `json:"next_cursor,omitempty"`
    PrevCursor string `json:"prev_cursor,omitempty"`
}

// ErrorResponse represents all API error responses.
type ErrorResponse struct {
    Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the specifics of an API error.
type ErrorDetail struct {
    Type    string `json:"type"`
    Code    string `json:"code"`
    Message string `json:"message"`
    Param   string `json:"param,omitempty"`
}

func NewListResponse(data any, hasMore bool, nextCursor, prevCursor string) *ListResponse {
    return &ListResponse{
        Data:       data,
        HasMore:    hasMore,
        NextCursor: nextCursor,
        PrevCursor: prevCursor,
    }
}

func NewErrorResponse(httpStatusCode int, err error, message, param string) *ErrorResponse {
    errorType := "api_error"
    if httpStatusCode >= 400 && httpStatusCode < 500 {
        errorType = "invalid_request_error"
    }

    errorCode := "unknown_error"
    if err != nil {
        errorCode = err.Error()
    }

    return &ErrorResponse{
        Error: ErrorDetail{
            Type:    errorType,
            Code:    errorCode,
            Message: message,
            Param:   param,
        },
    }
}
```

## Request/Response Models

```go
// internal/api/models.go

// CreateProductRequest represents the request body for creating a product.
type CreateProductRequest struct {
    Name        string            `json:"name" validate:"required,max=255"`
    Description *string           `json:"description" validate:"omitempty,max=1000"`
    Active      bool              `json:"active"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}

// ProductResponse represents a product resource in API responses.
type ProductResponse struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Active      bool              `json:"active"`
    Metadata    map[string]string `json:"metadata"`
    CreatedAt   string            `json:"created_at"`
    UpdatedAt   string            `json:"updated_at"`
}

// UpdateProductRequest represents the request body for updating a product.
type UpdateProductRequest struct {
    Name        *string           `json:"name" validate:"omitempty,max=255"`
    Description *string           `json:"description" validate:"omitempty,max=1000"`
    Active      *bool             `json:"active"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

## Validation

```go
// internal/api/validator.go
package api

import (
    "fmt"
    "strings"

    "github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
    validate = validator.New()
}

func ValidateStruct(s any) error {
    if err := validate.Struct(s); err != nil {
        if validationErrors, ok := err.(validator.ValidationErrors); ok {
            var messages []string
            for _, fieldError := range validationErrors {
                messages = append(messages, formatValidationError(fieldError))
            }
            return fmt.Errorf("%s", strings.Join(messages, "; "))
        }
        return err
    }
    return nil
}

func formatValidationError(err validator.FieldError) string {
    field := strings.ToLower(err.Field())

    switch err.Tag() {
    case "required":
        return fmt.Sprintf("%s is required", field)
    case "min":
        return fmt.Sprintf("%s must be at least %s", field, err.Param())
    case "max":
        return fmt.Sprintf("%s must be at most %s", field, err.Param())
    case "oneof":
        return fmt.Sprintf("%s must be one of: %s", field, err.Param())
    case "url":
        return fmt.Sprintf("%s must be a valid URL", field)
    default:
        return fmt.Sprintf("%s is invalid", field)
    }
}
```

## Routing

```go
// internal/api/routes.go
package api

import (
    "net/http"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/go-chi/cors"
    canonhttp "github.com/nhalm/canonlog/http"
    "github.com/nhalm/chikit/ratelimit"
    "github.com/nhalm/chikit/ratelimit/store"
    chikitvalidate "github.com/nhalm/chikit/validate"
    httpSwagger "github.com/swaggo/http-swagger/v2"

    _ "github.com/yourorg/myapp/docs" // Generated Swagger docs
)

type RouteConfig struct {
    ReadRPS        int
    WriteRPS       int
    MaxBodyBytes   int64
    AllowedOrigins []string
}

func DefaultRouteConfig() RouteConfig {
    return RouteConfig{
        ReadRPS:        100,
        WriteRPS:       20,
        MaxBodyBytes:   1048576,
        AllowedOrigins: []string{"http://localhost:5173"},
    }
}

func (h *Handler) Routes() http.Handler {
    return h.RoutesWithConfig(DefaultRouteConfig())
}

func (h *Handler) RoutesWithConfig(config RouteConfig) http.Handler {
    r := chi.NewRouter()

    st := store.NewMemory()

    readLimiter := ratelimit.NewBuilder(st).
        WithName("read").
        WithIP().
        Limit(config.ReadRPS, time.Second)

    writeLimiter := ratelimit.NewBuilder(st).
        WithName("write").
        WithIP().
        Limit(config.WriteRPS, time.Second)

    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(canonhttp.ChiMiddleware(nil))
    r.Use(chikitvalidate.MaxBodySize(config.MaxBodyBytes))
    r.Use(middleware.Recoverer)
    r.Use(middleware.Timeout(60 * time.Second))

    r.Use(cors.Handler(cors.Options{
        AllowedOrigins:   config.AllowedOrigins,
        AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
        AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
        ExposedHeaders:   []string{"Link"},
        AllowCredentials: true,
        MaxAge:           300,
    }))

    r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("OK"))
    })

    r.Get("/swagger/*", httpSwagger.WrapHandler)

    r.Route("/api/v1", func(r chi.Router) {
        r.Group(func(r chi.Router) {
            r.Use(readLimiter)
            r.Get("/products", h.ListProducts)
            r.Get("/products/{id}", h.GetProduct)
        })

        r.Group(func(r chi.Router) {
            r.Use(writeLimiter)
            r.Post("/products", h.CreateProduct)
            r.Patch("/products/{id}", h.UpdateProduct)
            r.Delete("/products/{id}", h.DeleteProduct)
        })
    })

    return r
}

func ParseAllowedOrigins(originsStr string) []string {
    if originsStr == "" {
        return []string{"http://localhost:5173"}
    }
    origins := strings.Split(originsStr, ",")
    for i, origin := range origins {
        origins[i] = strings.TrimSpace(origin)
    }
    return origins
}
```

## Complete Handler Example

```go
// internal/api/handler.go
package api

import (
    "context"
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    "github.com/nhalm/canonlog"
    "github.com/yourorg/myapp/internal/models"
)

// ProductService defines only the methods the API layer needs from the product service.
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
    var req CreateProductRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        BadRequest(w, r, err, "invalid request body", "")
        return
    }

    if err := ValidateStruct(req); err != nil {
        BadRequest(w, r, err, err.Error(), "")
        return
    }

    canonlog.AddRequestFields(r.Context(), map[string]any{
        "product_name": req.Name,
    })

    serviceReq := models.CreateProductRequest{
        Name:        req.Name,
        Description: req.Description,
        Active:      req.Active,
        Metadata:    req.Metadata,
    }

    product, err := h.productSvc.CreateProduct(r.Context(), &serviceReq)
    if err != nil {
        handleServiceError(w, r, err)
        return
    }

    Created(w, convertToProductResponse(product))
}

func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
    limit := 10
    if l := r.URL.Query().Get("limit"); l != "" {
        if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
            limit = parsed
        }
    }

    var active *bool
    if a := r.URL.Query().Get("active"); a != "" {
        b := a == "true"
        active = &b
    }

    filter := models.ListProductsFilter{
        Active:        active,
        Limit:         limit,
        StartingAfter: ptrOrNil(r.URL.Query().Get("starting_after")),
        EndingBefore:  ptrOrNil(r.URL.Query().Get("ending_before")),
    }

    result, err := h.productSvc.ListProducts(r.Context(), filter)
    if err != nil {
        handleServiceError(w, r, err)
        return
    }

    responses := make([]ProductResponse, len(result.Products))
    for i, p := range result.Products {
        responses[i] = convertToProductResponse(p)
    }

    var nextCursor, prevCursor string
    if result.NextCursor != nil {
        nextCursor = *result.NextCursor
    }
    if result.PrevCursor != nil {
        prevCursor = *result.PrevCursor
    }

    List(w, responses, result.HasMore, nextCursor, prevCursor)
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

func (h *Handler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    var req UpdateProductRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        BadRequest(w, r, err, "invalid request body", "")
        return
    }

    if err := ValidateStruct(req); err != nil {
        BadRequest(w, r, err, err.Error(), "")
        return
    }

    serviceReq := models.UpdateProductRequest{
        ID:          id,
        Name:        req.Name,
        Description: req.Description,
        Active:      req.Active,
        Metadata:    req.Metadata,
    }

    product, err := h.productSvc.UpdateProduct(r.Context(), &serviceReq)
    if err != nil {
        handleServiceError(w, r, err)
        return
    }

    Success(w, convertToProductResponse(product))
}

func (h *Handler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    if err := h.productSvc.DeleteProduct(r.Context(), models.DeleteProductParams{
        ProductID: id,
    }); err != nil {
        handleServiceError(w, r, err)
        return
    }

    w.WriteHeader(http.StatusNoContent)
}

func convertToProductResponse(product *models.Product) ProductResponse {
    var description string
    if product.Description != nil {
        description = *product.Description
    }

    return ProductResponse{
        ID:          product.ID,
        Name:        product.Name,
        Description: description,
        Active:      product.Active,
        Metadata:    product.Metadata,
        CreatedAt:   product.CreatedAt.Format("2006-01-02T15:04:05Z"),
        UpdatedAt:   product.UpdatedAt.Format("2006-01-02T15:04:05Z"),
    }
}

func ptrOrNil(s string) *string {
    if s == "" {
        return nil
    }
    return &s
}
```

## Swagger Annotations

Add Swagger annotations to handlers for OpenAPI documentation.

```go
// main.go
// @title My API
// @version 1.0
// @description API description
// @host localhost:8080
// @BasePath /api/v1

// API models (internal/api/models.go)
// @Description Request payload for creating a product
type CreateProductRequest struct {
    // Name of the product
    // @example "Premium Plan"
    Name string `json:"name" validate:"required,max=255"`

    // Optional description
    // @example "Full access to all features"
    Description string `json:"description" validate:"max=1000"`
}
```

Generate with:
```bash
swag init -g cmd/myapp/main.go -o docs
```
