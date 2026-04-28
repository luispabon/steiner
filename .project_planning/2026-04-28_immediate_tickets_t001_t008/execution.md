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
| stage-4-step-1 | implemented | Conversation scrollbar — TUI tests pass, visual-only Lipgloss scrollbar |
| stage-5-step-1 | implemented | /ls overlay — 854 tests pass, file listing modal with exclusions |
| stage-6-step-1 | implemented | @ file picker — 870 tests pass, substring-filtered file picker above input |
| manual-fix-001 | implemented | @ picker visual fixes: float overlay, accent bg selection, accent folder color, maxWidth fix |
| manual-fix-002 | implemented | @ picker overlay fix (inline not full-screen), viewport scrolling for long candidate lists |
| manual-fix-003 | implemented | @ picker true floating overlay via string-level line replacement, header mirrors selected entry |
| manual-fix-004 | implemented | @ picker overlay horizontal slicing fix — preserves sidebar content to the right of picker |

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
| stage-4-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-4-step-1 | tmp/stage-4-step-1 | Yes | Yes |
| stage-5-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-5-step-1 | tmp/stage-5-step-1 | Yes | Yes |
| stage-6-step-1 | kimi-k2.6 (same tier) | .worktrees/stage-6-step-1 | tmp/stage-6-step-1 | Yes | Yes |
| manual-fix-001 | kimi-k2.6 (same tier) | .worktrees/manual-fix-001 | tmp/manual-fix-001 | Yes | Yes |
| manual-fix-002 | kimi-k2.6 (same tier) | .worktrees/manual-fix-002 | tmp/manual-fix-002 | Yes | Yes |
| manual-fix-003 | kimi-k2.6 (same tier) | .worktrees/manual-fix-003 | tmp/manual-fix-003 | Yes | Yes |
| manual-fix-004 | kimi-k2.6 (same tier) | .worktrees/manual-fix-004 | tmp/manual-fix-004 | Yes | Yes |

## Verification Runs

| Run | Trigger | Commands | Result | Notes |
|-----|---------|----------|--------|-------|
| 001 | post-stage-3 | go test ./... | 838 passed in 15 packages | All tool tests pass including new glob + grep exclusion tests |
| 002 | post-stage-3 | go vet ./... | clean | No issues |
| 003 | post-stage-3 | go build ./... | success | All packages compile |
| 004 | post-stage-3 | make build-binaries | success | Binary built |
| 005 | end-of-implementation | go test ./... | 870 passed in 15 packages | Final verification — all tests pass |
| 006 | end-of-implementation | go vet ./... | clean | No issues |
| 007 | end-of-implementation | go build ./... | success | All packages compile |
| 008 | end-of-implementation | make build-binaries | success | Binary built |
| 009 | post-manual-fix-003 | go test ./internal/tui/... | 102 passed in 3 packages | TUI tests pass after overlay + preview fixes |
| 010 | post-manual-fix-003 | go vet ./... | clean | No issues |
| 011 | post-manual-fix-003 | go build ./... | success | All packages compile |
| 012 | post-manual-fix-003 | make build-binaries | success | Binary built |
| 013 | post-manual-fix-004 | go test ./internal/tui/... | 103 passed in 3 packages | TUI tests pass after horizontal slicing fix |
| 014 | post-manual-fix-004 | go vet ./... | clean | No issues |
| 015 | post-manual-fix-004 | go build ./... | success | All packages compile |
| 016 | post-manual-fix-004 | make build-binaries | success | Binary built |

## Fix Plans

| Pass | File | Result |
|------|------|--------|
| fix-glob-pattern-001 | fix_plan_glob_pattern_001.md | Fixed — gobwas/glob full-path matching + early-termination cap + schema description updates |
| manual-fix-001 | manual_fix_plan_round_001.md | Fixed — picker floated as overlay, accent bg for selection, accent color for folders, MaxWidth prevents overflow |
| manual-fix-002 | manual_fix_plan_round_002.md | Fixed — picker rendered inline (not full-screen Place), scrollOffset field for viewport scrolling |
| manual-fix-003 | manual_fix_plan_round_003.md | Fixed — true floating overlay via string-level line replacement, header mirrors selected candidate path |
| manual-fix-004 | manual_fix_plan_round_004.md | Fixed — overlay uses ansi.TruncateLeft to preserve sidebar content to the right of picker |

## Manual Verification

| Round | User Response | Notes |
|-------|---------------|-------|
| 001 | Issues reported: picker inline (not overlay), selected row bg overflow hides next line, folders amber (not accent), selected item too subtle | Fix dispatched: lipgloss.Place overlay, MaxWidth fix, Accent/AccentBg styles |
| 002 | Issues reported: picker full-screen black background hides content, no scrolling past 8 items | Fix dispatched: removed lipgloss.Place full-screen, inline render; added scrollOffset viewport |
| 003 | Issues reported: picker pushes sidebar/content up (not true overlay), search box should mirror selected entry | Fix dispatched: string-level line replacement overlay (research confirmed as standard Bubble Tea pattern), header shows selected candidate path in accent style |
| 004 | Issues reported: overlay nukes entire horizontal slice (sidebar disappears), search box preview works fine | Fix dispatched: ansi.TruncateLeft preserves base line content to the right of overlay width |
| 005 | — | Pending re-verification |

## Blockers / Deviations

- **stage-1-step-2 deviation**: Sub-agent discovered that `stage-1-step-1` left `configPatch.Model` as `*modelPatch`, which cannot decode YAML scalar strings like `model: alias`. Fixed by adding `ModelAlias string` to `configPatch` and handling alias resolution in `readConfigPatch` before decoding with `KnownFields`. This is a necessary backward-compatibility fix within the planned scope.
- **stage-3-step-3 approach revised**: After exploration of Dive's `grep.go` source, the original plan (custom walker or rg wrapper) was revised. The new approach: copy Dive's `toolkit/grep.go` core (~150 lines of walk + regex + formatting), strip Dive boilerplate (ripgrep, PathValidator, schema), integrate `PathExcluder`, and adapt to our `GrepInput`/`GrepResult` types. Apache 2.0 license permits copying with attribution.
- **stage-3-step-2 regression discovered**: Exploration of Dive's `glob.go` revealed our custom `globWalk` uses `filepath.Match` on base filenames only, which breaks patterns like `**/*.go`, `src/**/*.ts`, `*.{js,ts}`. Fix: replace with `gobwas/glob` (already in go.mod as indirect dependency) and match against full relative paths. Also add early-termination cap at `maxGlobLimit` (1000).

## Final Handoff State

All planned implementation steps are implemented and automated verification is passing. Pending manual verification checkpoint before reviewer handoff.
