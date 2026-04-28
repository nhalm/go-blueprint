# Errors

How errors flow from the database through every layer to the HTTP response.

The canonical sentinel set, repository `translateError`, service-layer mapping, and HTTP `handleServiceError` for the Products slice live in [EXAMPLE.md](EXAMPLE.md). This doc explains the chain; EXAMPLE.md is the source of truth for the code. Full chikit `APIError` shape, `Err*` sentinel table, and skimatik `IsXxx` predicate set are in [LIBRARIES.md](LIBRARIES.md).

## The Chain

```
DB predicate (generated)
  → repository sentinel      (internal/repository/errors.go)
    → domain sentinel        (internal/errors/errors.go)
      → HTTP response        (internal/api/errors.go via chikit)
```

Each layer translates errors into its own vocabulary — the API layer never imports `repository`, and the service layer never imports `chikit`.

## `internal/errors` — Domain Sentinels + ValidationError

Sentinel errors are package-level `var` values compared with `errors.Is`. Because `errors.Is` unwraps the error chain, a service can wrap a sentinel with additional context (`fmt.Errorf("creating product: %w", apperrors.ErrDuplicateName)`) and the API layer's `handleServiceError` will still match it correctly. This means layers can add context without breaking the translation chain.

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

```go {file=internal/repository/errors.go}
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
        return models.Product{}, apperrors.ErrProductNotFound
    }
    return p, err
}
```

## API Layer — Domain → HTTP

One function in `internal/api/errors.go` translates every domain error to an HTTP response. All handlers call it — no switch statements outside this file. The full implementation is the canonical Products switch in [EXAMPLE.md](EXAMPLE.md#error-mapping).

The translation has three shapes:

- **Structured validation errors** (`*apperrors.ValidationError`) → `chikit.NewValidationError([]chikit.FieldError{...})`. Each `FieldError` carries `Param` / `Code` / `Message`, surfacing per-field detail to the client.
- **Client-facing sentinels** (`apperrors.ErrProductNotFound`, `ErrDuplicateName`, `ErrForbidden`, `ErrInvalidInput`) → `chikit.ErrXxx.With("user-safe message")`. Status comes from the chikit sentinel.
- **Server-error sentinels** (`apperrors.ErrDatabaseFailed`, `ErrEncryptionFailed`, `ErrDependencyFailed`, default) → `canonlog.ErrorAdd(r.Context(), err)` to log the real cause, then `chikit.ErrInternal` to return a generic 500 to the client.
- **Custom statuses** (`apperrors.ErrServiceUnavailable` → 503) → constructed `&chikit.APIError{Type, Code, Message, Status}` directly.

Rule of thumb: **client-facing message** → `chikit.SetError(r, chikit.ErrXxx.With(...))`. **Server-side diagnostic** → `canonlog.ErrorAdd(r.Context(), err)` AND a generic `chikit.ErrInternal` to the client. Never leak SQL, stack traces, or provider errors to the response body.

Adding a new error case means adding one sentinel in `internal/errors/errors.go` and one case in EXAMPLE.md's `handleServiceError` switch — no other files change.

## Wire Format

`chikit.SetError` emits a consistent JSON shape — single envelope `{"error": {...}}` wrapping `chikit.APIError`:

```json
{
  "error": {
    "type":    "request_error",
    "code":    "bad_request",
    "message": "name is required",
    "param":   "name"
  }
}
```

Multi-field validation errors use an `errors` array instead of `param` (each entry has `param`/`code`/`message`):

```json
{
  "error": {
    "type":    "validation_error",
    "code":    "invalid_request",
    "message": "Validation failed",
    "errors": [
      { "param": "name",        "code": "required", "message": "name is required" },
      { "param": "description", "code": "max",      "message": "description must be at most 1000 characters" }
    ]
  }
}
```

`type` and `code` come from the sentinel chosen — see [LIBRARIES.md](LIBRARIES.md#sentinels) for the full table.

## Validation — Which Layer Owns What

**API layer — structural validation.** Struct tags via `validator.v10` (wired by `chikit.Binder()`). Catches required fields, length limits, format constraints. Runs before any service call. Failures map to `chikit.ErrBadRequest` or `chikit.NewValidationError(...)`.

**Service layer — business validation.** Anything requiring DB state or cross-field context: duplicate names, referenced resources, state transitions. Returns a domain error from `internal/errors`.

Don't duplicate — structural validation belongs in the API layer, business validation belongs in the service layer.
