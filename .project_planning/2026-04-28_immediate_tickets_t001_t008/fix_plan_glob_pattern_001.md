# Fix Plan: Glob Pattern Matching Regression (fix-glob-pattern-001)

## Problem
The custom `globWalk` in `internal/tool/builtin/glob.go` uses `filepath.Match(pattern, d.Name())` which matches against the **base filename only**. This breaks common glob patterns:
- `**/*.go` — `filepath.Match` doesn't support `**`
- `src/**/*.ts` — path separators can't match base names
- `*.{js,ts}` — `filepath.Match` doesn't support `{a,b}` alternation

## Root Cause
Dive's original `GlobTool` used `gobwas/glob` to compile patterns and match against **full relative paths**. Our replacement only matches base names.

## Fix
1. Replace `filepath.Match` with `gobwas/glob.Compile` in `globWalk`
2. Match against the full relative path (converted to forward slashes)
3. Add early-termination cap: stop walk when `len(matches) >= maxGlobLimit` (1000)
4. Update `GlobSchema` descriptions to mention defaults explicitly for model visibility

## Files
- `internal/tool/builtin/glob.go`
- `internal/tool/builtin/schema.go`

## Verification
- `go test ./internal/tool/builtin/... -run TestGlob`
- All existing glob tests must pass
- New tests for `**/*.go` and complex patterns
- `go build ./...`
- `go vet ./...`

## Dependencies
- `github.com/gobwas/glob` (already in go.mod as indirect)
