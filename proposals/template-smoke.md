# Template Smoke Testing

**Status:** design draft, not started
**Owner:** TBD
**Decision required before building:** EXAMPLE.md extraction vs parallel reference app (Section 6); Tier 2 vs Tier 3 scope

A design document for smoke-testing the blueprint's `templates/` directory — proving that a fresh service scaffolded from the templates actually compiles, lints, and runs the documented daily loop. Includes the unexpected leverage point: the smoke-test fixture is mostly EXAMPLE.md materialized as files, so the smoke test machine-verifies that EXAMPLE.md is executable, not just plausible-looking.

## TL;DR

Build a CI workflow in the blueprint repo that:

1. Extracts each fenced code block in `EXAMPLE.md` (annotated with a `{file=...}` marker) to its declared path in a temporary directory
2. Merges the extracted slice with `templates/*` (substituting the placeholder module/binary name) and a small set of un-doc'd fixtures (cmd entry, migration file, go.mod scaffold)
3. Runs `go build ./...`, `golangci-lint run`, `make migrate-up && make generate && make test-integration` against the temp directory
4. Cleans up

The blueprint repo's CI fails when any of those steps break. Drift in templates, EXAMPLE.md, or the libraries the blueprint depends on is caught the first time the smoke runs after the change.

This proposal exists because:

1. The blueprint ships executable scaffolding (Makefile, docker-compose, skimatik.yaml, CI workflow). Those are real artifacts that can break.
2. Library churn (`pgxkit`, `chikit`, `canonlog`, `skimatik`) silently invalidates the patterns in `EXAMPLE.md` between blueprint releases.
3. EXAMPLE.md is the canonical Products slice, but currently nothing proves its code blocks compile against current libraries. We've already shipped one round of bug fixes (cursor format, `Ping` vs `HealthCheck`, missing `error` returns) that a smoke test would have caught at PR time.

It does not exist yet. The decision to build is unmade. This doc captures the design space so the decision can be revisited with full context.

## Motivation

### What can drift in `templates/`

Templates ship eight files plus a workflows subdir, all hand-written:

| File | Drift modes |
|------|-------------|
| `Makefile` | Target order changes, path drift (e.g. `internal/database/migrations` rename), tool flag deprecations |
| `docker-compose.yml` | Image tag changes (`postgres:17-alpine` removes `pg_isready`), healthcheck syntax updates, port changes |
| `skimatik.yaml` | Skimatik renames a YAML key, requires a new top-level field, deprecates `default_functions` syntax |
| `.golangci.yml` | golangci-lint deprecates a linter, changes config schema between v1 and v2, adds required `version` field |
| `.github/workflows/ci.yml` | GitHub deprecates an action version, syntax changes (job-level `outputs`, etc.) |
| `.env.example` | Env var renamed, new required var added |
| `lefthook.yml` | lefthook config schema changes |
| `.gitignore` | New build artifact path appears that should be ignored |

Library-side drift compounds these: `pgxkit/v2` adds a required `WithX` option, `skimatik` changes its CLI flag for query files, `chikit` renames a sentinel.

### What can drift between `EXAMPLE.md` and the libraries

We just shipped six fixes for this:

- `canonlog.AddRequestFields` doesn't exist (real API is `InfoAdd`/`InfoAddMany`)
- `*pgxkit.DB.Ping` doesn't exist (real method is `HealthCheck`)
- `chikit.RegisterValidation` returns `error` (examples ignored it)
- Cursors are opaque base64 JSON, not shortuuids (`encodeCursor`/`decodeCursor` was wrong)
- `chikit.APIError` wire field is `errors:[{param,...}]` not `fields:[{field,...}]`
- `pgxkit.RequireDB` returns `*TestDB`, not `*DB`

A smoke test that compiles EXAMPLE.md's code against the actual library packages would have caught five of those six at the moment the doc went wrong. (The sixth — wire-format JSON — is testable but needs an integration test that observes the response body.)

### Why golangci-lint doesn't cover this

`golangci-lint` analyzes Go source. EXAMPLE.md is markdown. The compiler never sees the code blocks. Until we extract them to `.go` files, no static analysis applies.

### Why skimatik's own test suite doesn't cover this

Skimatik tests its generation against its own `example-app/`. Skimatik's example-app does not follow the blueprint's pattern (consumer-owned interfaces, `executorFromContext`, hand-written wrappers around generated repos). Skimatik's tests catch generation bugs in skimatik. They don't catch:

- The blueprint's `skimatik.yaml` parsing with current skimatik
- The blueprint's hand-written `*Repository` wrappers compiling against current skimatik output
- The blueprint's `executorFromContext` composing with skimatik's `Executor` parameter type
- The blueprint's domain method names (`Create`, `GetByID`) lining up with what skimatik generates (`CreateProducts`, `GetProductByAccountAndID`)

Skimatik upstream changes a generated method's parameter name, the blueprint's wrapper breaks, skimatik's test suite stays green.

### Why this matters for the blueprint specifically

Most documentation projects don't ship executable artifacts. The blueprint does — `templates/` is real config that real services consume verbatim. That puts the blueprint in the same category as cookiecutter (Python templates), yeoman (JS scaffolds), helm chart releases, and `kubectl apply -f` manifests. Every one of those ecosystems smoke-tests their templates in CI as standard practice. The blueprint should too.

## The Key Insight: `templates/` Is Zero Percent Skimatik Output

Worth being explicit because it shapes the proposal.

The eight files in `templates/` are 100% hand-written. None of them is generated by skimatik. Skimatik produces Go code in `internal/repository/generated/`, which lives **in the consumer's repo** after `make generate` runs against a live Postgres — it is never present in `templates/`.

This means:

1. The smoke test isn't re-testing skimatik's output. Skimatik has its own tests.
2. The smoke test is testing the **integration** — does the templates' Makefile orchestrate skimatik correctly, do the blueprint's hand-written wrappers compile against skimatik's current output, do the patterns compose.
3. The fixture the smoke test needs (a minimum vertical slice of Go code) is *not* skimatik output — it is hand-written code that *uses* skimatik. About 25 files of hand-written Go plus 6 generated files produced when the smoke test runs `make generate`.

The 25 hand-written files are *exactly* the contents of `EXAMPLE.md`'s code blocks. That's the leverage point this proposal turns on.

## Three Tiers

The smoke test can be built at three levels of completeness. Each tier subsumes the one before it.

### Tier 1 — Static lint of templates only

Run linters against the template files in isolation:

- `yamllint` over `skimatik.yaml`, `lefthook.yml`, `.github/workflows/ci.yml`, `docker-compose.yml`
- `actionlint` over `.github/workflows/ci.yml`
- `golangci-lint config verify` over `.golangci.yml`
- `make -n` over `Makefile` (parses but doesn't execute targets)

**Catches:** ~25% of drift — typos, malformed YAML, broken GitHub Actions syntax, deprecated golangci-lint config schema.

**Misses:** anything that requires the templates to *combine* with Go code, libraries, or each other.

**Cost:** ~hour. Single CI workflow.

### Tier 2 — Compile and lint the assembled service

Tier 1 plus: extract `EXAMPLE.md`, merge with templates, scaffold a complete service in a temp dir, compile and lint it. No Postgres, no Docker.

Steps:

1. Run extractor: parse `EXAMPLE.md`, write each `{file=X}` block to `/tmp/smoke/X`
2. Copy `templates/*` to `/tmp/smoke/`, applying placeholder substitution (`myapp` → `smoketest`)
3. Copy `examples/_smoke-fixtures/*` to `/tmp/smoke/` — the small set of files EXAMPLE.md doesn't fully cover (`cmd/<app>/main.go`, `cmd/<app>/root.go`, the migration files, a stub for any package not in EXAMPLE.md)
4. `go mod init github.com/example/smoketest && go mod tidy`
5. `go build ./...`
6. `golangci-lint run`
7. `go test -short ./...` (unit tests that don't need a DB)

**Catches:** ~70% of drift. Anything that breaks compilation against current `pgxkit`, `chikit`, `canonlog`, plus all of Tier 1.

**Misses:** runtime issues — Postgres healthcheck behavior, skimatik regeneration against a real schema, migration apply, golden-test capture, integration test execution.

**Cost:** ~half a day for the extractor + fixture set + CI workflow. Reference slice is already written (it's EXAMPLE.md); the new code is the extractor (~80 lines), fixture files (~150 lines for `cmd/`, migration, etc.), and CI orchestration.

### Tier 3 — Full bootstrap with Docker + Postgres

Tier 2 plus: stand up Postgres, run database-touching make targets, exercise `skimatik generate` against a real schema, run the integration test suite.

Additional steps after Tier 2:

8. `docker compose up -d postgres` (from templates' docker-compose.yml)
9. `make migrate-up` (apply the migration that's in the smoke fixtures)
10. `make generate` (skimatik regenerates against the real Postgres schema)
11. `make test-integration` (full suite hits real DB)
12. `docker compose down -v`

**Catches:** ~95% of drift. Adds runtime validation of every external dependency. The other 5% is the rare "works on macOS, breaks on Linux" class.

**Misses:** intentional drift between EXAMPLE.md and the un-doc'd smoke fixtures, since they live separately. Also misses anything specific to a real consumer's environment.

**Cost:** ~one day on top of Tier 2. Mostly CI workflow time and flake handling (Docker pulls, healthcheck timeouts, port collisions).

### Recommended scope

**Build Tier 2.** The cost-to-confidence ratio is best:

- Catches 70% of real drift modes
- Fixes the EXAMPLE.md "is this code correct?" question by making part of it compile
- Side effect: every PR that edits EXAMPLE.md or `templates/` proves the result still works

**Skip Tier 3 for v1.** Add it later if specific runtime drift (skimatik regen, migration apply behavior) keeps showing up as recurring breakage.

**Skip Tier 1 entirely.** Strictly subsumed by Tier 2.

## The EXAMPLE.md ↔ Reference App Tension

If we have a real reference app, it competes with EXAMPLE.md as the "canonical Products slice." Three legitimate ways to resolve this; each has different drift surface and tooling cost.

### Resolution A — EXAMPLE.md is canon, reference app is a fixture that may diverge slightly

Maintain both by hand. The reference app is `examples/products/`, fully committed Go. Smoke test runs against the reference app directly.

**Pro:** EXAMPLE.md stays a doc with full prose-voice freedom. The reference app exists as a copy-ready starting point for new services (`cp -r examples/products ../newservice`).
**Con:** Two sources of truth. They will drift. Need a manual diff process or a `blueprint-vet` rule to keep them aligned.

### Resolution B — Reference app is canon, EXAMPLE.md is generated from it

The reference app is the source of truth. A doc generator transforms it into EXAMPLE.md (probably extracting godoc comments as prose between code blocks).

**Pro:** Single source. Machine-checked.
**Con:** Doc voice is constrained by what fits as code comments. Generator is real tooling. `EXAMPLE.md` becomes build output, not a hand-edited file. The blueprint's voice ("pattern reference for AI agents") doesn't fit a comment-extracted format.

### Resolution C — EXAMPLE.md is canon, reference app is regenerated from it

EXAMPLE.md's code blocks carry file-path markers. A small extractor parses the markdown and writes each block to its declared path. The reference app is build output, never committed.

**Pro:** EXAMPLE.md keeps its prose voice. Single source of truth. The smoke test is the *only* consumer of the extracted files — they live in `/tmp` during CI, then get cleaned up.
**Con:** Need to write the extractor (~80 lines of Go). Need a marker convention for code blocks. The reference app is no longer a copy-ready starting point unless we add an `examples/` snapshot regenerated on releases.

### Recommendation: Resolution C

It's the cleanest fit for the blueprint's existing voice. EXAMPLE.md is already the canonical slice — annotating its code blocks with file paths is a small change, not an architectural pivot. The extractor is ~80 lines including tests.

If the "copy-ready starting point" use case becomes important later, that's solvable separately by snapshotting `examples/products/` from EXAMPLE.md on each blueprint release. v1 doesn't need that.

## Extractor Design

Concrete design of the EXAMPLE.md → file-system extractor.

### File-path marker convention

Each code block that should be extracted carries a `{file=PATH}` annotation on the language tag line:

````markdown
```go {file=internal/models/product.go}
package models

import (
    "time"

    "github.com/google/uuid"
)

type Product struct { /* ... */ }
```
````

The annotation is parsed by the extractor; markdown renderers ignore it (or render it harmlessly as part of the code-fence info string). Code blocks without `{file=...}` are illustrative and not extracted.

### Multiple blocks per file (concatenation)

Some files appear in EXAMPLE.md as multiple blocks separated by prose (e.g., `internal/api/products.go` is shown in sections — request types, then response types, then handlers). The extractor concatenates blocks that share a `{file=...}` value, in document order, joined with `\n\n`.

```markdown
```go {file=internal/api/products.go}
package api

import (...)

type CreateProductRequest struct {...}
```

(prose...)

```go {file=internal/api/products.go}
type ProductResponse struct {...}
```
```

→ One file at `internal/api/products.go` with both blocks concatenated.

### Module path substitution

EXAMPLE.md uses `github.com/yourorg/myapp` as the placeholder module path. The extractor substitutes a configurable target module (default: `github.com/example/smoketest`). All occurrences in extracted code are replaced.

### Smoke fixtures directory

A new `examples/_smoke-fixtures/` directory in the blueprint repo holds files EXAMPLE.md does not provide:

```
examples/_smoke-fixtures/
├── cmd/
│   └── myapp/
│       ├── main.go               # cobra root.Execute
│       ├── root.go               # cobra.Command, AddCommand for serve/migrate
│       ├── serve.go              # full RunE with config + DB + handler wiring
│       └── migrate.go            # full RunE for up/down/version
├── internal/
│   ├── config/
│   │   └── config.go             # config.Config + LoadLogging/LoadDatabase/LoadHTTP/LoadRedis
│   ├── repository/
│   │   ├── errors.go             # ErrNotFound, ErrAlreadyExists, translateError
│   │   └── tx.go                 # TxManager, ContextWithTx, executorFromContext
│   └── database/
│       ├── schema.sql
│       └── migrations/
│           ├── 000001_create_accounts.up.sql
│           ├── 000001_create_accounts.down.sql
│           ├── 000002_create_products.up.sql
│           └── 000002_create_products.down.sql
└── go.mod.tmpl                   # module + Go version + dep block; rendered with target module path
```

These are blueprint-pattern code that's already documented elsewhere (ARCHITECTURE.md, CONFIG.md, DATABASE.md) but not in EXAMPLE.md. The smoke-fixtures directory is the **executable form** of those docs, and like EXAMPLE.md should be the canonical reference for those patterns.

(Possible future move: extend the marker convention to other docs, so the smoke fixtures shrink as more files become extractable. v1 keeps them separate to limit scope.)

### Extractor behavior

Single Go program at `scripts/extract-example/main.go` (or `cmd/extract-example/` if we eventually package the blueprint repo as a Go module).

```bash
go run ./scripts/extract-example \
    --example EXAMPLE.md \
    --fixtures examples/_smoke-fixtures \
    --templates templates \
    --module github.com/example/smoketest \
    --output /tmp/smoke
```

Behavior:

1. Parse `EXAMPLE.md`. For each fenced code block with a `{file=PATH}` annotation, accumulate content keyed by PATH.
2. For each accumulated PATH, write `output/PATH` with concatenated content. Substitute module path.
3. Walk `templates/`. For each file, read content, substitute `myapp` placeholders, write to corresponding location under `output/`.
4. Walk `fixtures/`. Copy each file to corresponding location under `output/`. Substitute module path. Render `go.mod.tmpl` to `go.mod` with the target module path.
5. Exit 0.

If output directory exists, refuse unless `--force` is passed (avoid accidental clobber).

### Tests for the extractor

Unit tests in `scripts/extract-example/extractor_test.go`:

- Parses a single `{file=X}` block correctly
- Concatenates two blocks with same `{file=X}` in document order
- Ignores blocks without `{file=...}`
- Substitutes module path in extracted content
- Errors on `{file=X}` with relative `..` path traversal
- Handles blocks in any language (`go`, `sql`, `yaml`, etc.)

Tests against representative `EXAMPLE.md` fixtures shipped under `scripts/extract-example/testdata/`.

## End-to-End Smoke Flow

What happens in CI when the workflow runs.

```bash
# Setup
SMOKE_DIR=$(mktemp -d -t blueprint-smoke-XXXXXX)
cleanup() { rm -rf "$SMOKE_DIR"; }
trap cleanup EXIT

# 1. Extract canonical slice + scaffold from templates + fixtures
go run ./scripts/extract-example \
    --example EXAMPLE.md \
    --fixtures examples/_smoke-fixtures \
    --templates templates \
    --module github.com/example/smoketest \
    --output "$SMOKE_DIR"

cd "$SMOKE_DIR"

# 2. Resolve dependencies
go mod tidy

# 3. Tier 2 — compile and lint
go build ./...
golangci-lint run
go test -short ./...

# 4. Tier 3 (optional) — runtime
docker compose up -d postgres
# Wait for healthcheck...
make migrate-up
make generate
make test-integration
docker compose down -v
```

CI workflow `.github/workflows/template-smoke.yml` runs this on every PR to the blueprint repo. Failure blocks merge.

## Architecture: What Changes in the Blueprint Repo

### New files

```
go-blueprint/
├── proposals/
│   └── template-smoke.md             # this proposal
├── scripts/
│   └── extract-example/
│       ├── main.go                   # ~80 lines
│       ├── extractor.go              # parse markdown + concat
│       ├── extractor_test.go
│       └── testdata/
│           └── example-minimal.md    # fixture for tests
├── examples/
│   └── _smoke-fixtures/              # see Section 6
├── .github/
│   └── workflows/
│       └── template-smoke.yml        # the CI workflow
└── Makefile                          # blueprint-maintainer Makefile
                                       # (NOT the same as templates/Makefile)
```

### Edits to existing files

- `EXAMPLE.md` — add `{file=PATH}` markers to every canonical code block. Roughly 15 blocks to annotate.
- `README.md` — add row for `proposals/template-smoke.md`. Note that smoke testing exists.
- `templates/Makefile` — no changes (it's the consumer-facing Makefile, not the blueprint maintainer's).

### New top-level Makefile

The blueprint repo currently has no Makefile (it's docs). A new `Makefile` at the repo root provides maintainer-facing targets:

```makefile
.PHONY: smoke smoke-tier2 smoke-tier3 extract-example

extract-example:
	@go run ./scripts/extract-example --output build/smoke

smoke: smoke-tier2

smoke-tier2: extract-example
	@cd build/smoke && go mod tidy && go build ./... && golangci-lint run && go test -short ./...

smoke-tier3: smoke-tier2
	@cd build/smoke && \
	  docker compose up -d postgres && \
	  make migrate-up && make generate && make test-integration && \
	  docker compose down -v
```

This is the *blueprint maintainer's* Makefile. Consumers never see it; they see `templates/Makefile` which is what gets copied into their service.

## Where It Runs

| Layer | What runs | When |
|-------|-----------|------|
| **Blueprint repo CI** | `make smoke-tier2` (and `smoke-tier3` if built) | Every PR to `nhalm/go-blueprint` |
| **Pre-commit (blueprint maintainer)** | Optional: `make smoke-tier2` | If fast enough; bypassable |
| **Pre-push** | Nothing | Skip |
| **Consumer side** | Nothing (consumers don't run blueprint smoke) | N/A |

This is a quality gate on *the blueprint*, not on consumer code. Consumer CI runs `make test`/`make lint`/`make verify` against the consumer's own code, which is a separate concern.

## Pending Blueprint Doc Changes

These should land *with* (or just before) the smoke test infrastructure:

1. **Annotate `EXAMPLE.md` code blocks with `{file=PATH}` markers.** Roughly 15 blocks. No prose changes, just the language-tag line.

2. **Update `EXAMPLE.md`'s file-path comments to be redundant with markers.** Currently each block has a leading comment like `// internal/models/product.go`. Keep the comment for human readers; the marker is for the extractor. They serve different audiences.

3. **Adjust `EXAMPLE.md` blocks that are currently fragments to be complete files.** A handful of blocks in EXAMPLE.md show only a section of a file (e.g., the routes.go products subsection). The smoke test needs complete files. Either complete them in EXAMPLE.md, or accept the fragment and concatenate with smoke-fixture content. Decide per block.

4. **Add `examples/_smoke-fixtures/` content.** ~10 small files covering `cmd/myapp/`, `internal/config/`, `internal/repository/{errors,tx}.go`, `internal/database/{schema.sql,migrations/}`. Most of this content already exists as code snippets in ARCHITECTURE.md, CONFIG.md, and DATABASE.md — it just needs to be materialized as actual files.

5. **Add row to `README.md` docs table for `proposals/template-smoke.md`.** Optional; this proposal currently lives separately and need not be a discoverable doc.

## Open Questions (Resolve Before Building)

1. **Marker convention syntax.** `{file=PATH}` is one option. Alternatives:
    - `{file: PATH}` (more YAML-like)
    - `<!-- file: PATH -->` (HTML comment, doesn't appear in any rendered output)
    - First-line magic comment in the block itself (`// file: PATH` for Go, `-- file: PATH` for SQL)

   Recommendation: `{file=PATH}` on the language-tag line. Some markdown processors (CommonMark + GitHub) treat the info string after the language as opaque, so this round-trips cleanly. Verify against the blueprint repo's rendering.

2. **Multiple blocks for one file: append or replace?** Two valid policies for `{file=X}` blocks in document order:
    - **Append:** all blocks for `X` concatenate. Default behavior described above.
    - **Replace:** later blocks shadow earlier. Useful for "here's the simple version, now here's the full version" prose patterns, but probably not what we want.

   Recommendation: append by default. If replace is ever needed, add `{file=X mode=replace}`.

3. **Smoke-fixtures vs extending EXAMPLE.md.** The cleaner long-term direction is to make EXAMPLE.md the source for *every* canonical file (including `cmd/myapp/serve.go`), shrinking the smoke fixtures to nothing. v1 keeps them separate to limit scope. Decide at build time whether to migrate `cmd/` content into EXAMPLE.md too.

4. **Module path substitution: how robust?** Naive string replace on `github.com/yourorg/myapp` works but breaks if someone writes that string in a comment as an example. Fix: use a unique placeholder string in EXAMPLE.md (e.g., `BLUEPRINT_MODULE_PATH`) that the extractor substitutes. Decide before building.

5. **Library version pinning in the smoke `go.mod`.** Default could be `@latest` for all blueprint deps, or pin to specific tested versions. `@latest` catches drift faster but means a library release immediately breaks the smoke test (which might be desirable — that's the point — or might be flaky). Pinning is reproducible but lags behind real consumer behavior. Recommendation: `@latest` for blueprint-controlled libs (`pgxkit`, `chikit`, `canonlog`, `skimatik`), pinned for everything else.

6. **Tier 3 worth it now or later?** Tier 2 catches the bulk of drift. Tier 3 needs Docker in CI (slower, flakier). Recommendation: build Tier 2, defer Tier 3 until a Tier-2-missed bug actually ships.

7. **Do we extract from other docs (DATABASE.md, ARCHITECTURE.md, CONFIG.md, etc.) too?** Currently this proposal only extracts from EXAMPLE.md. Code blocks in DATABASE.md, etc. are illustrative, often partial, often deliberately not standalone. Pulling them into the smoke test would either require annotating which are extractable (more markers) or smoke fixtures absorb that role. Recommendation: EXAMPLE.md only, smoke-fixtures absorb the rest.

8. **What happens when EXAMPLE.md changes break the smoke test?** This is the design's payoff: PR can't merge until the docs and the code agree. But it also means a doc-only change (rephrasing a paragraph) shouldn't block CI. The extractor only operates on annotated code blocks, so prose edits are safe; code-block edits gate. Verify behavior in tests.

## Build Plan

When ready to execute. Each step ships independently.

### Step 1 — Annotate `EXAMPLE.md` (no tool changes)

Add `{file=PATH}` markers to canonical code blocks in EXAMPLE.md. No tooling, no CI yet — this just makes EXAMPLE.md ready to be extracted later.

Standalone value: the markers also tell agents reading EXAMPLE.md exactly what file each block belongs to. Useful even if extraction never happens.

### Step 2 — Build the extractor

Write `scripts/extract-example/`. Tests against minimal fixtures. Run locally; verify it produces sensible output.

Standalone value: the extractor is a debugging tool. `go run ./scripts/extract-example --output /tmp/check` to see what EXAMPLE.md materializes into.

### Step 3 — Add smoke fixtures

Write `examples/_smoke-fixtures/` with the un-doc'd files (`cmd/myapp/`, etc.). Verify the merge works: extractor + fixtures → temp dir → manually `go build ./...` → green.

Standalone value: the fixtures double as a concrete reference for material that's currently scattered across ARCHITECTURE.md, CONFIG.md, DATABASE.md.

### Step 4 — CI workflow for Tier 2

Write `.github/workflows/template-smoke.yml`. Runs extractor + fixtures + `go build ./... && golangci-lint run && go test -short`. Required check on PRs.

Standalone value: Tier 2 catches the bulk of drift; this is when smoke testing starts paying back.

### Step 5 — Tier 3 (optional, deferred)

Add Docker + Postgres steps to the CI workflow. Run `make migrate-up && make generate && make test-integration`. Mark optional or required based on flake observations.

### Step 6 — Migrate smoke-fixtures back into EXAMPLE.md (optional, deferred)

If smoke-fixtures stay small and stable, leave them. If they grow or feel separate, consider extending EXAMPLE.md to cover `cmd/myapp/` and other currently-fixtured content. Removes the parallel-content surface.

## References

- **CommonMark code-fence info string:** https://spec.commonmark.org/0.30/#info-string — what's allowed after the language tag in a code fence.
- **Markdown attribute extensions (e.g. pandoc):** https://pandoc.org/MANUAL.html#fenced-code-blocks — convention for `{...}` annotations on code blocks.
- **cookiecutter test patterns:** https://cookiecutter.readthedocs.io/en/stable/advanced/local_extensions.html — the Python ecosystem's standard "smoke-test a generated project" pattern.
- **kubernetes/kubernetes verify scripts:** `hack/verify-*.sh` — large-scale example of repo-self-verification in Go-shop style.
- **Blueprint repo doc:** [EXAMPLE.md](../EXAMPLE.md) — the canonical slice this proposal makes extractable.
- **Blueprint repo doc:** [LIBRARIES.md](../LIBRARIES.md) — the verified library surfaces extracted code must compile against.
- **Sister proposal:** [proposals/blueprint-vet.md](./blueprint-vet.md) — the conformance checker. Composes with this one (smoke test could run `make verify` as a final step once `blueprint-vet` exists).

## Reading This Doc Cold

If you (or a future agent) come back to this without context:

1. **Skim TL;DR + Motivation** to recover the "why."
2. **Read "The Key Insight"** — that's the core fact this proposal turns on.
3. **Read Three Tiers** to see what scope choices look like.
4. **Read "The EXAMPLE.md ↔ Reference App Tension"** to understand why we don't just commit a reference app.
5. **Read Extractor Design + End-to-End Smoke Flow** to see what the work looks like.
6. **Read Open Questions** — those are the unresolved decisions.
7. **Follow Build Plan** in order; each step is independently shippable.

The decisions still to make:

- **Marker syntax** (Section 11.1): `{file=...}` recommended; verify against the blueprint's markdown rendering.
- **Tier scope** (Section 5): build Tier 2; defer Tier 3.
- **EXAMPLE.md vs reference app** (Section 6): Resolution C (extract from EXAMPLE.md) recommended.
- **Module substitution mechanism** (Section 11.4): unique placeholder string in EXAMPLE.md, not raw `github.com/yourorg/myapp`.

The case still to evaluate:

- **Yes, build:** evidence that EXAMPLE.md or templates have drifted from working since LIBRARIES.md was written. The session that produced LIBRARIES.md surfaced six bugs of this exact shape — a smoke test prevents the recurrence.
- **No, skip:** if blueprint changes are infrequent enough that manual verification per change is sufficient, and the cost of one shipped breakage per year is acceptable.

The tier-1-only version (~hour, just static linting) is worth doing under almost any scenario. The tier-2 version (~half a day, the recommendation here) is worth doing if blueprint conformance and EXAMPLE.md correctness are both load-bearing. Tier 3 is worth doing only after Tier 2 leaves real drift on the table.
