# Library Surface Reference

Every external symbol the blueprint commits to using, in one place. Authoritative for *how the blueprint calls these libraries*; each library's own repository is authoritative for everything beyond what the blueprint uses.

Verified against repo source (April 2026):

- [chikit](https://github.com/nhalm/chikit) `main`
- [canonlog](https://github.com/nhalm/canonlog) `main`
- [pgxkit/v2](https://github.com/nhalm/pgxkit) `main` (module path `github.com/nhalm/pgxkit/v2`)
- [skimatik](https://github.com/nhalm/skimatik) `main`

If you need a symbol that isn't listed here, the blueprint hasn't committed to it — add it to LIBRARIES.md at the same time you add it to the blueprint pattern.

## chikit

### Sentinels

All sentinels are `*APIError` (pointer values). Apply `.With(msg)` for a custom message; `.WithParam(msg, param)` for a single-field error message. Both methods return a fresh `*APIError` (no mutation).

| Variable | Status | Type | Code |
|----------|--------|------|------|
| `chikit.ErrBadRequest`          | 400 | `request_error`     | `bad_request`         |
| `chikit.ErrUnauthorized`        | 401 | `auth_error`        | `unauthorized`        |
| `chikit.ErrPaymentRequired`     | 402 | `request_error`     | `payment_required`    |
| `chikit.ErrForbidden`           | 403 | `auth_error`        | `forbidden`           |
| `chikit.ErrNotFound`            | 404 | `not_found`         | `resource_not_found`  |
| `chikit.ErrMethodNotAllowed`    | 405 | `request_error`     | `method_not_allowed`  |
| `chikit.ErrConflict`            | 409 | `request_error`     | `conflict`            |
| `chikit.ErrGone`                | 410 | `request_error`     | `gone`                |
| `chikit.ErrPayloadTooLarge`     | 413 | `request_error`     | `payload_too_large`   |
| `chikit.ErrUnprocessableEntity` | 422 | `validation_error`  | `unprocessable`       |
| `chikit.ErrRateLimited`         | 429 | `rate_limit_error`  | `limit_exceeded`      |
| `chikit.ErrInternal`            | 500 | `internal_error`    | `internal`            |
| `chikit.ErrNotImplemented`      | 501 | `request_error`     | `not_implemented`     |
| `chikit.ErrServiceUnavailable`  | 503 | `request_error`     | `service_unavailable` |
| `chikit.ErrGatewayTimeout`      | 504 | `timeout_error`     | `gateway_timeout`     |

Custom statuses (e.g. 503 with a service-specific code): construct `&chikit.APIError{Type, Code, Message, Status}` directly.

### APIError and FieldError

```go
type APIError struct {
    Type    string       `json:"type"`
    Code    string       `json:"code,omitempty"`
    Message string       `json:"message"`
    Param   string       `json:"param,omitempty"`
    Errors  []FieldError `json:"errors,omitempty"`
    Status  int          `json:"-"`
}

type FieldError struct {
    Param   string `json:"param"`
    Code    string `json:"code"`
    Message string `json:"message"`
}

func (e *APIError) Error() string
func (e *APIError) Is(target error) bool                   // implements errors.Is
func (e *APIError) With(message string) *APIError          // copy with new message
func (e *APIError) WithParam(message, param string) *APIError  // copy with new message + param
```

Wire format wraps in a single `{"error": APIError}` envelope.

### Validation error constructor

```go
func chikit.NewValidationError(errors []FieldError) *APIError  // 400, type=validation_error, code=invalid_request
```

### Handler middleware

`chikit.Handler` is the response-writing middleware that **must** be the outermost layer of the chi stack — it owns the deferred response cleanup that `SetResponse`/`SetError` rely on.

```go
func chikit.Handler(opts ...HandlerOption) func(http.Handler) http.Handler

// Options
chikit.WithCanonlog()                                              // attach canonlog logger to ctx, flush on completion
chikit.WithCanonlogFields(fn func(*http.Request) map[string]any)   // add custom fields just before flush
chikit.WithSLOs()                                                  // log SLO PASS/FAIL (requires WithCanonlog)
chikit.WithTimeout(d time.Duration)                                // hard cutoff → 504
chikit.WithGracefulShutdown(d time.Duration)                       // grace after timeout, default 5s
chikit.WithAbandonCallback(fn func())                              // called when handler doesn't exit in grace period

// Graceful shutdown helpers
func chikit.WaitForHandlers(ctx context.Context) error              // block until in-flight handlers exit
```

### Response helpers

```go
func chikit.SetResponse(r *http.Request, status int, body any)
func chikit.SetError(r *http.Request, err *APIError)
func chikit.SetHeader(r *http.Request, key, value string)
func chikit.AddHeader(r *http.Request, key, value string)
func chikit.HasState(ctx context.Context) bool                     // true when chikit.Handler is active
```

All four `Set*`/`AddHeader` calls are silently no-ops if `chikit.Handler` is not in the stack.

### Request binding

```go
func chikit.Binder(opts ...BinderOption) func(http.Handler) http.Handler
func chikit.BindWithFormatter(fn func(field, tag, param string) string) BinderOption

func chikit.JSON(r *http.Request, dest any) bool   // decode + validate JSON body
func chikit.Query(r *http.Request, dest any) bool  // decode + validate query params

func chikit.RegisterValidation(tag string, fn validator.Func) error  // register a custom tag — returns error
```

`JSON` and `Query` return `false` after writing a 400 response — handler should `return` immediately.

Struct tags: `validate:"required,..."` for body, `query:"limit" validate:"omitempty,..."` for query strings.

### Header extraction

```go
func chikit.ExtractHeader(httpName, ctxKey string, opts ...HeaderExtractorOption) func(http.Handler) http.Handler
func chikit.HeaderFromContext(ctx context.Context, key string) (any, bool)

chikit.ExtractRequired()                                              // 400 if header missing
chikit.ExtractDefault(val any)                                        // fallback if absent
chikit.ExtractWithValidator(fn func(string) (any, error))             // transform/validate inbound value
```

### Body and header validation

```go
func chikit.MaxBodySize(size int64) func(http.Handler) http.Handler
// Two-stage protection: Content-Length pre-check (immediate 413) + MaxBytesReader streaming wrap.

func chikit.ValidateHeaders(rules ...ValidateHeaderRule) func(http.Handler) http.Handler
func chikit.ValidateWithHeader(name string, opts ...ValidateHeaderOption) ValidateHeaderRule

chikit.ValidateRequired()
chikit.ValidateAllowList(values ...string)
chikit.ValidateDenyList(values ...string)
chikit.ValidateCaseSensitive()                                       // default is case-insensitive
```

### Rate limiting

```go
func chikit.NewRateLimiter(st Store, limit int, window time.Duration, opts ...RateLimitOption) *RateLimiter
// Panics if no key dimension is provided.

// Key dimensions (combine multiple for layered limits)
chikit.RateLimitWithIP()                              // RemoteAddr
chikit.RateLimitWithRealIP()                          // X-Forwarded-For / X-Real-IP (skip if missing)
chikit.RateLimitWithRealIPRequired()                  // same, but 400 if missing
chikit.RateLimitWithEndpoint()                        // method + path
chikit.RateLimitWithHeader(name string)               // header value (skip if missing)
chikit.RateLimitWithHeaderRequired(name string)
chikit.RateLimitWithQueryParam(name string)
chikit.RateLimitWithQueryParamRequired(name string)

// Behavior
chikit.RateLimitWithName(name string)                 // key prefix for layered limits
chikit.RateLimitWithHeaderMode(mode RateLimitHeaderMode)

const (
    chikit.RateLimitHeadersAlways          RateLimitHeaderMode = iota // default
    chikit.RateLimitHeadersOnLimitExceeded
    chikit.RateLimitHeadersNever
)

// Response headers when active: RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset, Retry-After (429 only).
```

### Authentication

```go
func chikit.APIKey(validator func(string) bool, opts ...APIKeyOption) func(http.Handler) http.Handler
func chikit.APIKeyFromContext(ctx context.Context) (string, bool)
chikit.WithAPIKeyHeader(name string)                  // default: X-API-Key
chikit.WithOptionalAPIKey()

func chikit.BearerToken(validator func(string) bool, opts ...BearerTokenOption) func(http.Handler) http.Handler
func chikit.BearerTokenFromContext(ctx context.Context) (string, bool)
chikit.WithOptionalBearerToken()
```

### SLO tracking

```go
func chikit.SLO(tier SLOTier) func(http.Handler) http.Handler
func chikit.SLOWithTarget(target time.Duration) func(http.Handler) http.Handler
func chikit.GetSLO(ctx context.Context) (SLOTier, time.Duration, bool)

const (
    chikit.SLOCritical  SLOTier = iota // 50ms target
    chikit.SLOHighFast                 // 100ms
    chikit.SLOHighSlow                 // 1000ms
    chikit.SLOLow                      // 5000ms
)
```

Requires `WithCanonlog()` and `WithSLOs()` on `chikit.Handler` to log status.

### Stores (`chikit/store`)

```go
type store.Store interface {
    Increment(ctx context.Context, key string, delta int) (int, error)
    Get(ctx context.Context, key string) (int, error)
    Set(ctx context.Context, key string, value int, expiry time.Duration) error
    Delete(ctx context.Context, key string) error
    Close() error
}

func store.NewMemory() store.Store                    // dev/test only
func store.NewRedis(cfg store.RedisConfig) (store.Store, error)

type store.RedisConfig struct {
    URL          string                                // required
    Password     string
    DB           int
    Prefix       string                                // default: "ratelimit:"
    PoolSize     int
    MinIdleConns int
    DialTimeout  time.Duration
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
}
```

## canonlog

Canonical-log-line library: one structured event per request, accumulated through context across handlers and services.

```go
func canonlog.SetupGlobalLogger(level, format string)            // call once after LoadLogging; format = "text" | "json"

func canonlog.New(opts ...canonlog.Option) *canonlog.Logger
func canonlog.NewContext(ctx context.Context) context.Context    // attach a fresh logger to ctx (test setup)
func canonlog.WithLevel(level slog.Level) canonlog.Option

func canonlog.GetLogger(ctx context.Context) *canonlog.Logger    // panics if absent
func canonlog.TryGetLogger(ctx context.Context) (*canonlog.Logger, bool)
```

### Field accumulation

Two equivalent surfaces — **context-form** (most handlers/services) and **logger-form** (CLI commands and tests):

```go
// Context-form
func canonlog.DebugAdd(ctx context.Context, key string, value any)
func canonlog.DebugAddMany(ctx context.Context, fields map[string]any)
func canonlog.InfoAdd(ctx context.Context, key string, value any)
func canonlog.InfoAddMany(ctx context.Context, fields map[string]any)
func canonlog.WarnAdd(ctx context.Context, key string, value any)
func canonlog.WarnAddMany(ctx context.Context, fields map[string]any)
func canonlog.ErrorAdd(ctx context.Context, err error)
func canonlog.Flush(ctx context.Context)

// Logger-form (chainable, returns *Logger)
func (l *canonlog.Logger) DebugAdd(key string, value any) *canonlog.Logger
func (l *canonlog.Logger) DebugAddMany(fields map[string]any) *canonlog.Logger
func (l *canonlog.Logger) InfoAdd(key string, value any) *canonlog.Logger
func (l *canonlog.Logger) InfoAddMany(fields map[string]any) *canonlog.Logger
func (l *canonlog.Logger) WarnAdd(key string, value any) *canonlog.Logger
func (l *canonlog.Logger) WarnAddMany(fields map[string]any) *canonlog.Logger
func (l *canonlog.Logger) ErrorAdd(err error) *canonlog.Logger
func (l *canonlog.Logger) Flush(ctx context.Context)
```

`value` is `any` — pass strings, numbers, durations, bools, anything that's slog-friendly.

`Flush` emits the accumulated event. `chikit.Handler(chikit.WithCanonlog())` calls `Flush` automatically at request end. CLI commands call it explicitly after building the event with `New().InfoAdd(...).Flush(ctx)`.

## pgxkit (v2)

Module path: `github.com/nhalm/pgxkit/v2`.

### Connection

```go
func pgxkit.NewDB() *pgxkit.DB
func (db *DB) Connect(ctx context.Context, dsn string, opts ...ConnectOption) error
func (db *DB) ConnectReadWrite(ctx context.Context, readDSN, writeDSN string, opts ...ConnectOption) error
func (db *DB) Shutdown(ctx context.Context) error
func (db *DB) HealthCheck(ctx context.Context) error             // no Ping method — use this
func (db *DB) IsReady(ctx context.Context) bool

// Pool sizing (single-pool defaults)
pgxkit.WithMaxConns(n int32)
pgxkit.WithMinConns(n int32)
pgxkit.WithMaxConnLifetime(d time.Duration)
pgxkit.WithMaxConnIdleTime(d time.Duration)

// Read/write split (use with ConnectReadWrite)
pgxkit.WithReadMaxConns(n int32)
pgxkit.WithReadMinConns(n int32)
pgxkit.WithWriteMaxConns(n int32)
pgxkit.WithWriteMinConns(n int32)

// Hooks
pgxkit.WithBeforeOperation(fn HookFunc)
pgxkit.WithAfterOperation(fn HookFunc)
pgxkit.WithBeforeTransaction(fn HookFunc)
pgxkit.WithAfterTransaction(fn HookFunc)
pgxkit.WithOnShutdown(fn HookFunc)

// Pool lifecycle
pgxkit.WithOnConnect(fn func(*pgx.Conn) error)
pgxkit.WithOnDisconnect(fn func(*pgx.Conn))
pgxkit.WithOnAcquire(fn func(context.Context, *pgx.Conn) error)
pgxkit.WithOnRelease(fn func(*pgx.Conn))
```

### Query execution (on `*DB`)

```go
func (db *DB) Query(ctx, sql string, args ...any) (pgx.Rows, error)
func (db *DB) QueryRow(ctx, sql string, args ...any) pgx.Row
func (db *DB) Exec(ctx, sql string, args ...any) (pgconn.CommandTag, error)

// Read-pool variants (when ConnectReadWrite is used)
func (db *DB) ReadQuery(ctx, sql string, args ...any) (pgx.Rows, error)
func (db *DB) ReadQueryRow(ctx, sql string, args ...any) pgx.Row
```

### Executor interface

```go
type pgxkit.Executor interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
```

Both `*pgxkit.DB` and `*pgxkit.Tx` satisfy `Executor`. Generated repository methods take an `Executor`; the blueprint's `executorFromContext(ctx, db)` returns the active `*Tx` if present, otherwise the `*DB`.

### Transactions

```go
func (db *DB) BeginTx(ctx context.Context, opts pgx.TxOptions) (*pgxkit.Tx, error)

// On *Tx
func (t *Tx) Query(ctx, sql string, args ...any) (pgx.Rows, error)
func (t *Tx) QueryRow(ctx, sql string, args ...any) pgx.Row
func (t *Tx) Exec(ctx, sql string, args ...any) (pgconn.CommandTag, error)
func (t *Tx) Commit(ctx context.Context) error
func (t *Tx) Rollback(ctx context.Context) error                // no-op after Commit
func (t *Tx) Tx() pgx.Tx                                        // raw pgx.Tx escape hatch
func (t *Tx) IsFinalized() bool

var pgxkit.ErrTxFinalized = errors.New("transaction already finalized")
```

### Test helpers

```go
type pgxkit.TestDB struct {
    *pgxkit.DB                                                  // embedded pointer; methods promoted
}

func pgxkit.NewTestDB() *TestDB
func pgxkit.RequireDB(t *testing.T) *TestDB                     // reads TEST_DATABASE_URL; t.Skip if unset/unreachable

func (tdb *TestDB) Setup() error
func (tdb *TestDB) Clean() error
func (tdb *TestDB) EnableGolden(testName string) *DB            // returns a NEW *DB with plan-capture hook
func (db *DB) AssertGolden(t *testing.T, testName string)       // diff captured plans against testdata/golden/<name>.json

func pgxkit.CleanupGolden(testName string) error                // delete golden files for a test name

type pgxkit.QueryPlan struct {
    Query       int                      `json:"query"`
    SQL         string                   `json:"sql"`
    Plan        []map[string]interface{} `json:"plan"`
    ExecutionMS float64                  `json:"execution_ms,omitempty"`
    PlanningMS  float64                  `json:"planning_ms,omitempty"`
}
```

`RequireDB` does **not** register `t.Cleanup` — process exit handles connection cleanup for typical tests. Pass `testDB.DB` to constructors that take `*pgxkit.DB`.

`EnableGolden` returns a fresh `*DB` that shares the underlying pools but owns its own hook chain. Build the repository over the returned value, not over `testDB.DB`, or queries bypass the capture hook and the golden file silently captures nothing.

### Pool introspection

```go
func (db *DB) Stats() *pgxpool.Stat
func (db *DB) ReadStats() *pgxpool.Stat
func (db *DB) WritePool() *pgxpool.Pool
func (db *DB) ReadPool() *pgxpool.Pool

func pgxkit.GetDSN() string                                     // reads DATABASE_URL
```

## skimatik

Code generator — runs at build time, not invoked from app code. The blueprint surface is the *generated* output plus the configuration and annotation syntax that drives generation.

### `skimatik.yaml`

```yaml
database:
  dsn: "postgres://user:pass@host:port/dbname"
  schema: "public"

output:
  directory: "./internal/repository/generated"
  package:   "generated"

default_functions: "all"     # or a list: ["create", "get", "update", "delete", "list", "paginate"]

tables:
  products:
  accounts:
    functions: ["get", "list"]    # per-table override

queries:
  enabled:   true
  directory: "./internal/repository/queries"
  files:
    - "products.sql"

types:                                                          # optional custom mappings
  mappings:
    custom_type: "GoType"

verbose: true
```

### Query annotations

Each annotated SQL block becomes a method on the generated `*Queries` struct. Parameters use Postgres positional placeholders (`$1`, `$2`).

```sql
-- name: QueryName :type
```

| Annotation | Generates |
|------------|-----------|
| `:one`       | `func(ctx, exec Executor, params...) (*Row, error)` — one row or `IsNotFound` error |
| `:many`      | `func(ctx, exec Executor, params...) ([]Row, error)` |
| `:paginated` | Two functions: standard `:many` form **plus** `func(ctx, exec, params..., pagination PaginationParams) (*PaginationResult[Row], error)` |
| `:exec`      | `func(ctx, exec Executor, params...) error` — no result set |

`:paginated` requires an explicit `ORDER BY`. The order column must appear in the SELECT list and must be a simple column reference. Sort direction is extracted at generation time: ascending uses `>` forward / `<` backward, descending flips that.

### Parameter overrides — `-- param:`

Required when a Postgres column type doesn't map cleanly, or a parameter must be nullable (`NULL` for "no filter").

```sql
-- name: ListUsersWithOptionalFilters :many
-- param: $1 limit       int
-- param: $2 is_active   *bool
-- param: $3 name_filter *string
SELECT id, name, email, is_active
FROM users
WHERE ($2::boolean IS NULL OR is_active = $2)
  AND ($3::text    IS NULL OR name ILIKE $3)
ORDER BY created_at DESC
LIMIT $1;
```

Syntax: `-- param: $N name go_type`. Use a pointer type (`*int`, `*bool`, `*string`, `*time.Time`, etc.) for nullable parameters. The Go field type drives both the generated parameter type and the nil-check that selects "filter" vs "no filter" in the SQL.

### Result overrides — `-- result:`

Required when a SELECT expression's type doesn't match the table column (e.g. aggregate, computed column, alias).

```sql
-- name: GetUserCount :one
-- result: total int
SELECT COUNT(*) AS total FROM users;
```

Syntax: `-- result: column_name GoType`. One annotation per overridden column.

### Default type mapping

| PostgreSQL                                | NOT NULL          | NULLABLE             |
|-------------------------------------------|-------------------|----------------------|
| `SMALLINT` / `INTEGER` / `BIGINT` / serial | `int`             | `*int`               |
| `TEXT` / `VARCHAR`                        | `string`          | `*string`            |
| `BOOLEAN`                                 | `bool`            | `*bool`              |
| `UUID`                                    | `uuid.UUID`       | `*uuid.UUID`         |
| `TIMESTAMP` / `TIMESTAMPTZ` / `DATE`      | `time.Time`       | `*time.Time`         |
| `JSON` / `JSONB`                          | `json.RawMessage` | `*json.RawMessage`   |
| `BYTEA`                                   | `[]byte`          | `*[]byte`            |
| `REAL` / `FLOAT4`                         | `float32`         | `*float32`           |
| `DOUBLE PRECISION` / `FLOAT8` / `NUMERIC` | `float64`         | `*float64`           |
| `INTEGER[]` / `TEXT[]` / `UUID[]`         | `[]T`             | `[]*T`               |

### Generated runtime — pagination

```go
type generated.PaginationParams struct {
    NextCursor   string `json:"next_cursor,omitempty"`
    BeforeCursor string `json:"before_cursor,omitempty"`
    OrderBy      string `json:"order_by,omitempty"`            // defaults to "id"
    Limit        int    `json:"limit,omitempty"`               // default 20, max 100
}

type generated.PaginationResult[T any] struct {
    Items        []T    `json:"items"`
    HasMore      bool   `json:"has_more"`
    HasPrevious  bool   `json:"has_previous"`
    NextCursor   string `json:"next_cursor,omitempty"`
    BeforeCursor string `json:"before_cursor,omitempty"`
    Total        *int   `json:"total,omitempty"`               // optional, expensive
}
```

Cursors are base64-encoded JSON of `{column, value}` — opaque to the caller. Echo them back unchanged.

### Generated runtime — ID generators

```go
func generated.UUIDv7() uuid.UUID                              // default for repos constructed with nil
func generated.UUIDv4() uuid.UUID
```

Repo constructor accepts `func() uuid.UUID`. Passing `nil` activates `UUIDv7`. Pass a custom function to override (deterministic test IDs, UUIDv4 for non-PK uses).

### Generated runtime — error predicates

Generated in the output package; pair with the blueprint's `translateError` to convert into repository sentinels.

```go
func generated.IsNotFound(err error) bool                      // pgx.ErrNoRows
func generated.IsAlreadyExists(err error) bool                 // PG 23505 (unique_violation)
func generated.IsInvalidReference(err error) bool              // PG 23503 (foreign_key_violation)
func generated.IsValidationError(err error) bool               // PG 23514, 23502 (check / not_null)
func generated.IsConnectionError(err error) bool
func generated.IsTimeout(err error) bool
func generated.IsDatabaseError(err error) bool                 // catch-all
```

### Generated runtime — repository constructors

Generated per-table; use the embedded-struct pattern in the blueprint repo:

```go
generated.NewProductsRepository(idGen func() uuid.UUID) *ProductsRepository  // nil → UUIDv7
generated.NewProductsQueries() *ProductsQueries
```

The generated `Create*` / `Get*` / `Update*` / `Delete*` / `List*` / `Paginate*` methods all take `ctx, exec Executor, params...` — `exec` is supplied per call (not stored on the repo), enabling transactional orchestration via `executorFromContext`.

## shortuuid

```go
import "github.com/nhalm/shortuuid"

func shortuuid.ShortenUUID(id uuid.UUID) (string, error)       // 22-char base62
func shortuuid.ExpandUUID(short string) (uuid.UUID, error)
```

The blueprint applies an entity prefix manually (`"prod_" + short`), and trims the prefix before calling `ExpandUUID`.
