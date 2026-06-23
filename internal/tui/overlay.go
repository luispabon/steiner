package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// OverlayShell is a reusable framed overlay chrome shared by concrete overlays
// (e.g. filePickerOverlay, context viewer) that are placed over the base TUI
// model.  It handles open/closed state, dimensions, the title/header line, a
// horizontal divider below the header, the padded body region, footer help-chip
// rendering, and bottom-anchored placement over a base view string.
//
// Concrete overlays embed OverlayShell, add their own state, and delegate frame
// rendering via Render.  Placement in the parent Model.View is handled by
// PlaceBottomAnchored.
type OverlayShell struct {
	// open tracks whether the overlay is currently visible.
	open bool
	// width and height are the terminal dimensions passed in from the parent.
	width  int
	height int
	// title is displayed in the overlay header.
	title string
	// preferredWidth is the ideal width for this overlay, clamped to [40, width-4].
	// 0 means use the default (width-4, min 40).
	preferredWidth int
}

// IsOpen reports whether the overlay is currently visible.
func (o OverlayShell) IsOpen() bool {
	return o.open
}

// WithDimensions returns a copy of the overlay shell with updated terminal dimensions.
func (o OverlayShell) WithDimensions(width, height int) OverlayShell {
	o.width = width
	o.height = height
	return o
}

// WithTitle returns a copy of the overlay shell with the given title.
func (o OverlayShell) WithTitle(title string) OverlayShell {
	o.title = title
	return o
}

// WithPreferredWidth returns a copy of the overlay shell with the given preferred width.
func (o OverlayShell) WithPreferredWidth(w int) OverlayShell {
	o.preferredWidth = w
	return o
}

// open returns a copy of the shell in the open state.
func (o OverlayShell) openShell() OverlayShell {
	o.open = true
	return o
}

// closeShell returns a copy of the shell in the closed state.
func (o OverlayShell) closeShell() OverlayShell {
	o.open = false
	return o
}

// overlayWidth computes the width of the framed box from the terminal width.
func (o OverlayShell) overlayWidth() int {
	w := o.width - 4
	if o.preferredWidth > 0 && o.preferredWidth < w {
		w = o.preferredWidth
	}
	if w < 40 {
		w = 40
	}
	return w
}

// InnerWidth returns the usable content width inside the box padding.
func (o OverlayShell) InnerWidth() int {
	return o.overlayWidth() - 4
}

// Divider returns a horizontal rule string sized to the inner width.
func (o OverlayShell) Divider() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", o.InnerWidth()))
}

// FooterChip renders a single key-hint label using the shared chip style.
func FooterChip(label string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev2)).
		Foreground(lipgloss.Color(theme.FgFaint)).
		Padding(0, 1).
		Render(label)
}

// RenderFooter renders a footer bar from a pre-assembled footer text string,
// sized to the inner width.
func (o OverlayShell) RenderFooter(footerText string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(o.InnerWidth()).Render(footerText)
}

// Render wraps body lines (already joined) in the shared overlay frame — a
// bordered box with padding — and returns the complete framed overlay string.
// The caller is responsible for assembling body lines (header, divider, list,
// footer divider, footer) before calling Render.
func (o OverlayShell) Render(box lipgloss.Style, body string) string {
	return box.Width(o.InnerWidth()+2).Padding(1, 1).Render(body)
}

// RenderWithBg renders the overlay body with the given box style and wraps it
// with the provided background color.
func (o OverlayShell) RenderWithBg(box lipgloss.Style, body string, bg string) string {
	return theme.WithBg(o.Render(box, body), bg)
}

// PlaceBottomAnchored composites the overlay string over the base view string
// using the file picker's bottom-anchored placement strategy: the overlay sits
// just above the input area, left-aligned.  inputHeight is the number of rows
// occupied by the bottom chrome (hDivider + approval tray + activity row +
// input + status bar) at the bottom of the base view.
func (o OverlayShell) PlaceBottomAnchored(base, overlay string, inputHeight int) string {
	return o.PlaceBottomAnchoredAt(base, overlay, inputHeight, 0)
}

// PlaceBottomAnchoredAt is like PlaceBottomAnchored but shifts the overlay
// right by xOffset columns, preserving the base content in the left margin.
// Use this when the content area is offset from the terminal edge (e.g. sidebar on the left).
func (o OverlayShell) PlaceBottomAnchoredAt(base, overlay string, inputHeight, xOffset int) string {
	baseLines := strings.Split(base, "\n")
	olLines := strings.Split(overlay, "\n")

	height := o.height
	if height < 1 {
		height = len(baseLines)
	}
	startY := height - len(olLines) - inputHeight - 1
	if startY < 0 {
		startY = 0
	}

	for i := 0; i < len(olLines) && startY+i < len(baseLines); i++ {
		idx := startY + i
		olWidth := lipgloss.Width(olLines[i])
		if xOffset <= 0 {
			baseRight := ansi.TruncateLeft(baseLines[idx], olWidth, "")
			baseLines[idx] = olLines[i] + baseRight
		} else {
			left := padOverlayLine(ansi.Cut(baseLines[idx], 0, xOffset), xOffset)
			baseRight := ansi.TruncateLeft(baseLines[idx], xOffset+olWidth, "")
			baseLines[idx] = left + olLines[i] + baseRight
		}
	}
	return strings.Join(baseLines, "\n")
}

// composeCenteredOverlay composites overlay over base at the center of the
// given terminal bounds without clearing content outside the overlay bounds.
func composeCenteredOverlay(base, overlay string, width, height int) string {
	if width < 1 || height < 1 {
		return base
	}

	baseLines := normalizeOverlayLines(base, width, height)
	overlayLines := strings.Split(overlay, "\n")
	overlayHeight := len(overlayLines)
	if overlayHeight == 0 {
		return strings.Join(baseLines, "\n")
	}

	maxOverlayWidth := 0
	for _, line := range overlayLines {
		maxOverlayWidth = max(maxOverlayWidth, lipgloss.Width(line))
	}
	if maxOverlayWidth == 0 {
		return strings.Join(baseLines, "\n")
	}

	startX := (width - maxOverlayWidth) / 2
	startY := (height - overlayHeight) / 2
	endY := min(height, startY+overlayHeight)
	for y := max(0, startY); y < endY; y++ {
		overlayLine := overlayLines[y-startY]
		baseLines[y] = composeOverlayLine(baseLines[y], overlayLine, width, startX, maxOverlayWidth)
	}

	return strings.Join(baseLines, "\n")
}

func normalizeOverlayLines(rendered string, width, height int) []string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	normalized := make([]string, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			normalized[i] = padOverlayLine(ansi.Cut(lines[i], 0, width), width)
			continue
		}
		normalized[i] = strings.Repeat(" ", width)
	}
	return normalized
}

func composeOverlayLine(base, overlay string, totalWidth, startX, overlayWidth int) string {
	visibleStart := max(0, startX)
	overlayOffset := max(0, -startX)
	visibleWidth := min(overlayWidth-overlayOffset, totalWidth-visibleStart)
	if visibleWidth <= 0 {
		return base
	}

	left := padOverlayLine(ansi.Cut(base, 0, visibleStart), visibleStart)
	mid := padOverlayLine(ansi.Cut(overlay, overlayOffset, overlayOffset+visibleWidth), visibleWidth)
	rightWidth := totalWidth - visibleStart - visibleWidth
	right := padOverlayLine(ansi.Cut(base, visibleStart+visibleWidth, totalWidth), rightWidth)

	return left + mid + right
}

func padOverlayLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	lineWidth := lipgloss.Width(line)
	if lineWidth >= width {
		return line
	}
	return line + strings.Repeat(" ", width-lineWidth)
}
