# Fix Plan — Verification Pass 002

## Trigger

`make check` failed on `cl/2026-05-16_model_provider_refactoring` after merging `stage-9-step-1`.

## Failing Check

- `golangci-lint run ./...`

## Reported Issues From This Pass

### Changed or feature-scope files

- `cmd/steiner/cmd_model.go`
  - unchecked `fmt.Fprintf` return values
- `cmd/steiner/cmd_model_metadata.go`
  - unchecked `fmt.Fprintln` return values
  - ineffectual initial assignment to `freshness`
- `internal/metadata/cache.go`
  - unchecked `tmp.Close()` on an error path
- `internal/provider/discovery_ollama.go`
  - unchecked `resp.Body.Close()`
- `internal/provider/discovery_openrouter.go`
  - unchecked `resp.Body.Close()`
- `internal/provider/resolved_model.go`
  - `resolveTokenizerMetadataWithLoader` cyclomatic complexity too high
- `internal/provider/resolved_model_test.go`
  - `goimports` formatting required
- `internal/provider/token_counter.go`
  - exported tokenizer strategy constants need comments
- `internal/metadata/cache_test.go`
  - unused request parameter names in `httptest` handlers

### Branch-local lint blockers outside the immediate step diff but still blocking `make check`

- `internal/config/config.go`
  - exported provider type constants need a proper comment block
- `internal/config/patch_test.go`
  - unused helper functions:
    - `stringSlicePtr`
    - `durationMapPtr`
    - `modelPatchMapPtr`
    - `providerPatchMapPtr`
    - `toolPatchMapPtr`

## Scope

Keep fixes limited to the concrete lint/fmt issues reported above. Do not broaden into unrelated cleanup or behavior changes.

## Required Outcome

- Resolve the listed lint and formatting blockers.
- Preserve existing behavior.
- Keep the resulting changes narrow and mechanical where possible.

## Verification

- `gofmt -w` / `goimports -w` on touched Go files
- rerun the narrowest useful checks for touched packages if helpful
- rerun `make check`
