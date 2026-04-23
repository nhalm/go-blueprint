# Testing

How tests are structured, how mocks are generated, and how to run them.

## Layer Strategy

| Layer | Test type | DB | Mocked dependency |
|-------|-----------|-----|-------------------|
| `internal/repository` | Integration | Real Postgres via `pgxkit.RequireDB(t)` | None |
| `internal/service`    | Unit | None | `MockProductRepository` (gomock) |
| `internal/api`        | Unit | None | `MockProductService` (gomock) |
| `test/e2e/` (optional) | E2E | Real Postgres + `httptest.Server` | None |

Repository tests prove the SQL works. Service and handler tests prove the business + transport logic works without booting a DB. E2E tests cover the wiring.

## Layout & Naming

- Tests live alongside the code they test, in the **same package** (`*_test.go`).
- Repository integration tests use the `_integration_test.go` suffix and gate with `testing.Short()` so unit-only runs (`go test -short`) skip them:

  ```go
  func TestProductRepository_Integration(t *testing.T) {
      if testing.Short() {
          t.Skip("skipping integration test")
      }
      // ...
  }
  ```
- E2E tests live in a separate top-level `test/e2e/` directory, with shared `setup_test.go` / `helpers_test.go`.

## Mocking — gomock + consumer-owned interfaces

Each layer owns the interfaces it consumes in an `interfaces.go` file. The `//go:generate` directive lives at the top of that file:

```go
// internal/service/interfaces.go
package service

//go:generate mockgen -source=interfaces.go -destination=mock_interfaces_test.go -package=service

type ProductRepository interface {
    Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
    GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
    // ...
}
```

Run `go generate ./...` (or `make generate`) to (re)produce `mock_interfaces_test.go`. Mock files live in the same package as `_test.go` files and never ship in production binaries.

For tiny single-method dependencies (key providers, audit hooks), prefer a hand-written struct mock — generation is overkill:

```go
type stubKeyProvider struct{ key []byte }

func (s *stubKeyProvider) GetCipherKey(ctx context.Context, ref string) ([]byte, error) {
    return s.key, nil
}
```

## Test Database — `pgxkit.RequireDB`

Don't hand-roll a `GetTestDB` helper. pgxkit ships one:

```go
func TestSomething(t *testing.T) {
    if testing.Short() { t.Skip("skipping integration test") }

    ctx := context.Background()
    db := pgxkit.RequireDB(t)   // skips test if TEST_DATABASE_URL not set
    defer db.Shutdown(ctx)

    // ...
}
```

`pgxkit.RequireDB(t)` reads `TEST_DATABASE_URL`, calls `t.Skip` if it can't connect, and registers cleanup. For test isolation, prefer transaction-scoped tests:

```go
tx, err := db.BeginTx(ctx, pgx.TxOptions{})
require.NoError(t, err)
defer tx.Rollback(ctx)   // automatic cleanup, safe after commit

// run test against tx (it implements pgxkit.Executor)
```

When transactions don't fit (e.g., schema-altering tests), use `t.Cleanup` with explicit deletes.

## Assertions — testify

Use `github.com/stretchr/testify/require` for fail-fast checks (the test can't proceed) and `assert` for soft checks (let the rest of the test run):

```go
require.NoError(t, err)              // can't continue if this failed
assert.Equal(t, "prod_abc", got.ID)  // record failure but keep going
```

Don't use bare `t.Errorf` / `t.Fatalf` for assertions in new code.

## Table-Driven Tests

Standard shape — each case carries its inputs, expected outputs, and a `mockSetup` function:

```go
tests := []struct {
    name       string
    body       CreateProductRequest
    mockSetup  func(*MockProductService)
    wantStatus int
    wantID     string
}{
    {
        name: "successful creation",
        body: CreateProductRequest{Name: "test", Active: true},
        mockSetup: func(m *MockProductService) {
            m.EXPECT().CreateProduct(gomock.Any(), gomock.Any()).
                Return(&models.Product{ID: "prod_abc", Name: "test"}, nil)
        },
        wantStatus: http.StatusCreated,
        wantID:     "prod_abc",
    },
    // ...
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        _, mockSvc, router := setupTestHandler(t)
        tt.mockSetup(mockSvc)
        // ...
    })
}
```

## Running Tests

```bash
make test               # unit only — go test -v -short ./...
make test-integration   # full suite — spins a docker postgres on port 15432, runs migrations, then go test -v ./...
make test-db-up         # just start the test container
make test-db-down       # remove it
```

`make test-integration` is the gate — it boots a dedicated `myapp_test_db` container on port `15432` (separate from the dev DB on `5432`), sets `TEST_DATABASE_URL`, and runs every test including integration ones.

`pgxkit.RequireDB(t)` calls `t.Skip` when `TEST_DATABASE_URL` isn't set, so a plain `go test ./...` outside the Makefile won't fail — it just skips the integration tests.

## Reference Tests

Working examples in this blueprint:

| File | Demonstrates |
|------|--------------|
| `templates/internal/repository/product_repository_integration_test.go` | Real DB CRUD, `pgxkit.RequireDB`, `t.Cleanup`, NotFound assertion |
| `templates/internal/service/product_service_test.go` | Table-driven service unit test with gomock, `gomock.InOrder` for sequenced calls |
| `templates/internal/api/handler_test.go` | Handler unit test with chi router, canonlog context injection, JSON response decoding |

Copy these as starting points when adding tests for a new entity.
