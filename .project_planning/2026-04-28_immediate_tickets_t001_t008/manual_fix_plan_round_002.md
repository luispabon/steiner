# Manual Fix Plan: Round 002 — @ Picker Overlay & Scrolling

## Issues Reported

1. **Picker hides everything else** — `lipgloss.Place` with `WithWhitespaceChars(" ")` fills the entire screen with spaces, creating a black background that hides all content behind it. The picker should be a floating panel rendered over existing content.

2. **Picker doesn't scroll** — When navigating past the 8 visible items with arrow keys, selection moves but viewport doesn't follow. Need viewport scrolling.

## Fixes Required

### Fix 1: Render picker as inline overlay (not full-screen Place)
In `internal/tui/model.go` `View()`:
- Remove the `lipgloss.Place` block that replaces the entire screen
- Instead, append the picker view to `mainComponents` before `inputView` (reverting to inline but keeping it visually above input)
- The picker itself should have a background style (`PaletteOverlay`) so it appears as a floating panel
- This keeps conversation content visible behind/beside it

### Fix 2: Add viewport scrolling
In `internal/tui/file_picker.go`:
- Add `scrollOffset int` field to `filePickerOverlay`
- When `selection` moves past `scrollOffset + maxDisplay - 1`, increment `scrollOffset` to follow
- When `selection` moves below `scrollOffset`, decrement `scrollOffset` to follow
- In `View()`, render candidates starting from `scrollOffset` instead of 0
- Reset `scrollOffset` to 0 on filter/query change

## Files
- `internal/tui/file_picker.go`
- `internal/tui/model.go`

## Verification
- `go test ./internal/tui/...`
- `go build ./...`
- `go vet ./...`
