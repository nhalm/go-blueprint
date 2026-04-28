# Template Smoke Testing

**Status:** implemented — `make smoke` green end-to-end on 2026-04-28; CI workflows in `.github/workflows/template-smoke.yml` (per-PR) and `.github/workflows/template-smoke-drift.yml` (weekly cron + repository_dispatch).
**Owner:** Nick Halm

A design document for smoke-testing the blueprint's `templates/` directory — proving that a fresh service scaffolded from the templates actually compiles, lints, and runs the documented daily loop. Includes the unexpected leverage point: the smoke-test fixture is mostly EXAMPLE.md materialized as files, so the smoke test machine-verifies that EXAMPLE.md is executable, not just plausible-looking.

## TL;DR

Build a CI workflow in the blueprint repo that:

1. Extracts each fenced code block annotated `{file=...}` from every canonical doc (EXAMPLE.md, ARCHITECTURE.md, CONFIG.md, DATABASE.md) to its declared path in a temporary directory
2. Merges the extracted files with `templates/*` (substituting the placeholder module path) and `examples/_smoke-fixtures/go.mod.tmpl`
3. Runs the full daily loop against the temp directory: `docker compose up postgres`, `make migrate-up`, `make generate` (skimatik regenerates against real schema), `go build ./...`, `golangci-lint run`, `make test-integration`
4. Cleans up

The blueprint repo's CI fails when any of those steps break. Drift in templates, canonical docs, or the libraries the blueprint depends on is caught the first time the smoke runs after the change.

This proposal exists because:

1. The blueprint ships executable scaffolding (Makefile, docker-compose, skimatik.yaml, CI workflow). Those are real artifacts that can break.
2. Library churn (`pgxkit`, `chikit`, `canonlog`, `skimatik`) silently invalidates the patterns in `EXAMPLE.md` between blueprint releases.
3. EXAMPLE.md is the canonical Products slice, but currently nothing proves its code blocks compile against current libraries. We've already shipped one round of bug fixes (cursor format, `Ping` vs `HealthCheck`, missing `error` returns) that a smoke test would have caught at PR time.

It does not exist yet. Design decisions are resolved (Section 11); the proposal is ready for execution against the build plan in Section 13.

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

## Scope: Full Bootstrap

The smoke test runs the **complete daily loop** end-to-end: extract canonical docs into a temp directory, merge with templates, stand up Postgres in Docker, run migrations, run `skimatik generate`, build, lint, and exercise the integration test suite.

Steps:

1. Extractor parses every doc in `--docs`, writes each `{file=PATH}` block to `/tmp/smoke/PATH`
2. Copy `templates/*` to `/tmp/smoke/`, applying module-path substitution
3. Render `examples/_smoke-fixtures/go.mod.tmpl` to `/tmp/smoke/go.mod`
4. `make install-tools` (installs `blueprint-vet`, `blueprint-sql-check`, `golangci-lint`, `skimatik`, `migrate`)
5. `go mod tidy`
6. `docker compose up -d postgres` (from templates' docker-compose.yml)
7. Wait for Postgres healthcheck
8. `make migrate-up`
9. `make generate` (skimatik regenerates against the real Postgres schema)
10. `go build ./...`
11. `make lint` (golangci-lint)
12. `make verify` (blueprint-vet conformance — Go AST analyzers + SQL file rules)
13. `make test-integration`
14. `docker compose down -v`

**Why no staged "lint-only" or "compile-only" intermediate scope.** The blueprint's signature pattern is hand-written wrappers around skimatik-generated code. Those wrappers import `internal/repository/generated/`, which doesn't exist until `make generate` runs against a live Postgres. A "compile only, no Docker" tier either requires a committed snapshot of skimatik output (drifty), excludes the repository layer (defeats the purpose of testing the blueprint's signature pattern), or pretends to work but doesn't. Once Postgres is in CI, running migrations + generate + tests is small marginal cost.

**Catches:** drift in `templates/`, drift between canonical docs and current library APIs, skimatik regeneration breakage, migration apply behavior, integration-test wire format, and blueprint-pattern conformance (handler response writing, repository executor routing, layer-direction violations, soft-delete defaults — whatever rules `blueprint-vet` ships at the time the smoke runs). All six fixes from the recent doc-correction round (Section 2) would have been caught here.

**Composes with `blueprint-vet`.** `make verify` runs as part of the smoke flow, so any conformance rule the vet enforces in consumer projects is also enforced against the canonical docs. If a rule lands in `blueprint-vet` and EXAMPLE.md violates it, the smoke test fails until the doc is updated — keeping the docs and the conformance rules in lockstep.

**Cost:** Docker setup adds ~60–90s to CI runtime. Flake surface is the standard Postgres-in-CI set: image pull rate limits, healthcheck timeouts, port collisions. Well-trodden territory.

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

**Convention: only the first block per file carries `package` + imports; subsequent blocks are body-only** (types, functions, methods). This matches how humans already write tutorial code and keeps the extractor dumb (~80 lines). Violations fail loudly because the concatenated file won't compile — the smoke test itself is the check.

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

The canonical docs use `github.com/yourorg/myapp` as the placeholder module path. The extractor substitutes a configurable target module (default: `github.com/example/smoketest`). All occurrences in extracted content are replaced via naive string substitution.

Why naive substitution rather than a unique placeholder like `BLUEPRINT_MODULE_PATH`: the docs need to render as valid, legible Go for the agent audience. `BLUEPRINT_MODULE_PATH/internal/models` doesn't parse and pessimizes the primary reader to defend against a hypothetical false-positive that doesn't exist in current usage. The convention "don't write `github.com/yourorg/myapp` in any non-import context" is enforced by review, and a violation would surface loudly as a compilation failure in the smoke test. If a Go-AST-aware substitution is ever needed (only rewrite inside `import` declarations), upgrade then.

### Source docs and smoke fixtures

The extractor walks **every doc that contains `{file=...}` markers**, not just EXAMPLE.md. This honors the blueprint's "every pattern is inline" rule: each pattern's canonical home is the doc that already explains it, and the smoke test verifies that doc is executable.

| File the smoke test needs | Source doc |
|---|---|
| `cmd/myapp/{main,serve}.go` | ARCHITECTURE.md |
| `cmd/myapp/{root,migrate}.go` | CONFIG.md |
| `internal/config/config.go` | CONFIG.md (multi-block) |
| `internal/repository/tx.go` | DATABASE.md |
| `internal/repository/errors.go` | ERRORS.md |
| `internal/database/schema.sql`, migrations, `queries/products.sql` | EXAMPLE.md |
| `internal/models/product.go`, `internal/errors/errors.go`, `internal/repository/product_repository.go`, `internal/service/*.go`, `internal/api/{products,errors,service_interface}.go` | EXAMPLE.md (Products slice) |
| `internal/api/{routes,handler,validators}.go` | API.md |

`examples/_smoke-fixtures/` shrinks to **just the files that genuinely can't be a doc code block**:

```
examples/_smoke-fixtures/
└── go.mod.tmpl                   # module + Go version + dep block; rendered with target module path
```

Every other "fixture" file lives in its source doc with a `{file=...}` marker. If a pattern doesn't fit naturally in an existing doc, the doc gets extended — the fixtures dir is not a backdoor for un-doc'd code.

**Cross-doc concatenation rule:** the extractor merges blocks for the same `{file=X}` regardless of which doc they came from, in a deterministic order (alphabetical by source doc, then document order within each). A given file should normally be authored in a single doc; cross-doc blocks for the same file are an anti-pattern that the smoke test won't catch directly but that reviewers should reject.

### Library version pinning

`go.mod.tmpl` carries pinned versions for **every** dependency, including the blueprint-controlled libs (`pgxkit`, `chikit`, `canonlog`, `skimatik`). PR-time CI is deterministic: the same blueprint commit produces the same smoke result every run, and bumping a pin is an explicit blueprint PR — same model as any other dependency update.

Lib-drift detection lives in a separate workflow:

- **Weekly cron** (`.github/workflows/template-smoke-drift.yml`) checks out the repo, runs `go get -u ./...` over the smoke output, runs the full smoke flow. On failure, opens or updates a tracking issue with the resolved version diff.
- **Optional `repository_dispatch`** trigger on each lib repo's release workflow, so a new `pgxkit` release immediately fires the drift workflow rather than waiting for the next cron tick.

Why this split: PR CI must be stable — an unrelated `pgxkit` patch shipped at 11pm shouldn't break the next morning's blueprint PR for reasons the author can't diagnose. The drift workflow is the dedicated signal for "library released, blueprint needs an update," and its failure is non-blocking on PR merges.

### Extractor behavior

Single Go program at `scripts/extract-docs/main.go` (or `cmd/extract-docs/` if we eventually package the blueprint repo as a Go module).

```bash
go run ./scripts/extract-docs \
    --docs EXAMPLE.md,ARCHITECTURE.md,CONFIG.md,DATABASE.md,ERRORS.md,API.md \
    --fixtures examples/_smoke-fixtures \
    --templates templates \
    --module github.com/example/smoketest \
    --output /tmp/smoke
```

Behavior:

1. Parse each doc in `--docs`. For each fenced code block with a `{file=PATH}` annotation, accumulate content keyed by PATH (cross-doc, deterministic order).
2. For each accumulated PATH, write `output/PATH` with concatenated content. Substitute module path.
3. Walk `templates/`. For each file, read content, substitute `myapp` placeholders, write to corresponding location under `output/`.
4. Walk `fixtures/`. Copy each file to corresponding location under `output/`. Substitute module path. Render `go.mod.tmpl` to `go.mod` with the target module path.
5. Exit 0.

If output directory exists, refuse unless `--force` is passed (avoid accidental clobber).

### Tests for the extractor

Unit tests in `scripts/extract-docs/extractor_test.go`:

- Parses a single `{file=X}` block correctly
- Concatenates two blocks with same `{file=X}` in document order
- Ignores blocks without `{file=...}`
- Substitutes module path in extracted content
- Errors on `{file=X}` with relative `..` path traversal
- Handles blocks in any language (`go`, `sql`, `yaml`, etc.)

Tests against representative markdown fixtures shipped under `scripts/extract-docs/testdata/`.

## End-to-End Smoke Flow

What happens in CI when the workflow runs.

```bash
# Setup
SMOKE_DIR=$(mktemp -d -t blueprint-smoke-XXXXXX)
cleanup() { rm -rf "$SMOKE_DIR"; }
trap cleanup EXIT

# 1. Extract canonical docs + scaffold from templates + go.mod.tmpl
go run ./scripts/extract-docs \
    --docs EXAMPLE.md,ARCHITECTURE.md,CONFIG.md,DATABASE.md,ERRORS.md,API.md \
    --fixtures examples/_smoke-fixtures \
    --templates templates \
    --module github.com/example/smoketest \
    --output "$SMOKE_DIR"

cd "$SMOKE_DIR"

# 2. Install tools (blueprint-vet, golangci-lint, skimatik, migrate)
make install-tools

# 3. Resolve dependencies (pinned in go.mod.tmpl)
go mod tidy

# 4. Stand up Postgres, run migrations, regenerate skimatik output
docker compose up -d postgres
# Wait for healthcheck...
make migrate-up
make generate

# 5. Build, lint, conformance check, integration test
go build ./...
make lint           # golangci-lint
make verify         # blueprint-vet + blueprint-sql-check
make test-integration

# 6. Tear down
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
│   └── extract-docs/
│       ├── main.go                   # ~80 lines
│       ├── extractor.go              # parse markdown + concat
│       ├── extractor_test.go
│       └── testdata/
│           └── minimal-doc.md        # fixture for tests
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
.PHONY: smoke extract-docs

extract-docs:
	@go run ./scripts/extract-docs --output build/smoke

smoke: extract-docs
	@cd build/smoke && \
	  make install-tools && \
	  go mod tidy && \
	  docker compose up -d postgres && \
	  make migrate-up && make generate && \
	  go build ./... && make lint && make verify && \
	  make test-integration && \
	  docker compose down -v
```

This is the *blueprint maintainer's* Makefile. Consumers never see it; they see `templates/Makefile` which is what gets copied into their service.

## Where It Runs

| Layer | What runs | When |
|-------|-----------|------|
| **Blueprint repo CI** | `make smoke` | Every PR to `nhalm/go-blueprint` |
| **Pre-commit (blueprint maintainer)** | Optional: `make smoke` | If Docker is available locally; bypassable |
| **Pre-push** | Nothing | Skip |
| **Consumer side** | Nothing (consumers don't run blueprint smoke) | N/A |

This is a quality gate on *the blueprint*, not on consumer code. Consumer CI runs `make test`/`make lint`/`make verify` against the consumer's own code, which is a separate concern.

## Pending Blueprint Doc Changes

These should land *with* (or just before) the smoke test infrastructure:

1. **Annotate code blocks across canonical docs with `{file=PATH}` markers.** EXAMPLE.md (the Products slice), ARCHITECTURE.md (`cmd/myapp/`, `internal/repository/tx.go`), CONFIG.md (`internal/config/config.go`), DATABASE.md (`internal/repository/errors.go`, `internal/database/schema.sql`, migrations). No prose changes, just the language-tag line.

2. **Keep the leading file-path comments in code blocks for human readers.** Each block already has a `// internal/models/product.go` comment; the marker is for the extractor. They serve different audiences.

3. **Adjust blocks that are currently fragments to be complete files** (or split a doc's section into multiple `{file=...}` blocks for the same file, per Section 7.2's append rule). The smoke test needs complete files; doc voice tolerates either approach.

4. **Add `examples/_smoke-fixtures/go.mod.tmpl`.** That's the only fixture left — every other "fixture" file lives in its source doc.

5. **Add row to `README.md` docs table for `proposals/template-smoke.md`.** Optional; this proposal currently lives separately and need not be a discoverable doc.

## Design Decisions (all resolved)

1. **Marker convention syntax.** ✅ **Resolved: `{file=PATH}` on the language-tag line.** Verified against GitHub's markdown API — `POST https://api.github.com/markdown` with ` ```go {file=internal/models/product.go}\n…\n``` ` produced byte-equivalent HTML to ` ```go\n…\n``` ` except for the annotated content being completely stripped. Syntax highlighting preserved (same `highlight-source-go` class, same token spans). Pandoc-style `{...}` is the closest thing to a de facto convention for fenced-code attributes.

2. **Multiple blocks for one file: append or replace?** ✅ **Resolved: append, with the "first block carries package + imports, subsequent blocks are body-only" convention.** Replace is a tutorial idiom that doesn't fit the blueprint's pattern-reference voice; skip `mode=replace` until something demands it. See Section 7.2.

3. **Smoke-fixtures vs extending source docs.** ✅ **Resolved: extract from every canonical doc, not just EXAMPLE.md.** Each pattern lives in its existing home (ARCHITECTURE.md, CONFIG.md, DATABASE.md) and gets `{file=...}` markers there. `examples/_smoke-fixtures/` shrinks to just `go.mod.tmpl`. Subsumes Q7. See Section 7.3.

4. **Module path substitution: how robust?** ✅ **Resolved: keep `github.com/yourorg/myapp` and use naive string replace.** A unique placeholder breaks doc readability for agents (the primary audience) to defend against a false-positive risk that doesn't exist in current docs. Convention enforced by review; violations fail loudly at compile time. See Section 7.4.

5. **Library version pinning in the smoke `go.mod`.** ✅ **Resolved: pin everything in `go.mod.tmpl`.** PR-time CI is deterministic; bumping pins is an explicit blueprint PR. A separate scheduled workflow (weekly cron, or `repository_dispatch` from each lib repo on release) runs the smoke test against `go get -u ./...` and files an issue if it fails — that's where lib-drift detection lives, decoupled from PR CI. See Section 7.5.

6. **Staged tiers (lint-only → compile-only → full bootstrap)?** ✅ **Resolved: skip the tiering, build the full bootstrap directly.** A "compile-only" intermediate scope isn't coherent — the blueprint's signature pattern (hand-written wrappers around skimatik-generated code) needs `make generate` against a live Postgres, so any meaningful smoke test requires Docker anyway. See Section 5.

7. **Do we extract from other docs (DATABASE.md, ARCHITECTURE.md, CONFIG.md, etc.) too?** ✅ **Resolved by Q3: yes.** Every canonical doc with `{file=...}` markers is a source. Blocks without markers stay illustrative (prose-only fragments are fine). The smoke test verifies that any doc claiming to show a complete file actually compiles.

8. **Doc-only changes and CI cost.** ✅ **Resolved: always run the smoke test, plus a small `paths-ignore` for known-irrelevant files** (`README.md`, `proposals/**`, `LICENSE`, image assets). Pure prose changes inside extractable docs (EXAMPLE.md, ARCHITECTURE.md, CONFIG.md, DATABASE.md) still trigger smoke — by construction they produce byte-identical extraction output, so smoke is deterministic, but running it enforces the discipline that "if you edit this doc, you accept smoke as a gate." A unit test in `scripts/extract-docs/extractor_test.go` codifies that prose-only edits to source docs produce identical output. If PR volume grows enough to feel the ~2-min CI cost, upgrade to a diff-based skip (run extractor against base and head, compare outputs); not worth building speculatively.

## Build Plan

### Step 1 — Prepare canonical docs

1a. **Audit each canonical doc for which code blocks should become extractable files.** EXAMPLE.md (Products slice), ARCHITECTURE.md (`cmd/myapp/{main,root,serve,migrate}.go`, `internal/repository/tx.go`), CONFIG.md (`internal/config/config.go`), DATABASE.md (`internal/repository/errors.go`, `internal/database/schema.sql`, migration files). Some blocks are already complete files; others are illustrative fragments.

1b. **Convert fragments to complete files.** Either expand the block in place, or split the doc's section into multiple `{file=...}` blocks for the same file (per Section 7.2's append rule: first block carries package + imports, subsequent blocks are body-only).

1c. **Add `{file=PATH}` markers** to the language-tag line of every block that should be extracted.

### Step 2 — Build the extractor

Write `scripts/extract-docs/` (parser, concatenation, module-path substitution, path-traversal guards). Unit tests in `extractor_test.go` covering the cases listed in Section 7.7, including the "prose-only edits to source docs produce identical output" invariant from Q8.

### Step 3 — Add `go.mod.tmpl` and the maintainer Makefile, verify locally

Write `examples/_smoke-fixtures/go.mod.tmpl` with pinned versions. Add the top-level maintainer Makefile (Section 9.3). Run `make smoke` locally end-to-end: extract → install-tools → docker compose up postgres → migrate-up → generate → build → lint → verify (blueprint-vet) → test-integration → docker compose down. Resolve any drift surfaced (canonical-doc bugs, conformance violations, missing files) until the flow is green.

### Step 4 — CI workflow

Write `.github/workflows/template-smoke.yml`. Runs `make smoke` on every PR with a `paths-ignore` for `README.md`, `proposals/**`, `LICENSE`, image assets. Required check on PRs.

### Step 5 — Lib-drift workflow

Write `.github/workflows/template-smoke-drift.yml`. Weekly cron (and optional `repository_dispatch` from each lib repo) that runs the smoke flow with `go get -u ./...` to detect breakage from new lib releases. Failures open or update a tracking issue rather than blocking PRs.

## References

- **CommonMark code-fence info string:** https://spec.commonmark.org/0.30/#info-string — what's allowed after the language tag in a code fence.
- **Markdown attribute extensions (e.g. pandoc):** https://pandoc.org/MANUAL.html#fenced-code-blocks — convention for `{...}` annotations on code blocks.
- **cookiecutter test patterns:** https://cookiecutter.readthedocs.io/en/stable/advanced/local_extensions.html — the Python ecosystem's standard "smoke-test a generated project" pattern.
- **kubernetes/kubernetes verify scripts:** `hack/verify-*.sh` — large-scale example of repo-self-verification in Go-shop style.
- **Blueprint repo doc:** [EXAMPLE.md](../EXAMPLE.md) — the canonical slice this proposal makes extractable.
- **Blueprint repo doc:** [LIBRARIES.md](../LIBRARIES.md) — the verified library surfaces extracted code must compile against.
- **`blueprint-vet`** ([github.com/nhalm/blueprint-vet](https://github.com/nhalm/blueprint-vet)) — the conformance checker (Go AST analyzers + SQL file rules). Already integrated in `templates/Makefile` via `make install-tools` and `make verify`. The smoke test runs `make verify` as part of its flow; rule rationale and the full rule list live in [`proposals/blueprint-vet.md`](./blueprint-vet.md).

## Reading This Doc Cold

If you (or a future agent) come back to this without context:

1. **Skim TL;DR + Motivation** to recover the "why."
2. **Read "The Key Insight"** — that's the core fact this proposal turns on.
3. **Read "Scope: Full Bootstrap"** to see what the smoke test does.
4. **Read "The EXAMPLE.md ↔ Reference App Tension"** to understand why we don't just commit a reference app.
5. **Read Extractor Design + End-to-End Smoke Flow** to see what the work looks like.
6. **Read Design Decisions** — all resolved; rationale captured inline.
7. **Follow Build Plan** in order; each step is independently shippable.

The decisions still to make:

- ✅ **Marker syntax** (Section 11.1): `{file=PATH}` on the language-tag line. Verified against GitHub's markdown API — annotation strips cleanly, syntax highlighting preserved.
- ✅ **Multi-block concat** (Section 11.2): append; first block carries package + imports.
- ✅ **Source docs** (Section 11.3 / 11.7): extract from EXAMPLE.md, ARCHITECTURE.md, CONFIG.md, DATABASE.md. Smoke-fixtures shrinks to `go.mod.tmpl`.
- ✅ **Scope** (Section 5 / 11.6): single full-bootstrap smoke test (Docker + Postgres + skimatik + tests). No staged tiers.
- **EXAMPLE.md vs reference app** (Section 6): Resolution C (extract from docs, no committed reference app).
- ✅ **Module substitution** (Section 11.4): keep `github.com/yourorg/myapp`, naive string replace.
- ✅ **Library pinning** (Section 11.5): pin every dep in `go.mod.tmpl`; lib-drift detection lives in a separate scheduled workflow.
- ✅ **Doc-only PR cost** (Section 11.8): always run smoke; small `paths-ignore` for known-irrelevant files; defer diff-based skip until PR volume justifies it.

The case still to evaluate:

- **Yes, build:** evidence that EXAMPLE.md or templates have drifted from working since LIBRARIES.md was written. The session that produced LIBRARIES.md surfaced six bugs of this exact shape — a smoke test prevents the recurrence.
- **No, skip:** if blueprint changes are infrequent enough that manual verification per change is sufficient, and the cost of one shipped breakage per year is acceptable.

The full-bootstrap smoke test is worth doing if blueprint conformance and canonical-doc correctness are both load-bearing — once it exists, every PR proves the canonical docs still represent a working service.
