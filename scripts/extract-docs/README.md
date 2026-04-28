# Template Smoke Testing — Maintainer Guide

> **Scope:** this directory is blueprint-maintainer infrastructure. If you're an agent reading the blueprint to build a service, ignore this README — the canonical patterns you need are in `EXAMPLE.md`, `ARCHITECTURE.md`, etc. Nothing here is meant to be copied into a consumer's repo.

The blueprint ships executable scaffolding (`templates/`) and canonical docs full of code blocks. The smoke test proves that a fresh service materialized from those docs actually compiles, lints, generates against current skimatik, and runs the daily loop end-to-end. Without it, library churn (`pgxkit`, `chikit`, `canonlog`, `skimatik`) silently invalidates the docs between blueprint releases.

This README is operational — how to run, debug, and extend the smoke setup. Design rationale and resolved decisions are captured inline in this doc and in commit history.

## TL;DR

```bash
make smoke         # full bootstrap + lint + verify + integration tests
make smoke-clean   # tear down docker, wipe build/smoke
```

## What `make smoke` does

The maintainer Makefile at the repo root orchestrates the flow. From a clean state it runs:

1. **Extract** canonical docs to `build/smoke/` via `go run ./scripts/extract-docs --docs EXAMPLE.md,ARCHITECTURE.md,CONFIG.md,DATABASE.md,ERRORS.md,API.md`. Every fenced code block annotated with `{file=PATH}` is written to its declared path; multiple blocks for the same file are concatenated in deterministic order. Module-path placeholder `github.com/yourorg/myapp` is rewritten to the smoke target (`github.com/example/smoketest`).
2. **Merge** `templates/*` into `build/smoke/` (Makefile, docker-compose.yml, .golangci.yml, .env.example, etc.) and render `examples/_smoke-fixtures/go.mod.tmpl` to `go.mod`.
3. **Seed** `.env` from `.env.example` so `viper.ReadInConfig` finds a config file.
4. **Install tools** (`make install-tools` from the consumer Makefile): skimatik v2, golangci-lint v2, blueprint-vet, mockgen, goimports, lefthook. Plus the standalone `migrate` CLI (smoke-only — bootstraps before `cmd/myapp` can compile).
5. **Postgres up** via `docker compose up -d postgres` and wait for `pg_isready`.
6. **Apply migrations** with the standalone `migrate` CLI directly. We can't use `go run ./cmd/myapp migrate up` here because `cmd/myapp` won't compile until `internal/repository/generated/` exists, which only happens after skimatik runs against an already-migrated schema. Hence the standalone migrate.
7. **`skimatik generate`** — produces `internal/repository/generated/`. Now everything compiles.
8. **`go mod tidy`** twice. The first pass resolves blueprint deps. After `go generate ./...` materializes `*_interface_mock.go` files (which import `go.uber.org/mock/gomock`), the second pass picks up that import.
9. **`goimports -w .`** — markdown blocks use spaces; Go expects tabs. The extractor doesn't normalize, so a one-pass goimports brings extracted files into compliance.
10. **`go build ./...`** — full compile.
11. **`make lint`** — golangci-lint with the pinned v2 config in `templates/.golangci.yml`.
12. **`make verify`** — `blueprint-vet` (excluding `internal/repository/generated/`) + `blueprint-sql-check`.
13. **`make migrate-up`** via `cmd/myapp` — re-runs the migrate command, this time through the compiled binary, to verify that path also works.
14. **`docker compose down -v`** — clean teardown.

If every step succeeds the script prints `✔ Smoke test green.` and exits 0.

## Where things live

| Path | Role |
|---|---|
| `scripts/extract-docs/` | The extractor (Go program). Owns `{file=...}` parsing, multi-block concatenation, module-path substitution, and the `// path/to/file` preamble stripper. |
| `scripts/extract-docs/testdata/minimal-doc.md` | Fixture used by `extractor_test.go`. Add cases here when you change parser behavior. |
| `examples/_smoke-fixtures/go.mod.tmpl` | The single non-doc fixture. Pinned versions of every dependency the smoke test resolves; `BLUEPRINT_TARGET_MODULE` placeholder gets substituted. |
| `Makefile` (repo root) | Maintainer Makefile. Orchestrates `make smoke` and `make smoke-clean`. Different from `templates/Makefile` (which is what consumers see). |
| `.github/workflows/template-smoke.yml` | Per-PR CI. Runs `make smoke` against pinned deps. |
| `.github/workflows/template-smoke-drift.yml` | Weekly cron + `repository_dispatch[lib-released]`. Re-runs the smoke flow with `go get -u ./...` and opens a `lib-drift` issue on failure. |
| `build/smoke/` | Smoke output. Gitignored; regenerated each run. |

## The `{file=PATH}` marker convention

The extractor only consumes fenced code blocks whose info string carries a `{file=PATH}` annotation:

```
```go {file=internal/models/product.go}
package models
…
```

GitHub's markdown renderer treats everything after the language tag as opaque, so the annotation strips cleanly in rendered docs and round-trips through CommonMark. Verified via GitHub's `POST /markdown` API.

Conventions inside extracted blocks:

- **First block per file carries `package X` + imports.** Subsequent blocks for the same file are body-only (types, functions, methods). Concatenated with `\n\n`. The smoke test catches violations because the file won't compile.
- **Don't put a leading `// path/to/file.go` comment** on the line right under the fence — the extractor strips it (revive's `package-comments` rule rejects non-`Package X` comments at the top of a file). Use a proper `// Package X …` doc comment instead, which is what the canonical packages do.
- **Module path placeholder.** Write imports as `github.com/yourorg/myapp/...`; the extractor rewrites to whatever `--module` is. Don't write that string in any non-import context — review-time discipline; violations would surface as a compile failure on substitution.

## Common failure modes (and what they mean)

| Failure | Likely cause |
|---|---|
| `failed to analyze SELECT query: expected N arguments, got M` | A query in `EXAMPLE.md`'s `products.sql` uses `COALESCE($N, col)` or `CASE … $N …` in a SET clause. skimatik can't see those parameters. Reformulate as a full update + service-layer merge. |
| `r.CreateProducts undefined` | Wrapper calls a method that doesn't exist on the embedded generated struct. skimatik v2 generates `Create`/`Get`/etc., not `CreateProducts`. Use qualified calls: `r.ProductsRepository.Create(...)`. |
| `not enough arguments in call to ...; want (...,*pgxkit.DB,...)` | Extracted code is using the v2 per-method-executor pattern but skimatik installed is < v2. Check `templates/Makefile`'s `install-tools` points at `github.com/nhalm/skimatik/v2/cmd/skimatik@latest`. |
| `you are using a configuration file for golangci-lint v2 with golangci-lint v1` | `install-tools` is grabbing v1 of golangci-lint. The v2 path is `github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`. |
| `package-comments: should have a package comment` (revive) | A package's primary file is missing `// Package X …`. Add one to the canonical block in the source doc. |
| `blueprint-vet: use canonlog instead of fmt.Sprintf` on `internal/repository/generated/*` | `make verify` is running over generated code. Verify `templates/Makefile`'s `verify` target filters with `go list ./... | grep -v /repository/generated`. |
| `failed to read config file: open .env: no such file or directory` | Smoke didn't seed `.env`. The maintainer Makefile copies `.env.example` to `.env` before installing tools — make sure that step ran. Or, if it ran but the consumer-facing `LoadLogging` doesn't tolerate a missing file, the docs need fixing (`fs.ErrNotExist` check alongside `viper.ConfigFileNotFoundError`). |
| `cannot find package github.com/example/smoketest/internal/repository/generated` | skimatik didn't run, or its output went somewhere else. Check `templates/skimatik.yaml` `output.directory: "./internal/repository/generated"`. |
| `unknown revision vX.Y.Z` for some lib in `go mod tidy` | Pin in `examples/_smoke-fixtures/go.mod.tmpl` is wrong. Run `go list -m -versions <module>` to find a real version. |

## Adding a new canonical file

If a new file should become extractable from the docs:

1. Pick the source doc (the one whose prose explains the pattern).
2. Wrap the code block with a `{file=path/to/new-file.go}` annotation on the language-tag line.
3. Make sure the block is a complete file — `package X` + imports in the first block, body-only in any continuation blocks. If the doc currently shows a fragment, expand it.
4. Run `make smoke` locally. Iterate until green.
5. Update the source-doc table in this README if the addition crosses doc boundaries.

## Adding a new source doc

If a new top-level doc should be a source for extraction:

1. Add `{file=...}` markers to the relevant code blocks.
2. Add the doc to `SMOKE_DOCS` in the maintainer `Makefile`.
3. Update the docs list in `.github/workflows/template-smoke.yml` and the drift workflow.
4. Run `make smoke` locally.

## Adding `paths-ignore` for a doc-only edit

If a path you're editing should never trigger smoke (e.g., a new top-level doc that has no extractable code), add it to `.github/workflows/template-smoke.yml`'s `paths-ignore` list. The smoke test's purpose is to gate code-block edits, not prose-only changes to non-extractable files.

## Bumping pinned versions

The PR-time smoke uses pinned versions in `examples/_smoke-fixtures/go.mod.tmpl` for determinism. To bump:

1. Edit the version in `go.mod.tmpl`.
2. Run `make smoke` locally to verify the bump compiles and tests pass.
3. Commit the bump as a standalone PR (so a regression is easily bisectable).

The drift workflow runs `go get -u ./...` weekly to surface upstream breakage; treat its issues as triggers for explicit pin bumps, not as automatic merges.

## When the smoke test catches drift

This is the design's payoff. When a smoke run fails after a doc edit or a library release, the failure points at a real divergence between what the docs claim and what the code does. Resist the urge to "fix the smoke" by relaxing rules or skipping steps — fix the underlying drift in the docs or the libraries instead.
