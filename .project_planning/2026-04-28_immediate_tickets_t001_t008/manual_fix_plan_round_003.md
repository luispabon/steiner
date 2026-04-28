# Manual Fix Plan: Round 003 — @ Picker True Overlay + Selection Preview

## Issues Reported

1. **Picker pushes content up** — Inline rendering causes the viewport and sidebar to shift. User wants a true floating overlay that draws OVER existing content without pushing it.

2. **Search box should mirror selected entry** — As user scrolls through picker, the header should show the currently selected path (like IDE command palettes).

## Research: Bubble Tea Overlay Patterns

Bubble Tea / Lipgloss has no native layering. The standard patterns:
- `lipgloss.Place(width, height, x, y, content, WithWhitespaceChars(" "))` — fills entire area, replacing everything
- Inline layout — content flows vertically, pushes everything
- **String-level compositing** — render background, render overlay, merge by replacing lines

The palette overlay in the codebase uses the first pattern (full-screen replace). For the picker, we need the third pattern: merge the picker into the bottom of the rendered view.

## Fixes Required

### Fix 1: True overlay via string-level compositing
In `internal/tui/model.go` `View()`:
- Render the normal view first (without picker)
- If picker is open, render the picker
- Merge: replace the bottom N lines of the normal view with the picker lines
- This makes the picker appear to float over the bottom of the viewport without pushing anything

Helper approach:
```go
func overlayAtBottom(background, overlay string) string {
    bgLines := strings.Split(background, "\n")
    olLines := strings.Split(overlay, "\n")
    start := len(bgLines) - len(olLines)
    for i, line := range olLines {
        if start+i >= 0 && start+i < len(bgLines) {
            bgLines[start+i] = line
        }
    }
    return strings.Join(bgLines, "\n")
}
```

- The viewport keeps its FULL height (don't subtract picker height)
- The picker covers the bottom portion of the viewport + input area
- Sidebar remains unchanged

### Fix 2: Header mirrors selected entry
In `internal/tui/file_picker.go` `View()`:
- When `f.query == ""` and `len(f.candidates) > 0`, show the selected candidate path in the header instead of "search files…"
- When query is non-empty, show query as before
- This gives immediate visual feedback of what's selected

## Files
- `internal/tui/file_picker.go`
- `internal/tui/model.go`

## Verification
- `go test ./internal/tui/...`
- `go build ./...`
- `go vet ./...`
