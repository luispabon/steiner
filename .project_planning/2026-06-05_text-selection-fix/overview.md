## Request

Fix GitHub issue #121: text selection offset at small terminal heights. When selecting text, the selection appears 1-3 rows above the mouse cursor. The offset increases as the terminal gets shorter.

## Overview

### Root cause: frame height overflow

The TUI frame can exceed `m.height` when the terminal is short enough. The chain:

1. `layout()` clamps viewport height: `max(1, m.height - 3 - inputRows - activityRows)`
2. When clamped to 1, total chrome (viewport pane + hDivider + activity + input + status) = `5 + inputRows`, which exceeds `m.height`
3. `renderMainColumn` uses `lipgloss.Height(m.height)` — but lipgloss `Height()` only **pads** short content; it **never truncates** (confirmed in `lipgloss/align.go:64-66`)
4. The oversized frame is written to the alternate screen buffer. The terminal scrolls it up by `(frame_lines - m.height)` rows
5. Mouse Y coordinates become offset by that amount — selection appears above the cursor

**Concrete example**: terminal 80×15, user typed 10 input lines → `inputRows=12`, viewport clamps to 1, frame = 17 lines, overflow = 2. Selection is 2 rows above mouse.

### Secondary bug: off-by-one line counting in syncViewport

`syncViewport` counts content lines as `strings.Count(rendered, "\n")` — this counts newline **separators** (N-1 for N lines). Lipgloss correctly uses `strings.Count(s, "\n") + 1`. The result:

- `contentTopPad` is always 1 too large
- Viewport content is always `viewport.Height + 1` lines
- `viewport.TotalLineCount() > viewport.Height` is always true → scrollbar always shown
- ContentPane path (no scrollbar) is never taken

The +1 doesn't affect selection coordinates (it cancels with the +1 YOffset in the scrollbar path), but it creates a cosmetic bug: scrollbar always visible, even with minimal content.

### Same off-by-one in segmentHeights

`content_render.go` uses `strings.Count(rendered, "\n")` for `segmentHeights`. This affects `handleLeftClick` (click-to-collapse) accuracy, not text selection. Fix alongside.

## Verification Strategy

| Check | Command | Cost |
|-------|---------|------|
| Format | `gofmt -w <files>` | cheap |
| Imports | `goimports -w <files>` | cheap |
| Vet | `go vet ./...` | cheap |
| Lint | `golangci-lint run ./...` | medium |
| Unit tests | `go test ./internal/tui/ -run <TestName>` | cheap |
| Full tests | `go test ./...` | medium |
| Race detector | `go test -race ./...` | expensive |
| Build | `go build ./...` | cheap |
| Full check | `make check` | expensive |

Prefer targeted tests during development, `make check` before finalizing.

## Decision Log

- **MaxHeight vs layout rework**: Use `MaxHeight(m.height)` for the frame clamp — it's the smallest correct fix and prevents all overflow. A graceful chrome reduction for tiny terminals is a separate enhancement (not needed for this bug).
- **Fix contentLines counting**: Change to `strings.Count(rendered, "\n") + 1` in both `syncViewport` and `content_render.go`. This removes the always-visible-scrollbar cosmetic bug and restores the ContentPane path for short content.
- **No research needed**: All bugs are repo-local with clear fixes. No external dependencies or APIs involved.
