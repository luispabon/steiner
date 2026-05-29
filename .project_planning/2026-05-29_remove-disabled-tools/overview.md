## Request

Remove three disabled built-in tools (`write`, `edit`, `apply_patch`) and all their dead collateral code. These tools were superseded by `mutate` and are no longer registered in `Builtins()`.

## Overview

Steiner's `internal/tool/builtin/` contains implementation files, schemas, input structs, result types, tests, and policy branches for three tools that are never exposed to models:

| Tool | Superseded by | Files |
|------|--------------|-------|
| `write` | mutate `create`/`write` ops | `write.go`, `write_test.go` |
| `edit` | mutate `replace`/`line_replace` ops | `edit.go`, `edit_test.go` |
| `apply_patch` | mutate (all ops) | `apply_patch.go`, `apply_patch_test.go`, entire `patchdoc/` subpackage (14 files) |

Dead collateral spans `schema.go`, `input.go`, `result.go`, `policy.go`, `policy_test.go`, and various test files.

**Key complication**: `edit.go` contains ~150 lines of diagnostic/matching helpers (`buildNoMatchDiagnostics`, `buildAmbiguousDiagnostics`, whitespace-match detection, anchor search, context preview) that `mutate.go` calls for its `replace` operation. These survive in a new `mutate_diagnostics.go`.

**Not in scope**: `web_search` (conditionally registered in `runner.go` when search backend configured), `brave_search`, `search_backend`, `fetch_url` — all active.

## Verification Strategy

| Check | Command | Cost |
|-------|---------|------|
| Format | `gofmt -w <files>` | cheap |
| Imports | `goimports -w <files>` | cheap |
| Vet | `go vet ./...` | cheap |
| Test | `go test ./...` | medium |
| Build | `go build ./...` | medium |
| Full gate | `make check` | medium-expensive |

Per-step: `go build ./...` + `go test ./internal/tool/...` after each step.
Final: `make check`.

## Decision Log

- 2026-05-29: Diagnostic helpers from `edit.go` → `mutate_diagnostics.go` (user chose dedicated file over inlining into mutate.go)
- 2026-05-29: `web_search` confirmed active (conditionally registered), excluded from scope
