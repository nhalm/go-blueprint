# Errors

How errors flow from the database through every layer to the HTTP response.

## The Chain

```
DB predicate (generated)
  → repository sentinel      (internal/repository/errors.go)
    → domain sentinel        (internal/errors/errors.go)
      → HTTP response        (internal/api/errors.go via chikit)
```

Each layer translates errors into its own vocabulary — the API layer never imports `repository`, and the service layer never imports `chikit`.

## `internal/errors` — Domain Sentinels + ValidationError

Sentinel errors are package-level `var` values compared with `errors.Is`. Because `errors.Is` unwraps the error chain, a service can wrap a sentinel with additional context (`fmt.Errorf("creating product: %w", apierrors.ErrDuplicateName)`) and the API layer's `handleServiceError` will still match it correctly. This means layers can add context without breaking the translation chain.

Sentinels also keep error handling declarative — `handleServiceError` is a single switch rather than string matching or type assertions scattered across handlers. Adding a new error condition means adding one sentinel and one case; nothing else changes.

The foundation imported by every layer:

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

Structured validation errors for multi-field failures:

```go
// internal/errors/validation.go
package errors

type FieldError struct {
    Field   string
    Code    string
    Message string
}

type ValidationError struct {
    Fields []FieldError
}

func (e *ValidationError) Error() string { /* ... */ }
func NewValidationError(fields ...FieldError) *ValidationError { return &ValidationError{Fields: fields} }
```

## Repository Layer — DB → Repository Sentinels

skimatik generates predicate helpers for every Postgres error category. The `translateError` function in the repository package converts them to repository-level sentinels:

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

Every hand-written repo method wraps its generated call with `translateError`:

```go
func (r *ProductRepository) GetByAccountAndID(ctx context.Context, accountID, id uuid.UUID) (models.Product, error) {
    row, err := r.GetProductByAccountAndID(ctx, executorFromContext(ctx, r.db), accountID, id)
    if err != nil {
        return models.Product{}, translateError(err)
    }
    return toProductModel(row), nil
}
```

skimatik's full predicate set:

| Helper | Meaning | Typical mapping |
|--------|---------|-----------------|
| `IsNotFound`         | No row matched              | `ErrNotFound` |
| `IsAlreadyExists`    | Unique-constraint violation | `ErrAlreadyExists` / `ErrDuplicate*` |
| `IsInvalidReference` | Foreign-key violation       | domain-specific conflict |
| `IsValidationError`  | CHECK / NOT NULL violation  | `ValidationError` with field detail |
| `IsConnectionError`  | Connection dropped / refused | `ErrServiceUnavailable` |
| `IsTimeout`          | Statement timeout           | `ErrRequestTimeout` |
| `IsDatabaseError`    | Catch-all database error    | `ErrDatabaseFailed` |

## Service Layer — Repository → Domain Sentinels

The service maps repository sentinels to domain sentinels from `internal/errors`. This keeps the API layer free of any repository or DB knowledge:

```go
func (s *ProductService) Get(ctx context.Context, params models.GetProductParams) (models.Product, error) {
    p, err := s.repo.GetByAccountAndID(ctx, params.AccountID, params.ProductID)
    if errors.Is(err, repository.ErrNotFound) {
        return models.Product{}, apierrors.ErrProductNotFound
    }
    return p, err
}
```

## API Layer — Domain → HTTP

One function in `internal/api/errors.go` translates every domain error to an HTTP response. All handlers call it — no switch statements outside this file:

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

    // Server errors — log full detail, return a generic response.
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

Rule of thumb: **client-facing message** → `chikit.SetError(r, chikit.ErrXxx.With(...))`. **Server-side diagnostic** → `canonlog.ErrorAdd(r.Context(), err)` AND a generic `chikit.ErrInternal` to the client. Never leak SQL, stack traces, or provider errors to the response body.

## Wire Format

`chikit.SetError` emits a consistent JSON shape:

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
      { "field": "name",        "code": "required", "message": "name is required" },
      { "field": "description", "code": "max",      "message": "description must be at most 1000 characters" }
    ]
  }
}
```

## Validation — Which Layer Owns What

**API layer — structural validation.** Struct tags via `validator.v10` (wired by `chikit.Binder()`). Catches required fields, length limits, format constraints. Runs before any service call. Failures map to `chikit.ErrBadRequest` or `chikit.NewValidationError(...)`.

**Service layer — business validation.** Anything requiring DB state or cross-field context: duplicate names, referenced resources, state transitions. Returns a domain error from `internal/errors`.

Don't duplicate — structural validation belongs in the API layer, business validation belongs in the service layer.
