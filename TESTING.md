# Testing

Layer strategy, mocking with gomock, test DB setup, and test runners.

## Layer Strategy

| Layer | Test type | DB | Mocked dependency |
|-------|-----------|-----|-------------------|
| `repository`       | Integration | Real Postgres | None |
| `service`          | Unit | None | `MockXRepository` (gomock) |
| `api`              | Unit | None | `MockXServiceInterface` (gomock) |
| `test/e2e/` (opt.) | E2E | Real Postgres + `httptest.Server` | None |

Repository tests prove the SQL works. Service + handler tests prove the business / transport logic works without booting a DB. E2E tests exercise the wiring.

## Layout

Tests live in the same package as the code they cover (`*_test.go`). Integration tests can use a `_integration_test.go` suffix by convention; what actually gates them is `testing.Short()`:

```go
func TestProductRepository_CRUD(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    // ...
}
```

`make test` passes `-short` and skips them; `make test-integration` runs everything.

E2E tests live in a separate `test/e2e/` directory with shared `setup_test.go` / `helpers_test.go` — one file to spin up the full stack (db, migrations, `httptest.Server`) and one with small request-building helpers consumed by the tests. E2E state is seeded through the API itself (POST the resource, then assert the GET), not by direct DB writes, so the tests stay honest about the full request path including auth headers.

## Mocking — gomock

Each layer defines the interfaces it consumes (consumer-owned interfaces — see [ARCHITECTURE.md](ARCHITECTURE.md#consumer-owned-interfaces)). A `//go:generate mockgen` directive points at whichever file declares them.

```go
// internal/service/repository_interface.go
//go:generate mockgen -source=repository_interface.go -destination=repository_interface_mock.go -package=service

package service

type ProductRepository interface {
    Create(ctx context.Context, req models.CreateProductRequest) (models.Product, error)
    GetByID(ctx context.Context, id uuid.UUID) (models.Product, error)
    // ...
}
```

Run `go generate ./...` (via `make generate`) to produce the `*_interface_mock.go` files. Each mock lives in the same package as the interface it implements — `service.MockProductRepository` is declared in `internal/service/repository_interface_mock.go`; `api.MockProductServiceInterface` is declared in `internal/api/service_interface_mock.go`.

For tiny single-method dependencies (key providers, audit hooks), prefer a hand-written struct — generation is overkill:

```go
type stubKeyProvider struct{ key []byte }

func (s *stubKeyProvider) GetCipherKey(ctx context.Context, ref string) ([]byte, error) {
    return s.key, nil
}
```

## Test DB — `pgxkit.RequireDB`

pgxkit ships the helper. Don't hand-roll a `GetTestDB`:

```go
import "github.com/nhalm/pgxkit/v2"

func TestProductRepository_CRUD(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx := context.Background()
    db  := pgxkit.RequireDB(t)   // reads TEST_DATABASE_URL; t.Skip if unreachable

    repo := repository.NewProductRepository(db)
    // ... run CRUD assertions ...
}
```

`pgxkit.RequireDB(t)`:
- Reads `TEST_DATABASE_URL`.
- Calls `t.Skip` if the env var is unset or the DB is unreachable — so unit-only runs don't fail.
- Registers shutdown cleanup via `t.Cleanup` — do not call `db.Shutdown` manually.

For test isolation prefer a rolled-back transaction over per-test TRUNCATE:

```go
tx, err := db.BeginTx(ctx, pgx.TxOptions{})
require.NoError(t, err)
defer tx.Rollback(ctx)  // auto-cleanup; safe even after Commit

txCtx := repository.ContextWithTx(ctx, tx)
// Run operations against txCtx — executorFromContext resolves the tx automatically
```

When transactions don't fit (DDL tests, multi-connection tests) fall back to `pgxkit.CleanupTestData("TRUNCATE products CASCADE", ...)` in a `t.Cleanup`.

## Service Tests — gomock + testify

Table-driven, one case per row, each case brings its own `mockSetup`. Use `wantErr error` (not `bool`) so each case can assert the exact sentinel that should propagate:

```go
// internal/service/product_service_test.go
func TestProductService_CreateProduct(t *testing.T) {
    tests := []struct {
        name      string
        req       models.CreateProductRequest
        mockSetup func(*MockProductRepository)
        wantErr   error
    }{
        {
            name: "successful creation",
            req:  models.CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductRepository) {
                m.EXPECT().
                    Create(gomock.Any(), gomock.Eq(models.CreateProductRequest{Name: "test", Active: true})).
                    Return(models.Product{Name: "test"}, nil)
            },
        },
        {
            name: "duplicate name maps to domain error",
            req:  models.CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductRepository) {
                m.EXPECT().
                    Create(gomock.Any(), gomock.Any()).
                    Return(models.Product{}, repository.ErrAlreadyExists)
            },
            wantErr: apierrors.ErrProductAlreadyExists,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctrl := gomock.NewController(t)
            mockRepo := NewMockProductRepository(ctrl)
            tt.mockSetup(mockRepo)

            svc := service.NewProductService(mockRepo, config.Config{})
            _, err := svc.CreateProduct(context.Background(), tt.req)

            if tt.wantErr != nil {
                require.Error(t, err)
                assert.ErrorIs(t, err, tt.wantErr)
                return
            }
            require.NoError(t, err)
        })
    }
}
```

Use `gomock.InOrder(...)` when a test needs a specific call sequence. Constrain arguments with `gomock.Eq` so the ordering assertion is meaningful:

```go
existing := models.Product{ID: productID, Name: "old-name"}
updated  := models.Product{ID: productID, Name: "new-name"}

gomock.InOrder(
    mockRepo.EXPECT().
        GetByID(gomock.Any(), gomock.Eq(productID)).
        Return(existing, nil),
    mockRepo.EXPECT().
        Update(gomock.Any(), gomock.Eq(models.UpdateProductRequest{ID: productID, Name: "new-name"})).
        Return(updated, nil),
)
```

## Handler Tests — Mount the Production Middleware

Handler tests mount the **same** `chikit.Handler(chikit.WithCanonlog())` middleware the production router uses. That way the canonlog context is set up the same way in tests — headers extracted, logger attached to `r.Context()` — no hand-rolled `canonlog.NewContext` helper needed.

```go
// internal/api/handler_test.go
func setupTestHandler(t *testing.T) (*Handler, *MockProductServiceInterface, config.Config) {
    t.Helper()
    ctrl := gomock.NewController(t)
    mockSvc := NewMockProductServiceInterface(ctrl)

    cfg := config.Config{
        HTTPRequestTimeout:  30 * time.Second,
        MaxRequestBodyBytes: 1024 * 1024,
    }
    handler := NewHandler(mockSvc, nil, cfg)
    return handler, mockSvc, cfg
}

func setupTestRouter(h *Handler, cfg config.Config) chi.Router {
    r := chi.NewRouter()
    r.Use(chikit.Handler(chikit.WithCanonlog()))
    r.Use(chikit.MaxBodySize(int64(cfg.MaxRequestBodyBytes)))
    r.Use(chikit.ExtractHeader("X-Account-ID", "account_id", chikit.ExtractRequired()))
    r.Use(chikit.Binder())

    r.Post  ("/v1/products",        h.CreateProduct)
    r.Get   ("/v1/products/{id}",   h.GetProduct)
    r.Patch ("/v1/products/{id}",   h.UpdateProduct)
    r.Delete("/v1/products/{id}",   h.DeleteProduct)
    return r
}

func TestHandler_CreateProduct(t *testing.T) {
    tests := []struct {
        name       string
        body       any
        headers    map[string]string
        mockSetup  func(*MockProductServiceInterface)
        wantStatus int
    }{
        {
            name:    "successful creation",
            body:    CreateProductRequest{Name: "test", Active: true},
            headers: map[string]string{"X-Account-ID": "acc_test"},
            mockSetup: func(m *MockProductServiceInterface) {
                m.EXPECT().
                    CreateProduct(gomock.Any(), gomock.Any()).
                    Return(models.Product{
                        ID: uuid.MustParse("01903abc-1234-7000-8000-000000000001"),
                        Name: "test", Active: true,
                        CreatedAt: time.Now(), UpdatedAt: time.Now(),
                    }, nil)
            },
            wantStatus: http.StatusCreated,
        },
        {
            name:       "missing X-Account-ID returns 400",
            body:       CreateProductRequest{Name: "test", Active: true},
            headers:    map[string]string{},
            mockSetup:  func(m *MockProductServiceInterface) {},
            wantStatus: http.StatusBadRequest,
        },
        {
            name:       "malformed JSON returns 400",
            body:       json.RawMessage(`{invalid`),
            headers:    map[string]string{"X-Account-ID": "acc_test"},
            mockSetup:  func(m *MockProductServiceInterface) {},
            wantStatus: http.StatusBadRequest,
        },
        {
            name:    "duplicate name returns 409",
            body:    CreateProductRequest{Name: "test", Active: true},
            headers: map[string]string{"X-Account-ID": "acc_test"},
            mockSetup: func(m *MockProductServiceInterface) {
                m.EXPECT().
                    CreateProduct(gomock.Any(), gomock.Any()).
                    Return(models.Product{}, apierrors.ErrProductAlreadyExists)
            },
            wantStatus: http.StatusConflict,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            handler, mockSvc, cfg := setupTestHandler(t)
            tt.mockSetup(mockSvc)
            router := setupTestRouter(handler, cfg)

            body, _ := json.Marshal(tt.body)
            req := httptest.NewRequest(http.MethodPost, "/v1/products", bytes.NewReader(body))
            req.Header.Set("Content-Type", "application/json")
            for k, v := range tt.headers {
                req.Header.Set(k, v)
            }
            rr := httptest.NewRecorder()

            router.ServeHTTP(rr, req)
            assert.Equal(t, tt.wantStatus, rr.Code)
        })
    }
}
```

If you need custom validators in a handler test (anything you registered via `chikit.RegisterValidation`), call `RegisterValidators()` once per test binary — typically in a package-level `init` guarded by `sync.Once`:

```go
var registerOnce sync.Once

func init() {
    registerOnce.Do(func() { RegisterValidators() })
}
```

## Repository Tests — Real DB

Use rolled-back transactions for isolation. Operations within `txCtx` automatically use the transaction via `executorFromContext` — no explicit executor threading:

```go
// internal/repository/product_repository_integration_test.go
func TestProductRepository_CRUD(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx  := context.Background()
    db   := pgxkit.RequireDB(t)
    repo := repository.NewProductRepository(db)

    t.Run("Create and Get", func(t *testing.T) {
        tx, err := db.BeginTx(ctx, pgx.TxOptions{})
        require.NoError(t, err)
        defer tx.Rollback(ctx)

        txCtx     := repository.ContextWithTx(ctx, tx)
        accountID := uuid.New()

        product, err := repo.Create(txCtx, models.CreateProductRequest{
            AccountID: accountID,
            Name:      "integration-test",
            Active:    true,
        })
        require.NoError(t, err)
        assert.NotEmpty(t, product.ID)

        fetched, err := repo.GetByAccountAndID(txCtx, accountID, product.ID)
        require.NoError(t, err)
        assert.Equal(t, product.ID, fetched.ID)
    })

    t.Run("Get missing returns ErrNotFound", func(t *testing.T) {
        _, err := repo.GetByAccountAndID(ctx, uuid.New(), uuid.New())
        require.ErrorIs(t, err, repository.ErrNotFound)
    })
}
```

## Query-Plan Regression — pgxkit Golden Testing

pgxkit's golden testing captures the `EXPLAIN` plan of every query run through a wrapped db handle and compares the set against a stored baseline. Use it on repository methods whose performance is load-bearing (hot paths, reports, anything that would become an N+1 if someone swapped an index or moved a join).

Because repositories take a `*pgxkit.DB` in their constructor and run queries through it via the `Executor` interface, wrapping the db with `EnableGolden` before building the repo captures *everything* the repo method does — no raw SQL in the test:

```go
func TestProductRepository_ListWithFilters_QueryPlan(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx    := context.Background()
    testDB := pgxkit.RequireDB(t)

    // Wrap the db for plan capture.
    goldenDB := testDB.EnableGolden("TestProductRepository_ListWithFilters_QueryPlan")

    // Build the repository over the wrapped db. Every query the repo runs
    // — generated CRUD, custom skimatik queries, all of it — is captured.
    repo := repository.NewProductRepository(goldenDB)

    _, err := repo.ListWithFilters(ctx, models.ListProductsFilter{
        AccountID: uuid.New(),
        Limit:     20,
    })
    require.NoError(t, err)

    // Diff captured plans against testdata/golden/<TestName>.json.
    // First run creates the file; subsequent runs compare.
    goldenDB.AssertGolden(t, "TestProductRepository_ListWithFilters_QueryPlan")
}
```

**Workflow:**
1. First run creates `testdata/golden/TestProductRepository_ListWithFilters_QueryPlan.json`. Commit it with the test.
2. Subsequent runs diff against the baseline — if a migration drops an index or someone rewrites the query, the test fails and shows the plan diff.
3. After an intentional plan change, delete the golden file and re-run to regenerate it, then review and commit the new baseline.

**Limits and caveats:**
- DML operations (the `Create`, `Update`, `Delete` paths the repo exposes) are captured inside a rolled-back transaction so `EXPLAIN` doesn't mutate the DB.
- `EXPLAIN` queries themselves are skipped to avoid recursion.
- Tests using `EnableGolden` can run `t.Parallel()` — each gets its own wrapped handle.
- **Use the `*pgxkit.DB` returned by `EnableGolden` everywhere the repo might touch the db — not `testDB.DB`.** `EnableGolden` builds a *new* `*pgxkit.DB` that shares the underlying read/write pools but owns its own hook chain with the plan-capture hook attached. Queries through `testDB.DB` bypass the hook and are not recorded, so `AssertGolden` will see an empty capture and silently create a blank baseline.

Reach for this on repository methods where an unnoticed plan regression would hurt (paginated list endpoints, joined reports, account-scoped queries over large tables). Skip it on trivial single-row `GetByID` round-trips.

## What Not to Test

- **Generated code** — trust skimatik. If skimatik generates wrong code, that's a bug against skimatik.
- **Framework behavior** — chi routing, viper binding, validator.v10 internals, gomock itself.
- **Trivial accessors / constructors** — skip unless they encode a non-obvious invariant.

## Makefile Targets

```
make test               # go test -v -short ./...  — skips integration
make test-integration   # starts test DB, runs migrations, runs full suite
make test-db-up         # start the test container
make test-db-down       # remove it
make test-db-migrate    # apply migrations to the test DB
```

Run with `-race` when testing concurrent code locally; CI always runs `go test -race ./...`. The `test-integration` Makefile target omits it by default for speed — add it explicitly when you need it.

The test DB runs on a dedicated port (default `15432` in the template Makefile) so it doesn't fight with the dev DB on `5432`. Pick a unique port per service if you run several side-by-side.
