# Manual Fix Plan: Round 004 — @ Picker Overlay Horizontal Slicing

## Issue
The overlay replaces entire lines, nuking the sidebar and any content to the right of the picker. When `baseLines[startY+i] = olLines[i]` runs, `olLines[i]` is only ~40-50 chars wide (the picker box), but the base line was full terminal width (main column + sidebar). Bubble Tea's renderer appends `EraseLineRight` to short lines, clearing everything to the right.

## Root Cause
`lipgloss.JoinHorizontal` creates lines where the left side is main column and right side is sidebar. Replacing the entire line with a short overlay string destroys the right portion.

## Fix
In `internal/tui/model.go`:
- Calculate overlay line width with `lipgloss.Width(olLines[i])`
- Use `ansi.TruncateLeft(baseLines[idx], olWidth, "")` to get the base line content to the RIGHT of the overlay
- Concatenate: `olLines[i] + baseRight`
- This preserves sidebar content while overlaying the picker on the left

`github.com/charmbracelet/x/ansi` is already in go.mod (indirect). Promote to direct if needed.

## Files
- `internal/tui/model.go`

## Verification
- `go test ./internal/tui/...`
- `go build ./...`
- `go vet ./...`
