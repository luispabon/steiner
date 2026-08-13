package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// scrollModel is a minimal vertical scroll-position calculator. It replaces
// charm.land/bubbles/v2/viewport.Model for the conversation pane, where no
// soft wrapping, frame, styling, or horizontal scrolling is used, so every
// expensive capability of the library collapses to the arithmetic below.
//
// scrollModel owns the single line slice of the viewport: visibleViewportContent
// slices this slice and the scrollbar line count derives from it, so the
// rendered content and the scroll position cannot disagree by construction.
// The line splitting rule: SetContent normalises "\r\n" to "\n" and splits on
// "\n", matching exactly what the renderer emits (a lone '\r' not followed by
// '\n' is preserved and does not split a line). SetLines takes pre-split lines
// and must not contain embedded '\n'. Like the bubbles viewport, a single
// zero-width line is collapsed to no lines.
type scrollModel struct {
	lines           []string
	width           int
	height          int
	yOffset         int
	mouseWheelDelta int
}

// newScrollModel returns a scroll model with the given width and height and
// the same default mouse wheel delta (3) as viewport.New.
func newScrollModel(width, height int) scrollModel {
	return scrollModel{width: width, height: height, mouseWheelDelta: 3}
}

// Width returns the viewport width in columns.
func (m scrollModel) Width() int { return m.width }

// SetWidth sets the viewport width. Width does not affect scroll arithmetic;
// the render pipeline sizes content to it.
func (m *scrollModel) SetWidth(w int) { m.width = w }

// Height returns the viewport height in rows.
func (m scrollModel) Height() int { return m.height }

// SetHeight sets the viewport height. Matching the bubbles viewport, it does
// NOT re-clamp yOffset: increasing the height shrinks maxYOffset and can
// leave the model past the bottom (YOffset() > maxYOffset()), the library's
// PastBottom state. ScrollDown preserves that state and ScrollUp clamps back
// into range.
func (m *scrollModel) SetHeight(h int) { m.height = h }

// Lines returns the viewport content lines. It is the single owned slice the
// renderer slices; only SetLines and SetContent write it.
func (m scrollModel) Lines() []string { return m.lines }

// TotalLineCount returns the number of content lines.
func (m scrollModel) TotalLineCount() int { return len(m.lines) }

// maxYOffset returns the maximum valid yOffset: max(0, len(lines) - height).
func (m scrollModel) maxYOffset() int { return max(0, len(m.lines)-m.height) }

// YOffset returns the vertical scroll position.
func (m scrollModel) YOffset() int { return m.yOffset }

// SetYOffset clamps n to [0, maxYOffset()] and sets the scroll position.
func (m *scrollModel) SetYOffset(n int) {
	m.yOffset = min(max(n, 0), m.maxYOffset())
}

// AtTop reports whether the viewport is at the top.
func (m scrollModel) AtTop() bool { return m.yOffset <= 0 }

// AtBottom reports whether the viewport is at or past the bottom.
func (m scrollModel) AtBottom() bool { return m.yOffset >= m.maxYOffset() }

// ScrollDown moves the view down by n lines. It early-returns when at the
// bottom (including the PastBottom state, which it must preserve), when n is
// zero, or when there is no content.
func (m *scrollModel) ScrollDown(n int) {
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return
	}
	m.SetYOffset(m.yOffset + n)
}

// ScrollUp moves the view up by n lines. It early-returns when at the top,
// when n is zero, or when there is no content.
func (m *scrollModel) ScrollUp(n int) {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return
	}
	m.SetYOffset(m.yOffset - n)
}

// GotoBottom sets the scroll position to the bottom.
func (m *scrollModel) GotoBottom() { m.SetYOffset(m.maxYOffset()) }

// SetContent sets the content from a single string, normalising "\r\n" to
// "\n" before splitting on "\n". If the resulting offset is past the new
// maximum, the view is pulled to the bottom, matching the bubbles viewport.
func (m *scrollModel) SetContent(s string) {
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	m.SetLines(strings.Split(s, "\n"))
}

// SetLines replaces the content lines, taking ownership of the slice. A
// single zero-width line is collapsed to no lines, matching the bubbles
// viewport, and an offset past the new maximum is clamped by moving to the
// bottom.
func (m *scrollModel) SetLines(lines []string) {
	m.lines = lines
	if len(m.lines) == 1 && ansi.StringWidth(m.lines[0]) == 0 {
		m.lines = nil
	}
	if m.yOffset > m.maxYOffset() {
		m.GotoBottom()
	}
}
