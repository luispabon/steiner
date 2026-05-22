# Mouse Text Selection

## Request

Implement mouse-drag text selection in the TUI viewport, with clipboard copy support (y/c keys). Reference: charmbracelet/crush PR #563.

## Overview

The main conversation viewport (`viewport.Model`) renders a single large ANSI string. Text selection requires:

1. **Selection state** — track `(line, col)` start/end in content-space (absolute line index over the full rendered content, including top pad lines).
2. **Mouse drag** — on `MouseActionPress+Left` start selection; on `MouseActionMotion` (button held) update end; on `MouseActionRelease+Left` finalize (or treat as click if not moved).
3. **Highlight rendering** — post-process `viewport.View()` in `renderViewportView()`: for each visible line overlapping the selection, use `ansi.Cut(line, 0, startCol)` + `selectionStyle.Render(ansi.Strip(middle))` + `ansi.Cut(line, endCol, width)`. Selected segment is shown with flat highlight bg (strips local syntax color — matches typical terminal selection UX).
4. **Copy** — `y`/`c` key (when selection exists and composer not focused): strip ANSI from cached viewport lines, extract selected columns, write via `ansi.SetSystemClipboard` (OSC52 — works in SSH/tmux).
5. **Clear** — `Esc` when selection exists, or left-click without drag.

### Mouse mode change

`app.go:NewProgram` uses `tea.WithMouseCellMotion()` (mode 1002) but `model_init.go:Init()` immediately downgrades to mode 1000 via `mouseDowngradeCmd` with the intent: *"allowing the terminal to handle native text selection."*

Software selection requires mode 1002 for drag (`MouseActionMotion`) events. The change is: **remove `mouseDowngradeCmd` from `Init()`** — stay in mode 1002. Native terminal selection (click-drag in the terminal emulator) will no longer work, replaced by our software selection.

`Cleanup()` already disables 1000; bubbletea handles 1002 cleanup on exit. No change needed there.

### Coordinate mapping

Viewport renders inside a `ContentPane` with `PaddingTop(1)`, `PaddingLeft(3)`, `PaddingRight(3)`.
- Content line = `(termY - 1) + m.viewport.YOffset`
- Content col = `termX - leftPad - 3`, where `leftPad = sidebarWidth+1` if sidebar visible on left, else `0`

### Key design decisions

- **No new dependency**: `ansi.Cut(s, left, right int) string` is in `charmbracelet/x/ansi v0.10.1` (confirmed). `ansi.SetSystemClipboard(s string) string` (same package) for OSC52 clipboard.
- **Cache viewport lines**: `syncViewport()` stores `m.viewportLines []string` so copy extraction doesn't re-render.
- **Flat highlight in selection**: Strip ANSI for selected segment, re-render with selection bg color. Simpler than preserving nested ANSI; matches typical terminal selection behavior.
- **Selection scope**: Viewport content only (not input field, not overlays).
- **Double/triple click**: Out of scope for this plan.
- **New file `selection.go`**: Selection types, coordinate mapping, highlight logic, text extraction. Keeps `model_input.go` and `model_view.go` from accumulating selection concerns.

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | Format after each file |
| `go build ./...` | cheap | Compile check |
| `go vet ./...` | cheap | Static analysis |
| `go test ./internal/tui/...` | medium | Unit tests |
| `make check` | medium | Repo-mandated lint + tests |

Manual verification: launch TUI (`go run ./cmd/steiner`), drag to select, press `y`/`c`, paste elsewhere.

## Decision Log

- Use `ansi.Cut` not ultraviolet — avoids new dependency, sufficient for line-based highlighting
- OSC52 via `ansi.SetSystemClipboard` — same package, works SSH/tmux
- Remove `mouseDowngradeCmd` — required for mode 1002 motion events; trades native terminal selection for software selection
- Selection in content-space coordinates — stable across scroll position changes
- Cache `viewportLines` in `syncViewport` — avoids re-rendering for copy extraction
- Flat highlight for selected segment — simpler, consistent with most terminal selection behavior
