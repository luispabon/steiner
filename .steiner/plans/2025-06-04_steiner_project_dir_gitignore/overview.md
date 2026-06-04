## Request

GitHub issue #105: Steiner should create its own project-level `.steiner` folder on startup if it doesn't exist, and add inside a `.gitignore` for itself to ensure the whole folder is ignored by git. If the file already exists, skip it.

## Overview

Currently, the `.steiner/` directory is never explicitly created by production code. Only `.steiner/home/` is created implicitly via `os.MkdirAll` inside `sandbox.EnsureHome()`. The project-level `.steiner/` directory is expected to hold `config.yaml`, `skills/`, and `plans/`, but Steiner does not ensure it exists or that it is git-ignored.

This change adds a single initialization step to `defaultBuildRuntime()` in `cmd/steiner/runtime.go` (called by both exec and interactive modes) that:

1. Ensures a `.steiner/` directory exists in the current working directory (`os.MkdirAll` with `0o755`).
2. Ensures a `.gitignore` file exists inside `.steiner/` with content `*` to ignore all contents.
3. If the `.gitignore` already exists, it is left untouched.

The helper will live in `cmd/steiner/runtime_build.go` to keep the orchestration function in `runtime.go` clean.

### Scope boundaries

- Only the project-level `.steiner/` directory in the current working directory is affected.
- No changes to user-level `~/.steiner/` or `~/.config/steiner/` paths.
- No changes to sandbox `.steiner/home/` behavior.
- No deletion or modification of existing `.gitignore` files.

### Risks and tradeoffs

- **Risk:** If the working directory is read-only, `os.MkdirAll` will fail and Steiner will error on startup. This is acceptable — a missing `.steiner/` is not fatal to Steiner's core operation, but the issue treats it as a startup requirement. We will return the error so the user knows why.
- **Risk:** The `.gitignore` content `*` is minimal and may not match all use cases (e.g., users who want to check in `.steiner/config.yaml`). However, the issue explicitly requests that the whole folder be ignored, so `*` is the correct minimal choice.
- **Assumption:** This behavior should run for both exec (`--exec`) and interactive modes because both flow through `defaultBuildRuntime()`.

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w cmd/steiner/runtime_build.go cmd/steiner/runtime_build_test.go` | Cheap | Format new and changed files |
| `goimports -w cmd/steiner/runtime_build.go cmd/steiner/runtime_build_test.go` | Cheap | Fix imports |
| `go test ./cmd/steiner -run TestEnsureSteinerProjectDir` | Cheap | New unit tests for the init helper |
| `go test ./cmd/steiner` | Medium | Existing cmd tests still pass |
| `go test ./...` | Medium | Full suite |
| `go test -race ./...` | Medium | Race detection |
| `go vet ./...` | Cheap | Static analysis |
| `make build-binaries` | Cheap | Build smoke test |
| `golangci-lint run ./...` | Expensive | Full lint |

Executor should run targeted tests first, then broad checks.

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Hook into `defaultBuildRuntime()` | Both exec and interactive modes use this function; it's the canonical startup orchestration point. |
| 2 | Place helper in `runtime_build.go` | Keeps `runtime.go` focused on orchestration; `runtime_build.go` already houses `buildRuntimeSandbox` and similar init helpers. |
| 3 | Create `.gitignore` with content `*` | Minimal pattern that ignores everything inside `.steiner/`, matching the issue requirement to "ensure the whole folder is ignored by git." |
| 4 | Skip if `.gitignore` already exists | Matches issue requirement exactly; avoids overwriting user customizations. |
| 5 | Add dedicated unit tests in `runtime_build_test.go` | `runtime_build.go` currently has no dedicated tests; this is a good opportunity to add isolated coverage for the new helper. |
