# Execution Log: Immediate Tickets T001–T008

## Active Branch

`cl/2026-04-28_immediate_tickets_t001_t008`

## Verification Strategy

Loaded from `overview.md`.

- **Format**: fix mode preferred (`gofmt -w <files>`)
- **Lint**: check mode (`go vet ./...`)
- **Unit Tests**: check mode (`go test ./internal/config/...`, `go test ./internal/tool/...`, `go test ./internal/tui/...`)
- **Full Test Suite**: check mode (`go test ./...`)
- **Build**: check mode (`go build ./...`, `make build-binaries`)
- **Execution timing**: deferred until end of implementation
- **Stage exception**: Format must run after any Go file edit

## Execution State

### Steps

| Step | Status | Notes |
|------|--------|-------|
| stage-1-step-1 | implemented | Config types update — 167 tests pass, go vet clean |
| stage-1-step-2 | implemented | Consumer updates — 785 tests pass, build + vet clean |
| stage-2-step-1 | implemented | ThinkingChunk toggle — config + output tests pass, no deviations |
| stage-3-step-1 | implemented | Path exclusion config + PathExcluder — config + tool tests pass, no deviations |
| stage-3-step-2 | implemented | Custom glob walker — 212 builtin tests pass, build + vet clean |
| fix-glob-pattern-001 | implemented | Fix glob pattern matching regression — switched to gobwas/glob for full-path matching, added early-termination cap, updated schema descriptions |
| stage-3-step-3 | implemented | Custom grep walker — copied Dive core (Apache 2.0), stripped boilerplate, integrated PathExcluder — 834 tests pass |
| stage-4-step-1 | pending | Conversation scrollbar |
| stage-5-step-1 | pending | /ls overlay |
| stage-6-step-1 | pending | @ file picker |

### Current Phase

Step scheduling complete. Ready to begin implementation.

## Sub-Agent Log

| Step | Model | Worktree | Branch | Merged | Closed |
|------|-------|----------|--------|--------|--------|
| stage-1-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-1-step-1 | tmp/stage-1-step-1 | Yes | Yes |
| stage-1-step-2 | kimi-k2.6 (same tier) | .worktrees/stage-1-step-2 | tmp/stage-1-step-2 | Yes | Yes |
| stage-2-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-2-step-1 | tmp/stage-2-step-1 | Yes | Yes |
| stage-3-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-3-step-1 | tmp/stage-3-step-1 | Yes | Yes |
| stage-3-step-2 | kimi-k2.6 (same tier) | .worktrees/stage-3-step-2 | tmp/stage-3-step-2 | Yes | Yes |
| fix-glob-pattern-001 | kimi-k2.6 (same tier) | .worktrees/fix-glob-pattern-001 | tmp/fix-glob-pattern-001 | Yes | Yes |
| stage-3-step-3 (grep-exclusions) | kimi-k2.6 (same tier) | .worktrees/grep-exclusions | tmp/grep-exclusions | Yes | Yes |

## Verification Runs

| Run | Trigger | Commands | Result | Notes |
|-----|---------|----------|--------|-------|
| 001 | post-stage-3 | go test ./... | 838 passed in 15 packages | All tool tests pass including new glob + grep exclusion tests |
| 002 | post-stage-3 | go vet ./... | clean | No issues |
| 003 | post-stage-3 | go build ./... | success | All packages compile |
| 004 | post-stage-3 | make build-binaries | success | Binary built |

## Fix Plans

| Pass | File | Result |
|------|------|--------|
| fix-glob-pattern-001 | fix_plan_glob_pattern_001.md | Fixed — gobwas/glob full-path matching + early-termination cap + schema description updates |

## Manual Verification

| Round | User Response | Notes |
|-------|---------------|-------|
| — | — | — |

## Blockers / Deviations

- **stage-1-step-2 deviation**: Sub-agent discovered that `stage-1-step-1` left `configPatch.Model` as `*modelPatch`, which cannot decode YAML scalar strings like `model: alias`. Fixed by adding `ModelAlias string` to `configPatch` and handling alias resolution in `readConfigPatch` before decoding with `KnownFields`. This is a necessary backward-compatibility fix within the planned scope.
- **stage-3-step-3 approach revised**: After exploration of Dive's `grep.go` source, the original plan (custom walker or rg wrapper) was revised. The new approach: copy Dive's `toolkit/grep.go` core (~150 lines of walk + regex + formatting), strip Dive boilerplate (ripgrep, PathValidator, schema), integrate `PathExcluder`, and adapt to our `GrepInput`/`GrepResult` types. Apache 2.0 license permits copying with attribution.
- **stage-3-step-2 regression discovered**: Exploration of Dive's `glob.go` revealed our custom `globWalk` uses `filepath.Match` on base filenames only, which breaks patterns like `**/*.go`, `src/**/*.ts`, `*.{js,ts}`. Fix: replace with `gobwas/glob` (already in go.mod as indirect dependency) and match against full relative paths. Also add early-termination cap at `maxGlobLimit` (1000).

## Final Handoff State

Not yet complete.
