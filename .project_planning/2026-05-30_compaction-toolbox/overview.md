## Request

Replace the compaction progress banner in the conversation view with a delegation-box-style tool box:
- Spinner while active (braille frames), green ✓ when finished
- Remove the fake progress bar
- Collapsed by default; on expand show pretty-printed compaction result
- Delegation-box layout: header with elapsed time + compaction count right-aligned, divider, key/value table, footer stats row
- Warn/orange border (existing compaction accent)

## Overview

The compaction banner currently renders as a full-width bar with a fake progress fill animation (`content_render_chrome.go:96-148`). It uses `compactionBannerData` with `progress`/`pct` fields and a tick-driven fill pattern. The finished state renders as a single `▼` prefixed line.

This change replaces both states with a bordered box matching the delegation box pattern:

**Data model** — Extend `compactionBannerData` in `content_events.go` with:
- `startTime int64` (unix nano, set on compacting start)
- `elapsed string` (formatted on finish)
- `spinnerFrame int` (braille index, advanced by tick)
- `compactionCount int`
- Full diagnostics fields from `ContextDiagnosticsEvent`: `compactedTurns`, `compactedMessages`, `retainedTurns`, `retainedMessages`, `mode`, `beforeTokens`, `beforePct`, `afterTokens`, `afterPct`, `summaryTitle`
- `collapsed bool` (default true)

**Event handling** — Update `handleCompactionDiagnostics` in `content_events_approval_diagnostics.go` to:
- Set `startTime` on compacting start
- Populate all diagnostics fields on finish
- Compute `elapsed` from `startTime` to `time.Now()`
- Pass `CompactionCount` through

**Rendering** — Replace `renderCompactionBanner` in `content_render_chrome.go` with a new renderer that:
- Renders a bordered box using `lipgloss.NormalBorder()` with warn-colored border
- Header: `▸/▾ compaction  <subtitle>  <spinner|✓> <elapsed> · #<count>`
- Collapsed: header only
- Expanded: divider + key/value rows (compacted, retained, mode, before, after, summary) + divider + footer stats row
- Reuses existing `spinnerFrames` for active state

**Spinner tick** — Add compaction spinner advancement in `model_update.go` alongside delegation spinners. The content buffer already re-renders active compaction banners each tick (`content_render.go:20-21`).

**Collapse toggle** — Wire click/keybind on compaction box header (same as tool call toggle) through `collapseState` map.

### Scope boundaries

- No changes to `ContextDiagnosticsEvent` struct or event emission
- No changes to sidebar compaction display or activity row
- No changes to non-TUI output rendering (`internal/output/`)
- Compaction box does NOT support expand/collapse via ctrl+x (that's delegation-only); uses standard click toggle like tool calls

### Risks

- Compaction events may arrive without timing data (e.g. replayed from history) — elapsed shows nothing, graceful fallback
- `CompactionCount` is only available from session_health events in some flows — may show as `#0`, acceptable

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | formatter, fix mode |
| `goimports -w <files>` | cheap | import organizer, fix mode |
| `go vet ./internal/tui/...` | cheap | static analysis |
| `go test ./internal/tui/ -run <TestName>` | cheap | targeted tests |
| `go test ./internal/tui/` | medium | full TUI package |
| `go build ./...` | medium | compilation check |
| `golangci-lint run ./internal/tui/...` | medium | lint |
| `make check` | medium-expensive | repo-mandated umbrella (run before finalizing) |

Preferred order: format → targeted tests → build → lint → `make check` at end.

## Decision Log

| Decision | Rationale |
|----------|-----------|
| Delegation-box layout (Option B) | User chose: richer metadata, elapsed time, compaction count, divider separators, footer stats row |
| Warn/orange border | User chose: keep visual distinction from regular tool calls |
| No research needed | Fully repo-local, all patterns exist in codebase |
| Track elapsed in banner data | `ContextDiagnosticsEvent` doesn't carry timing; compute from start→finish in TUI layer |
| Reuse `spinnerFrames` | Same braille sequence as delegations — consistent spinner style |
