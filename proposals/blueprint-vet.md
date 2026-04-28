# blueprint-vet — Conformance Checker

**Status:** [v0.1.0 published](https://github.com/nhalm/blueprint-vet/releases/tag/v0.1.0) at [github.com/nhalm/blueprint-vet](https://github.com/nhalm/blueprint-vet)
**Owner:** Nick Halm

A design document for a Go static analysis tool that enforces blueprint conformance. Implementation lives at [github.com/nhalm/blueprint-vet](https://github.com/nhalm/blueprint-vet); this doc records the rule rationale and design decisions.

## Implementation Status

| Build-plan step | State |
|-----------------|-------|
| Step 1 — `golangci-lint` config additions (LC-1, LC-2) | Done — see [`templates/.golangci.yml`](../templates/.golangci.yml). LC-3 (layer direction) was reshaped into R-11 instead of shipping as `depguard` — see "Why R-11 absorbed LC-3" below. |
| Step 2 — repo skeleton + first analyzer (`nowriteheader`) | Done. |
| Step 3 — remaining Go analyzers (R-2 through R-8, plus R-11, R-12) | Done — 10 analyzers wired into `cmd/blueprint-vet`'s multichecker. |
| Step 4 — `blueprint-sql-check` binary + R-9 + R-10 | Done — second binary at `cmd/blueprint-sql-check`. |
| Step 5 — `templates/Makefile`, CI workflow, `lefthook.yml` integration | Done in [#5](https://github.com/nhalm/go-blueprint/pull/5). |
| Step 6 — live consumer test | Pending. |

### Why R-11 absorbed LC-3

LC-3 was originally specced as a `depguard` config block. Implementation surfaced that `depguard.pkg` is a **prefix match, not a glob** — so the rule has to spell out concrete module paths (e.g. `myapp/internal/repository`), forcing consumers to either rename `myapp` via `sed` at copy time or hand-edit the YAML. Researching how Google and Uber solve the same problem (Bazel `visibility`) confirmed there is no off-the-shelf module-path-agnostic answer for plain `go build` projects. Since the analyzer infrastructure was already standing, LC-3 became R-11 (`layerdirection`) — same enforcement, no module rename, no third-party tool.

## TL;DR

`blueprint-vet` is a single Go binary that runs ~10 custom static-analysis rules (`go/analysis` framework) against any service built from the go-blueprint patterns. Rules catch silent-but-wrong bugs that compile and pass tests — handlers calling `w.WriteHeader` directly, repository methods skipping `executorFromContext`, model ID fields typed as `string`, and so on.

It ships from a separate repo (`github.com/nhalm/blueprint-vet`) as a tool consumers install via `go install`. The blueprint's `templates/Makefile` adds one line to `install-tools` and one `verify` target. CI runs `make verify` as a required step.

This proposal exists because:

1. Blueprint docs alone don't enforce conformance — a pattern an agent missed in the docs becomes a silent bug.
2. `golangci-lint` catches language-level bugs but can't express domain rules like "every repository method routes through `executorFromContext`."
3. AI-generated code is the primary consumer of this blueprint. Mechanical conformance checks reduce review fatigue and turn recurring agent-error patterns into permanent gates.

It does *not* exist yet. The decision to build is unmade. This doc captures the design space so the decision can be revisited with full context.

## Motivation

### The class of bug this catches

Bugs that compile, pass `go test`, pass `golangci-lint`, and look right at a glance:

| Bug | Why it slips through |
|-----|----------------------|
| Handler writes `w.WriteHeader(400); json.Encode(w, err)` instead of `chikit.SetError(...)` | Compiles. Returns 400. Just doesn't go through the canonical-log path so the request never produces a structured error event. |
| New read query missing `WHERE deleted_at IS NULL` | Returns soft-deleted rows in API responses. Customer notices before code review does. |
| Repository method calls `r.GetProductByAccountAndID(ctx, r.db, ...)` directly | Works fine outside transactions. Silently bypasses the active transaction when called from a service inside `BeginTx`. |
| `ToServiceModel(accountID string)` instead of `(accountID uuid.UUID)` | Compiles. `models.X.AccountID` is now a string. UUID validation deferred to the database, which fails at query time with a less-useful error. |
| New `*_interface.go` file without `//go:generate mockgen` directive | Mocks compile from a stale baseline indefinitely. Tests pass against signatures that no longer exist. |
| `:paginated` query without `ORDER BY` | Generation fails or produces non-deterministic pagination. Sometimes works, sometimes doesn't, depending on Postgres planner mood. |
| `fmt.Println("user created")` instead of `canonlog.InfoAdd` | Log line lands in stdout not Datadog. Noticed only when debugging a production incident. |

These bugs share a property: they are **shaped like the right answer**. Reviewers' eyes pass over them. `go vet` doesn't fire. The only catch is either someone reading carefully, or the bug producing a customer-visible failure.

### Why AI-written code makes this matter more

A human writing service code reads documentation once and internalizes patterns. An AI agent writing service code reads the docs each session and is good but not perfect at internalizing them. Drift compounds across many slices and many agents. Mechanical enforcement converts "the agent has to remember this rule" into "the build fails if the rule is broken" — which is the only durable form.

### Why golangci-lint isn't enough

`golangci-lint` is excellent for language-level rules: unused imports, ineffective assignments, naked returns, suspicious `gocritic` patterns. It cannot express:

- "the bool return of `chikit.JSON` must be checked in an `if !X { return }`"
- "every method on a `*XxxRepository` type must call `executorFromContext` somewhere"
- "model struct fields named `*ID` must be of type `uuid.UUID`"

Some of those *can* be expressed via `forbidigo` (no-symbol rules) or `depguard` (no-import rules), but most genuinely need AST awareness. `golangci-lint` itself supports only the older `.so` plugin model for custom analyzers, which is fragile in practice.

### Why a checklist doc isn't enough

A markdown checklist with 30 rules is performative. Agents don't run prose. Even if they do, they skim past items they think they've already done. The leverage is in *executable* checks that fail builds.

## Design Discussion (Recap)

This section preserves the conversation's reasoning so the decision is reproducible.

### Options considered

| Option | Why rejected (or kept) |
|--------|------------------------|
| Pure prose `VERIFY.md` checklist | Performative. Agents skim; humans skim more. No enforcement. |
| Bash/`rg` scripts under `make verify`, copied into each consumer | Means every consumer ships analyzer source. Drift across repos. Awkward for the maintainer to update rules. |
| `golangci-lint` config additions only (forbidigo + depguard) | **Kept as a complement.** Catches symbol/path/import rules cheaply, no new tool. Fails on AST-shaped rules. |
| Custom analyzers loaded by `golangci-lint` via `.so` plugin | Plugin distribution is brittle. Skipped. |
| Custom analyzers in standalone Go binary (`go/analysis` framework) | **Selected.** Same model as `staticcheck`, `errcheck`, `nilaway`, Uber's internal analyzers. Standard Go tooling distribution via `go install`. |
| `semgrep` / `ast-grep` rule sets | Adds a non-Go tool to the stack. Marginal benefit. Skipped. |

### Why a separate repo (not in blueprint)

The blueprint repo is "self-contained docs; every pattern is inline. No external pointers, no Go skeleton under templates/." Adding a real Go module (`cmd/blueprint-vet/` + `internal/analysis/`) at the blueprint repo's root violates that frame. It also entangles versioning: a blueprint-prose fix shouldn't bump the analyzer's version.

A separate repo (`nhalm/blueprint-vet`) keeps each concern clean:

- Blueprint = docs and copy-ready non-code scaffolding
- blueprint-vet = the tool that enforces blueprint patterns

This matches how every other Go analyzer in the ecosystem ships (`errcheck`, `staticcheck`, `gosec`, `nilaway`, `wire`, `mockgen` — all separate repos).

### Why not "just write something in each application"

Earlier sketch had `cmd/blueprint-vet/` and `internal/analysis/` lifted as templates and copied into each consuming service. That was wrong:

- Every consumer ends up with analyzer source code
- Rule updates require manual sync across N services
- No clean version pinning
- Consumers without an opinion on the tool still ship its full source

Correct shape: the analyzer lives in *one* place. Consumers depend on the binary the same way they depend on `mockgen` or `skimatik`.

## Rule Taxonomy

Two tiers, treated differently:

### Hard rules

Always true. No legitimate exceptions. Failure is a bug.

Examples:
- `chikit.JSON`'s bool return must be handled
- UUID PK columns must have no `DEFAULT`
- `:paginated` queries must have `ORDER BY`
- Repository methods must wrap returns through `translateError`

These produce **build failures**. CI step exits non-zero. PR can't merge.

### Default rules

True ~95% of the time. Legitimate opt-outs exist and need a clear way to signal intent.

Examples:
- Read queries filter `deleted_at IS NULL` (opt-out: queries named `*IncludingDeleted`, `*Audit`, `*Trash`, `*AllVersions`)
- Service constructors are minimal (opt-out: explicit `NewXxxService(repo, tx, cfg)` when used)
- Domain methods take/return values not pointers (opt-out: rare, document inline)

For rule-builder purposes, default rules are encoded with the opt-out as part of the rule. The check considers the opt-out signal (typically a naming convention) and skips matched cases. **In v1, all enforced rules are encoded as hard rules with an explicit opt-out built into the check.** Pure-judgment rules ("constructor minimalism") stay as prose in the relevant blueprint doc — not in `blueprint-vet`.

### Why the distinction matters

Treating every rule as hard produces a tool that flags too much and gets disabled. Treating every rule as advisory produces a tool nobody trusts. The tier the rule lives in is a deliberate design call per rule, made when the rule is added.

## Tool Selection Matrix

Each rule shape gets the right tool. Mixing them is normal.

| Rule shape | Right tool | Lives in |
|------------|------------|----------|
| "Symbol X must not appear in path Y" | `forbidigo` | `templates/.golangci.yml` |
| "Package A may not import package B" | `depguard` | `templates/.golangci.yml` |
| "AST-shaped Go pattern" | Custom `analysis.Analyzer` | `nhalm/blueprint-vet` |
| ".sql file content matches Z" | Small Go program walking files | `nhalm/blueprint-vet` (separate binary) |
| "Pure judgment / architectural" | Prose in blueprint doc | `go-blueprint/*.md` |

## Starter Rule Set

Eight Go-AST rules, two SQL rules, plus three `golangci-lint` config additions. Numbered for stable reference.

### Custom Go analyzers (live in `blueprint-vet`)

#### R-1 `nowriteheader` — Handlers don't call `WriteHeader` directly

**Statement:** Code under `internal/api/` may not call any method named `WriteHeader` on an `http.ResponseWriter`.

**Why:** Handlers must use `chikit.SetResponse` / `chikit.SetError`. The `chikit.Handler` middleware owns deferred response writing — calling `WriteHeader` directly bypasses canonical logging, error envelope formatting, and the `Set*` mutex protection.

**Detection:** Walk `*ast.CallExpr` nodes whose `Fun` is a `*ast.SelectorExpr` with `Sel.Name == "WriteHeader"`. Filter to packages whose import path contains `/internal/api`. Report position with message pointing at `chikit.SetResponse` / `chikit.SetError`.

**Tier:** Hard.

#### R-2 `nojsonencode` — Handlers don't encode response bodies directly

**Statement:** Code under `internal/api/` may not call `json.NewEncoder(w).Encode(...)` against the `http.ResponseWriter`.

**Why:** Same reason as R-1 — `chikit.SetResponse` owns serialization plus content-type plus status-code in one place. Direct encoding produces inconsistent error envelopes and skips canonlog.

**Detection:** Walk for `json.NewEncoder(...)` followed by `.Encode(...)` where the argument flows from an `http.ResponseWriter` parameter of the enclosing function. Filter to `/internal/api`. (Type-info-aware via `pass.TypesInfo`.)

**Tier:** Hard.

#### R-3 `handlerjsonbool` — `chikit.JSON` / `chikit.Query` results are checked

**Statement:** Calls to `chikit.JSON(r, &dst)` and `chikit.Query(r, &dst)` must be the condition of an `if !X { return }` (or equivalent guard pattern).

**Why:** The bool return signals whether validation/binding failed. If the call result is discarded, validation errors silently set the response *but the handler keeps running* — potentially calling the service with a zero-value request struct, producing nonsense behavior.

**Detection:** Find `*ast.CallExpr` calling `chikit.JSON` or `chikit.Query`. Walk the parent: if the parent isn't an `*ast.IfStmt` whose condition is a unary `!` of the call, or an `*ast.IfStmt` whose body returns/branches, flag.

**Tier:** Hard.

#### R-4 `mockgendirective` — `*_interface.go` files have a `mockgen` directive

**Statement:** Any file whose base name matches `*_interface.go` must contain a `//go:generate mockgen` comment line.

**Why:** Without the directive, `go generate ./...` doesn't regenerate the mock when the interface changes. Tests then pass against a stale interface signature indefinitely. This is the worst kind of false-green CI.

**Detection:** Walk all parsed files. For each file whose `pass.Fset.File(file.Pos()).Name()` matches `*_interface.go`, scan `file.Comments` for any `//go:generate mockgen` line. Report at `file.Package` if absent.

**Tier:** Hard.

#### R-5 `repoexecutor` — Repository methods route through `executorFromContext`

**Statement:** Methods on a `*XxxRepository` type whose body calls a generated method (any method on an embedded `*generated.XxxRepository` or `*generated.XxxQueries`) must pass `executorFromContext(ctx, r.db)` (or a comparable executor expression) — not `r.db` directly.

**Why:** Generated methods take an `Executor`. Passing `r.db` works outside transactions but bypasses the active `*pgxkit.Tx` when the caller is inside `BeginTx`. The blueprint's `executorFromContext` resolves the right executor from context. Forgetting it is the classic "transactional service silently doesn't transact" bug.

**Detection:** Find methods whose receiver type name ends in `Repository` and is defined in a package importing `github.com/.../internal/repository/generated`. For each call to a method on the embedded generated repo struct, inspect the second argument. If it's a selector like `r.db` or `xxx.db` (not a `Call` to `executorFromContext`), flag.

**Tier:** Hard.

**Heuristic limits:** This is the most complex rule. Generated method detection is by package path; the second-argument check is structural. False positives possible if the consumer uses a wrapper. Document the escape hatch (a `// nolint:repoexecutor` line) and a small allow-list config option.

#### R-6 `idtypeuuid` — Model ID fields are `uuid.UUID`

**Statement:** Struct fields under `internal/models/` whose name ends in `ID` (or *is* `ID`) must have type `uuid.UUID` or `*uuid.UUID`. String IDs are forbidden in `internal/models`.

**Why:** The blueprint's ID strategy is "internal `uuid.UUID`, wire `string`." Domain models that hold IDs as strings short-circuit the boundary, push UUID parsing into queries, and produce worse error messages on bad input. The bug is invisible until something deeper bites.

**Detection:** Walk type declarations in packages whose path is `*/internal/models`. For each `*ast.StructType`, inspect fields. For names matching `ID$` or `^ID$`, check the field type via `pass.TypesInfo.Types[field.Type]`. If not `github.com/google/uuid.UUID` (or pointer thereto), flag.

**Tier:** Hard.

#### R-7 `apperroralias` — `internal/errors` is imported as `apperrors`

**Statement:** Imports of `<module>/internal/errors` must use the alias `apperrors`. Bare imports (`internal/errors` shadowing stdlib `errors`) are forbidden; other aliases are forbidden for blueprint consistency.

**Why:** The package name `errors` collides with stdlib. The blueprint codified `apperrors` as the alias. Other aliases (`apierrors`, `domerr`, `myerrors`) work but produce drift across files in the same codebase, which slows reading.

**Detection:** Walk file-level imports. For any import path matching `*/internal/errors`, check the import name. If absent (using package name `errors`) or any value other than `apperrors`, flag.

**Tier:** Hard.

#### R-8 `nofmtprint` — No `fmt.Print*` outside whitelisted paths

**Statement:** Calls to `fmt.Print`, `fmt.Println`, `fmt.Printf`, `fmt.Fprint*`, `fmt.Sprint*` are allowed only in:
- `cmd/<app>/` (CLI feedback)
- `internal/config/` (pre-canonlog config-load errors)

**Why:** Runtime application logging must go through canonlog. `fmt.Print*` lands on stdout, bypassing Datadog. The two whitelisted paths are documented exceptions (CLI commands print human-facing summaries after the canonlog event; config loaders run before canonlog is set up).

**Detection:** Walk `*ast.CallExpr` for `fmt.Print*` / `fmt.Fprint*` / `fmt.Sprint*` (selector on `fmt`). Filter package path: skip if matches `/cmd/` or `/internal/config`. Otherwise flag.

**Tier:** Hard.

**Note:** This is borderline expressible via `forbidigo`. A `forbidigo` rule with `--exclude` paths would work. Decide at build time whether to ship as analyzer (richer message, scope-aware) or `forbidigo` config (zero code).

### SQL file rules (separate binary in `blueprint-vet`)

Lives in a second binary because `go/analysis` framework only sees Go source. SQL checker is a small Go program that walks `*.sql` files and applies regex/parsing rules.

Binary name: `blueprint-sql-check`. Run as `blueprint-sql-check internal/repository/queries/`.

#### R-9 `softdelete` — Read queries filter soft-deleted rows by default

**Statement:** SQL queries annotated `:one`, `:many`, or `:paginated` must include `deleted_at IS NULL` in their `WHERE` clause **unless** the query name matches an opt-out pattern: `*IncludingDeleted`, `*Audit`, `*Trash`, `*AllVersions`.

**Why:** Returning soft-deleted rows in API responses is a customer-visible silent bug. Including them on purpose is fine — admin views, recovery flows, audit/compliance reads all need it. The naming convention separates intent from accident.

**Detection:**
1. Parse each `.sql` file for `-- name: <Name> :<type>` annotations and the SQL block following.
2. For each block where type is `one`/`many`/`paginated`, check for `deleted_at` token in the SQL.
3. If absent, check whether `<Name>` matches `(?i).*(IncludingDeleted|Audit|Trash|AllVersions)$`. If yes, skip (intentional opt-out). If no, flag.
4. Also skip if the underlying table doesn't have a `deleted_at` column — but detecting that requires a schema-aware check. **v1: skip the schema check; trust naming convention. Document that tables without `deleted_at` need manual exception via comment.**

**Tier:** Hard (with explicit naming opt-out).

**Open question for build time:** how to handle tables without `deleted_at` columns. Two options: per-query `-- skip-softdelete-check` comment, or a config file in the consumer listing tables that are exempt.

#### R-10 `paginatedorderby` — `:paginated` queries have `ORDER BY`

**Statement:** Every query annotated `:paginated` must contain an `ORDER BY` clause.

**Why:** skimatik's `:paginated` requires `ORDER BY` to construct the cursor. Generation fails or produces non-deterministic pagination otherwise. Catching this at lint time fails fast instead of failing at codegen time.

**Detection:** For each SQL block whose type is `paginated`, search for `ORDER BY` (case-insensitive). If absent, flag.

**Tier:** Hard.

### `golangci-lint` config additions (live in `templates/.golangci.yml`)

Ship with the blueprint, no new tool needed. Three rules, all expressible declaratively.

#### LC-1 — Forbid `*pgxkit.DB.Ping`

```yaml
linters-settings:
  forbidigo:
    forbid:
      - p: '\.Ping\('
        pkg: 'github.com/nhalm/pgxkit/v2'
        msg: '*pgxkit.DB does not have Ping; use HealthCheck or IsReady'
```

**Why:** This was a real bug in our docs. `*pgxkit.DB` exposes `HealthCheck` / `IsReady`, not `Ping`. (Redis clients do have `Ping`, so the rule must scope to pgxkit-typed receivers, which `forbidigo` can do via the `pkg:` qualifier.)

#### LC-2 — Forbid `canonlog.AddRequestFields`

```yaml
linters-settings:
  forbidigo:
    forbid:
      - p: 'canonlog\.AddRequestFields'
        msg: 'use canonlog.InfoAdd(ctx, key, value) or canonlog.InfoAddMany(ctx, fields)'
```

**Why:** `AddRequestFields` does not exist in canonlog (we found this bug during research). If any consumer tries it because of stale docs or training data, this catches it immediately.

#### LC-3 — Layer import direction

```yaml
linters-settings:
  depguard:
    rules:
      models-no-internal:
        list-mode: lax
        files: ['**/internal/models/**']
        deny:
          - pkg: '**/internal/repository'
            desc: 'models cannot import repository'
          - pkg: '**/internal/service'
            desc: 'models cannot import service'
          - pkg: '**/internal/api'
            desc: 'models cannot import api'
      api-no-repository:
        list-mode: lax
        files: ['**/internal/api/**']
        deny:
          - pkg: '**/internal/repository'
            desc: 'api cannot import repository directly; consume via service interface'
```

**Why:** Layer direction is non-negotiable in the blueprint. The blueprint repos already document this; depguard makes it mechanical.

## Architecture

### Repository layout (proposed)

New repo: **`github.com/nhalm/blueprint-vet`**

```
blueprint-vet/
├── README.md
├── LICENSE
├── go.mod                                   # module github.com/nhalm/blueprint-vet
├── go.sum
├── Makefile
├── .github/workflows/ci.yml
├── cmd/
│   ├── blueprint-vet/
│   │   └── main.go                          # multichecker.Main(...) — Go-AST analyzers
│   └── blueprint-sql-check/
│       └── main.go                          # walks *.sql files, applies SQL rules
├── internal/
│   └── analysis/
│       ├── nowriteheader/
│       │   ├── nowriteheader.go             # ~80 lines
│       │   ├── nowriteheader_test.go
│       │   └── testdata/src/example/example.go
│       ├── nojsonencode/...
│       ├── handlerjsonbool/...
│       ├── mockgendirective/...
│       ├── repoexecutor/...
│       ├── idtypeuuid/...
│       ├── apperroralias/...
│       └── nofmtprint/...
└── internal/
    └── sqlcheck/
        ├── sqlcheck.go                      # parses *.sql, dispatches rules
        ├── sqlcheck_test.go
        └── rules/
            ├── softdelete.go
            └── paginatedorderby.go
```

Each analyzer package follows the standard `go/analysis` shape:

- Exports a single `Analyzer` variable of type `*analysis.Analyzer`
- Test file uses `analysistest.Run(t, analysistest.TestData(), Analyzer, "example")`
- Testdata has positive and negative cases annotated with `// want "expected message"`

### `cmd/blueprint-vet/main.go` (sketch)

```go
package main

import (
    "golang.org/x/tools/go/analysis/multichecker"

    "github.com/nhalm/blueprint-vet/internal/analysis/apperroralias"
    "github.com/nhalm/blueprint-vet/internal/analysis/handlerjsonbool"
    "github.com/nhalm/blueprint-vet/internal/analysis/idtypeuuid"
    "github.com/nhalm/blueprint-vet/internal/analysis/mockgendirective"
    "github.com/nhalm/blueprint-vet/internal/analysis/nofmtprint"
    "github.com/nhalm/blueprint-vet/internal/analysis/nojsonencode"
    "github.com/nhalm/blueprint-vet/internal/analysis/nowriteheader"
    "github.com/nhalm/blueprint-vet/internal/analysis/repoexecutor"
)

func main() {
    multichecker.Main(
        apperroralias.Analyzer,
        handlerjsonbool.Analyzer,
        idtypeuuid.Analyzer,
        mockgendirective.Analyzer,
        nofmtprint.Analyzer,
        nojsonencode.Analyzer,
        nowriteheader.Analyzer,
        repoexecutor.Analyzer,
    )
}
```

### Sample analyzer (full code) — `nowriteheader`

```go
// internal/analysis/nowriteheader/nowriteheader.go
package nowriteheader

import (
    "go/ast"
    "strings"

    "golang.org/x/tools/go/analysis"
    "golang.org/x/tools/go/analysis/passes/inspect"
    "golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
    Name: "nowriteheader",
    Doc: `Disallow direct WriteHeader calls in internal/api.

Handlers must use chikit.SetResponse or chikit.SetError. The chikit.Handler
middleware owns deferred response writing — calling WriteHeader directly
bypasses canonical logging and the response-state mutex.`,
    Run:      run,
    Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
    if !strings.Contains(pass.Pkg.Path(), "/internal/api") {
        return nil, nil
    }

    insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
    insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
        call := n.(*ast.CallExpr)
        sel, ok := call.Fun.(*ast.SelectorExpr)
        if !ok || sel.Sel.Name != "WriteHeader" {
            return
        }
        pass.Reportf(call.Pos(),
            "use chikit.SetResponse or chikit.SetError; chikit.Handler middleware owns response writing")
    })
    return nil, nil
}
```

```go
// internal/analysis/nowriteheader/nowriteheader_test.go
package nowriteheader_test

import (
    "testing"

    "golang.org/x/tools/go/analysis/analysistest"
    "github.com/nhalm/blueprint-vet/internal/analysis/nowriteheader"
)

func TestAnalyzer(t *testing.T) {
    analysistest.Run(t, analysistest.TestData(), nowriteheader.Analyzer, "example")
}
```

```go
// internal/analysis/nowriteheader/testdata/src/example/example.go
package example

import "net/http"

func BadHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusBadRequest) // want `use chikit.SetResponse or chikit.SetError`
}

func GoodHandler(w http.ResponseWriter, r *http.Request) {
    // No direct WriteHeader call — would use chikit.SetResponse(r, ...)
    _ = w
    _ = r
}
```

The `// want` annotation is the golden assertion — `analysistest` parses it, runs the analyzer, and verifies the message and position match.

### `cmd/blueprint-sql-check/main.go` (sketch)

```go
package main

import (
    "fmt"
    "os"

    "github.com/nhalm/blueprint-vet/internal/sqlcheck"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: blueprint-sql-check <dir-of-.sql-files>...")
        os.Exit(2)
    }
    failed := false
    for _, dir := range os.Args[1:] {
        if errs := sqlcheck.Run(dir); len(errs) > 0 {
            for _, e := range errs {
                fmt.Println(e)
            }
            failed = true
        }
    }
    if failed {
        os.Exit(1)
    }
}
```

`sqlcheck.Run` parses each file's annotations, applies each rule, returns a slice of human-readable findings (`file:line: rule: message`).

## Distribution & Consumer Integration

What changes in the blueprint's `templates/` and `README.md`.

### `templates/Makefile` additions

```makefile
install-tools:
	@go install github.com/nhalm/skimatik/cmd/skimatik@latest
	@go install github.com/swaggo/swag/cmd/swag@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install go.uber.org/mock/mockgen@latest
	@go install golang.org/x/tools/cmd/goimports@latest
	@go install github.com/evilmartians/lefthook@latest
	@go install github.com/nhalm/blueprint-vet/cmd/blueprint-vet@latest          # ← new
	@go install github.com/nhalm/blueprint-vet/cmd/blueprint-sql-check@latest    # ← new

verify:                                                                           # ← new target
	@blueprint-vet ./...
	@blueprint-sql-check ./internal/repository/queries
```

### `templates/.github/workflows/ci.yml` additions

```yaml
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true
      - name: Install blueprint-vet
        run: |
          go install github.com/nhalm/blueprint-vet/cmd/blueprint-vet@latest
          go install github.com/nhalm/blueprint-vet/cmd/blueprint-sql-check@latest
      - name: Run blueprint-vet
        run: blueprint-vet ./...
      - name: Run blueprint-sql-check
        run: blueprint-sql-check ./internal/repository/queries
```

### `templates/.golangci.yml` additions

`forbidigo` and `depguard` config additions per LC-1, LC-2, LC-3 above.

### `templates/lefthook.yml` (optional)

Add `blueprint-vet` to pre-commit if speed allows (these analyzers run in well under a second on small codebases):

```yaml
pre-commit:
  parallel: true
  jobs:
    - name: fmt
      run: make fmt
    - name: lint
      run: make lint
    - name: verify       # ← new
      run: make verify
    - name: test
      run: make test
```

### Consumer-facing impact

A consumer of the blueprint adds *no source code* for the analyzer. They get:

- Two more lines in `install-tools` (already a list of `go install` lines)
- One new `verify` Makefile target
- One new CI step
- A few config additions to `.golangci.yml`

That's it. Updating to a new rule version is `go install ...@latest`. Pinning is `go install ...@v0.3.1`. Standard Go tooling.

## Where It Runs

| Layer | What runs | Required? |
|-------|-----------|-----------|
| **CI (GitHub Actions)** | `make verify` (analyzers + SQL checker) + `make lint` (golangci-lint with new rules) | Yes — gates merge |
| **Pre-commit (lefthook)** | `make verify` if it stays under ~1s; `make lint` always | Optional but recommended; bypassable with `--no-verify` |
| **Pre-push** | Nothing analyzer-related | Skip — redundant with pre-commit and CI |

## Pending Blueprint Doc Changes

1. ~~**DATABASE.md** — soften the soft-delete prose.~~ Already in place at DATABASE.md:35 with all four opt-out conventions documented; the proposal's reference was stale.

2. ~~**README.md docs table** — add `proposals/` row.~~ Done.

3. **Templates integration** (Step 5) — pending extraction. Once `blueprint-vet` lives at `github.com/nhalm/blueprint-vet`, add `go install` lines to `templates/Makefile`'s `install-tools`, a `verify` target, a `verify` step to `templates/.github/workflows/ci.yml` (file does not exist yet), and an optional `verify` job in `templates/lefthook.yml`.

## Open Questions (Resolve Before Building)

1. **Repo name.** `blueprint-vet` is descriptive but generic. Alternatives: `go-blueprint-vet`, `blueprintkit`, `blueprintlint`. The `vet` suffix is conventional in the Go ecosystem (`shadow`, `errcheck`, `nilaway`). Recommendation: `blueprint-vet`.

2. **License.** Same as the other `nhalm/*` repos? (Check existing). Likely MIT.

3. **Versioning model.** SemVer with `v0.x` while rules are unstable (allows breaking message changes between minors). Bump to `v1.0.0` when rule set is settled.

4. **Schema-aware soft-delete check (R-9).** Tables without a `deleted_at` column shouldn't be flagged. v1 trusts the naming convention; consumers can add `// blueprint-vet:skip softdelete` comments per query as a fallback. Alternative: a `blueprint-vet.yaml` config in the consumer listing exempt tables. Decide before R-9 ships.

5. **`repoexecutor` analyzer (R-5) heuristics.** Most complex analyzer. False positives possible if consumer wraps generated calls. Document the `// nolint:repoexecutor` escape hatch and an allow-list config option. Decide config shape during build.

6. **`nofmtprint` (R-8): analyzer or `forbidigo` rule?** The analyzer gives a richer message and explicit scope. `forbidigo` does it in YAML config. Recommendation: ship as `forbidigo` rule in templates/.golangci.yml; don't write an analyzer. (Reduces analyzer count to 7.)

7. **Should consumers be able to opt out of individual rules?** Most analysis tools support `// nolint:rulename` line directives via golangci-lint integration. blueprint-vet running standalone doesn't honor those. Decide: support `// blueprint-vet:disable rulename` directives? Or rely on golangci-lint integration once that's added?

8. **`golangci-lint` integration vs standalone.** Long-term, packaging `blueprint-vet` rules as a `golangci-lint` plugin is nicer for consumers (one tool, one config). The plugin model has historically been painful (Go plugins are fragile). v1: ship standalone. Re-evaluate after `golangci-lint` v2's improved plugin story.

## Build Plan

When ready to execute, this is the order. Each step ships independently — partial completion is still useful.

### Step 1 — `golangci-lint` config additions only (zero new tool)

Lowest cost, highest value-per-hour. Ship LC-1 (no `pgxkit.DB.Ping`), LC-2 (no `canonlog.AddRequestFields`), LC-3 (layer imports) as additions to `templates/.golangci.yml`. Verify they fire on intentionally-broken sample code. Update DEVOPS.md.

This alone covers ~25% of the gap, with ~no maintenance burden.

### Step 2 — `blueprint-vet` repo with one analyzer (`nowriteheader`)

Set up the repo skeleton, multichecker, one working analyzer with full tests. Tag `v0.1.0`. Update blueprint `templates/Makefile` to install + run.

This proves the framework end-to-end. If anything is going to be hard, it shows up here.

### Step 3 — Add remaining seven Go analyzers, one at a time

Each analyzer is its own package. Each ships with tests and testdata. Tag a new release per batch (or per analyzer if cautious).

### Step 4 — `blueprint-sql-check` binary + R-9 + R-10

Separate binary in the same repo. Different entry point, doesn't share infrastructure with the Go analyzers.

### Step 5 — Document + integrate

Update blueprint `templates/Makefile`, `templates/.github/workflows/ci.yml`, `templates/lefthook.yml`. Update DATABASE.md soft-delete prose. Add row to README.md docs table linking to LIBRARIES.md and noting that `blueprint-vet` enforces these patterns.

### Step 6 — Live test on a real consumer

Pick a real service built from the blueprint (or scaffold a fresh one from templates). Run `make verify`. Fix anything that surfaces. This is where heuristic limits in R-5 / R-9 become visible.

## References

- **`go/analysis` framework:** https://pkg.go.dev/golang.org/x/tools/go/analysis — the standard Go static-analysis interface. `Analyzer` type, `Pass`, `Reportf`.
- **`analysistest`:** https://pkg.go.dev/golang.org/x/tools/go/analysis/analysistest — golden-test helper for analyzers.
- **`multichecker`:** https://pkg.go.dev/golang.org/x/tools/go/analysis/multichecker — composes multiple analyzers into one binary.
- **Uber's `nilaway`:** https://github.com/uber-go/nilaway — large real-world example of the same pattern.
- **`forbidigo`:** https://github.com/ashanbrown/forbidigo — `golangci-lint` linter for forbidden patterns.
- **`depguard`:** https://github.com/OpenPeeDeeP/depguard — `golangci-lint` linter for import direction.
- **`go-blueprint` doc:** [LIBRARIES.md](../LIBRARIES.md) — verified library surfaces these rules check against.
- **`go-blueprint` doc:** [EXAMPLE.md](../EXAMPLE.md) — canonical Products slice; the conformance target.

## Reading This Doc Cold

If you (or a future agent) come back to this without context:

1. **Skim TL;DR + Motivation** to recover the "why."
2. **Read Starter Rule Set** to see the concrete rules.
3. **Read Architecture + Sample Analyzer** to see what the work looks like.
4. **Read Open Questions** before starting — those are the un-resolved decisions.
5. **Follow Build Plan** in order; each step is independently shippable.

The decision still to make: **does the cost justify it?** Two signals to look for:

- **Yes, build:** specific blueprint-conformance bugs keep recurring across services or across agent sessions. Reviewer time is being spent on the same patterns repeatedly.
- **No, skip:** the `golangci-lint` config additions (Step 1) cover what actually drifts. Custom analyzers would be over-engineering for a small consumer count.

The lightweight Step-1 version is worth doing under almost any scenario. The full Step 2-6 build is worth doing only if Step 1 leaves real drift on the table.
