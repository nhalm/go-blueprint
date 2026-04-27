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

E2E tests live in a separate `test/e2e/` directory with shared `setup_test.go` / `helpers_test.go` — one file to spin up the full stack (db, migrations, `httptest.Server`) and one with small request-building helpers consumed by the tests.

## Mocking — gomock

Each layer defines the interfaces it consumes (consumer-owned interfaces — see [ARCHITECTURE.md](ARCHITECTURE.md#consumer-owned-interfaces)). A `//go:generate mockgen` directive points at whichever file declares them.

```go
// internal/service/repository_interface.go
//go:generate mockgen -source=repository_interface.go -destination=repository_interface_mock.go -package=service

package service

type ProductRepository interface {
    Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetByID(ctx context.Context, id string) (*models.Product, error)
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
    db := pgxkit.RequireDB(t)   // reads TEST_DATABASE_URL; t.Skip if unreachable
    defer db.Shutdown(ctx)

    repo := repository.NewProductRepository(db)
    // ... run CRUD assertions ...
}
```

`pgxkit.RequireDB(t)`:
- Reads `TEST_DATABASE_URL`.
- Calls `t.Skip` if the env var is unset or the DB is unreachable — so unit-only runs don't fail.
- Registers shutdown cleanup via `t.Cleanup`.

For test isolation prefer a rolled-back transaction over per-test TRUNCATE:

```go
tx, err := db.BeginTx(ctx, pgx.TxOptions{})
require.NoError(t, err)
defer tx.Rollback(ctx)  // auto-cleanup; safe even after Commit

// Run operations against tx (implements pgxkit.Executor, same as db)
```

When transactions don't fit (DDL tests, multi-connection tests) fall back to `pgxkit.CleanupTestData("TRUNCATE products CASCADE", ...)` in a `t.Cleanup`.

## Service Tests — gomock + testify

Table-driven, one case per row, each case brings its own `mockSetup`:

```go
// internal/service/product_service_test.go
func TestProductService_CreateProduct(t *testing.T) {
    tests := []struct {
        name      string
        req       *models.CreateProductRequest
        mockSetup func(*MockProductRepository)
        wantErr   bool
    }{
        {
            name: "successful creation",
            req:  &models.CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductRepository) {
                m.EXPECT().
                    Create(gomock.Any(), gomock.Any()).
                    Return(&models.Product{ID: "prod_abc", Name: "test"}, nil)
            },
        },
        {
            name: "repository error propagates",
            req:  &models.CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductRepository) {
                m.EXPECT().
                    Create(gomock.Any(), gomock.Any()).
                    Return(nil, errors.New("db down"))
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctrl := gomock.NewController(t)
            mockRepo := NewMockProductRepository(ctrl)
            tt.mockSetup(mockRepo)

            svc := service.NewProductService(mockRepo)
            got, err := svc.CreateProduct(context.Background(), tt.req)

            if tt.wantErr {
                require.Error(t, err)
                assert.Nil(t, got)
                return
            }
            require.NoError(t, err)
            require.NotNil(t, got)
        })
    }
}
```

Use `gomock.InOrder(...)` when a test needs a specific sequence:

```go
gomock.InOrder(
    mockRepo.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(existing, nil),
    mockRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(updated, nil),
)
```

## Handler Tests — Mount the Production Middleware

Handler tests mount the **same** `chikit.Handler(chikit.WithCanonlog())` middleware the production router uses. That way the canonlog context is set up the same way in tests — headers extracted, logger attached to `r.Context()` — no hand-rolled `canonlog.NewContext` helper needed.

```go
// internal/api/handler_test.go
func setupTestHandler(t *testing.T) (*Handler, *MockProductServiceInterface, *gomock.Controller) {
    t.Helper()
    ctrl := gomock.NewController(t)
    mockSvc := NewMockProductServiceInterface(ctrl)

    cfg := config.Config{
        HTTPRequestTimeout:  30 * time.Second,
        MaxRequestBodyBytes: 1024 * 1024,
    }
    handler := NewHandler(mockSvc, nil, cfg)
    return handler, mockSvc, ctrl
}

func setupTestRouter(h *Handler) chi.Router {
    r := chi.NewRouter()
    r.Use(chikit.Handler(chikit.WithCanonlog()))
    r.Use(chikit.ExtractHeader("X-Account-ID", "account_id", chikit.ExtractRequired()))
    r.Use(chikit.Binder())

    r.Post("/v1/products",        h.CreateProduct)
    r.Get ("/v1/products/{id}",   h.GetProduct)
    r.Patch("/v1/products/{id}",  h.UpdateProduct)
    r.Delete("/v1/products/{id}", h.DeleteProduct)
    return r
}

func TestHandler_CreateProduct(t *testing.T) {
    tests := []struct {
        name       string
        body       any
        mockSetup  func(*MockProductServiceInterface)
        wantStatus int
        wantID     string
    }{
        {
            name: "successful creation",
            body: CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductServiceInterface) {
                m.EXPECT().
                    CreateProduct(gomock.Any(), gomock.Any()).
                    Return(&models.Product{
                        ID: "prod_abc", Name: "test", Active: true,
                        CreatedAt: time.Now(), UpdatedAt: time.Now(),
                    }, nil)
            },
            wantStatus: http.StatusCreated,
            wantID:     "prod_abc",
        },
        {
            name: "service NotFound returns 404",
            body: CreateProductRequest{Name: "test", Active: true},
            mockSetup: func(m *MockProductServiceInterface) {
                m.EXPECT().
                    CreateProduct(gomock.Any(), gomock.Any()).
                    Return(nil, apierrors.ErrProductNotFound)
            },
            wantStatus: http.StatusNotFound,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            handler, mockSvc, _ := setupTestHandler(t)
            tt.mockSetup(mockSvc)
            router := setupTestRouter(handler)

            body, _ := json.Marshal(tt.body)
            req := httptest.NewRequest(http.MethodPost, "/v1/products", bytes.NewReader(body))
            req.Header.Set("Content-Type", "application/json")
            req.Header.Set("X-Account-ID", "acc_test")
            rr := httptest.NewRecorder()

            router.ServeHTTP(rr, req)
            assert.Equal(t, tt.wantStatus, rr.Code)

            if tt.wantID != "" {
                var resp ProductResponse
                require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
                assert.Equal(t, tt.wantID, resp.ID)
            }
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

```go
// internal/repository/product_repository_integration_test.go
func TestProductRepository_CRUD(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx := context.Background()
    db := pgxkit.RequireDB(t)
    defer db.Shutdown(ctx)

    repo := repository.NewProductRepository(db)

    t.Run("Create and Get", func(t *testing.T) {
        product, err := repo.Create(ctx, db, generated.CreateProductsParams{
            Name:      "integration-test",
            AccountId: "acc_test",
            Active:    true,
        })
        require.NoError(t, err)
        require.NotEmpty(t, product.Id)

        t.Cleanup(func() { _ = repo.Delete(ctx, db, product.Id) })

        fetched, err := repo.GetByAccountAndID(ctx, "acc_test", product.Id)
        require.NoError(t, err)
        assert.Equal(t, product.Id, fetched.ID)
    })

    t.Run("Get missing returns ErrNotFound", func(t *testing.T) {
        _, err := repo.GetByAccountAndID(ctx, "acc_test", "prod_missing")
        require.Error(t, err)
        assert.ErrorIs(t, err, repository.ErrNotFound)
    })
}
```

## Query-Plan Regression — pgxkit Golden Testing

pgxkit's golden testing captures the `EXPLAIN` plan of every query run through a wrapped db handle and compares the set against a stored baseline. Use it on repository methods whose performance is load-bearing (hot paths, reports, anything that would become an N+1 if someone swapped an index or moved a join).

Because repositories take a `*pgxkit.DB` in their constructor and run queries through it via the `Executor` interface, wrapping the db with `EnableGolden` before building the repo captures *everything* the repo method does — no raw SQL in the test:

```go
func TestProductRepository_ListWithFilters_QueryPlan(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx := context.Background()
    testDB := pgxkit.RequireDB(t)
    defer testDB.Shutdown(ctx)

    // Wrap the db for plan capture.
    goldenDB := testDB.EnableGolden("TestProductRepository_ListWithFilters_QueryPlan")

    // Build the repository over the wrapped db. Every query the repo runs
    // — generated CRUD, custom skimatik queries, all of it — is captured.
    repo := repository.NewProductRepository(goldenDB)

    // Exercise the repository method. Assertions on correctness are a
    // separate test; here we only care that the plans don't regress.
    _, err := repo.ListWithFilters(ctx, models.ListProductsFilter{
        AccountID: "acc_test",
        Limit:     20,
    })
    require.NoError(t, err)

    // Diff captured plans against testdata/golden/<TestName>.json.
    // First run of a test creates the file; subsequent runs compare.
    goldenDB.AssertGolden(t, "TestProductRepository_ListWithFilters_QueryPlan")
}
```

**Workflow:**
1. First run creates `testdata/golden/TestProductRepository_ListWithFilters_QueryPlan.json`. Commit it with the test.
2. Subsequent runs diff against the baseline — if a migration drops an index or someone rewrites the query, the test fails and shows the plan diff.
3. After an intentional plan change, refresh the baseline:
   ```bash
   cp testdata/golden/TestProductRepository_ListWithFilters_QueryPlan.json \
      testdata/golden/TestProductRepository_ListWithFilters_QueryPlan.json.baseline
   ```
   Then review the diff before committing.

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

The test DB runs on a dedicated port (default `15432` in the template Makefile) so it doesn't fight with the dev DB on `5432`. Pick a unique port per service if you run several side-by-side.
