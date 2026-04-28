# Manual Fix Plan: Round 001 — @ Picker Visual Issues

## Issues Reported

1. **Picker opens inside prompt box**, pushing all content up. Should float over everything.
2. **Line below selected file disappears** — dark background overflows to next line and hides it.
3. **Folders highlighted in amber** — should use the current accent colour.
4. **Selected file not visible enough** — dark background (`accentSoft`) isn't cutting it.

## Fixes Required

### Fix 1: Float picker as overlay instead of inline
In `internal/tui/model.go` `View()`:
- Remove picker from `mainComponents` vertical flow
- Render picker using `lipgloss.Place` to position it absolutely over the main content area
- Position above the input area (bottom-aligned within the content pane)

### Fix 2: Fix background overflow on selected item
In `internal/tui/file_picker.go` `View()`:
- The `PaletteItemActive` style background is leaking. Each row must be rendered as a complete block.
- Ensure selected row is wrapped in a style that doesn't overflow to adjacent lines.
- Consider using `MaxWidth(innerWidth)` on each row style.

### Fix 3: Use accent colour for folders
In `internal/tui/file_picker.go` `View()`:
- Replace hardcoded `theme.AccentAmber` for folders with `f.styles.Accent` (which uses the current theme accent).

### Fix 4: Make selected item more visible
In `internal/tui/file_picker.go` `View()`:
- Replace `PaletteItemActive` (background `accentSoft`) with a more visible style.
- **Suggestion**: Use `AccentBg` (accent background + black foreground) for the selected row, or add an accent-coloured left border bar (`▎`) before the selected item text.
- **Preferred**: Accent background for the full selected row width, with black text for contrast.

## Files
- `internal/tui/file_picker.go`
- `internal/tui/model.go`

## Verification
- `go test ./internal/tui/...`
- `go build ./...`
- `go vet ./...`
