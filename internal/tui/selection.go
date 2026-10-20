package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type selectionPoint struct {
	line, col int
}

type selectionRegion int

const (
	regionNone selectionRegion = iota
	regionViewport
	regionInput
	regionSidebar
)

type selectionState struct {
	start, end             selectionPoint
	active                 bool
	startAnchor, endAnchor selectionAnchor
}

// hasSelection reports whether a non-trivial selection exists.
func (s selectionState) hasSelection() bool {
	return s.start != s.end || s.active
}

// canonical returns (start, end) normalised so start <= end by line, then col.
func (s selectionState) canonical() (selectionPoint, selectionPoint) {
	if s.start.line < s.end.line || (s.start.line == s.end.line && s.start.col <= s.end.col) {
		return s.start, s.end
	}
	return s.end, s.start
}

// clear returns the zero selectionState.
func (s selectionState) clear() selectionState {
	return selectionState{}
}

// anchorPoint converts screen coordinates to selection coordinates for the
// active region. The viewport region anchors to unpadded content lines so the
// selection follows text on scroll; the input region stays screen-anchored.
func (m *Model) anchorPoint(x, y int) selectionPoint {
	if m.activeRegion == regionViewport {
		left, _ := m.regionXBounds(regionViewport)
		line := max(0, m.contentLineAtScreenY(y))
		if maxContent := len(m.viewport.Lines()) - m.contentTopPad - 1; maxContent >= 0 {
			line = min(line, maxContent)
		}
		return selectionPoint{line: line, col: x - left}
	}
	return selectionPoint{line: y, col: x}
}

// screenSelection projects the stored selection to screen coordinates for
// highlight rendering. Viewport selections are content-anchored and mapped
// back through the current scroll offset and region left bound; other regions
// are already screen-anchored and pass through unchanged. The projected span
// is canonicalized and clamped to the viewport content rows so an off-screen
// selection cannot highlight chrome; a partially visible span clamps its
// endpoint to the visible row and the region edge column.
func (m *Model) screenSelection() selectionState {
	s := m.selection
	if m.activeRegion == regionViewport {
		left, right := m.regionXBounds(regionViewport)
		s.start.line = m.screenYAtContentLine(s.start.line)
		s.end.line = m.screenYAtContentLine(s.end.line)
		s.start.col += left
		s.end.col += left
		a, b := s.canonical()
		top := m.viewportContentTopRow()
		bottom := m.viewportContentBottomRow()
		if b.line < top || a.line > bottom {
			return s.clear()
		}
		if a.line < top {
			a.line = top
			a.col = left
		}
		if b.line > bottom {
			b.line = bottom
			b.col = right
		}
		s.start, s.end = a, b
	}
	return s
}

// applyScreenHighlight applies selStyle highlight to screen-space coordinates.
// regionLeft and regionRight constrain intermediate (fully-selected) lines to
// the active region's content bounds, preventing the highlight from bleeding
// into the sidebar, divider, or padding columns. Pass 0, 0 to disable
// constraining (matches the un-clamped legacy behaviour).
func applyScreenHighlight(frame string, state selectionState, selStyle lipgloss.Style, regionLeft, regionRight int) string {
	var c highlightCache
	return c.apply(frame, state, selStyle, regionLeft, regionRight)
}

// detectRegion classifies a screen coordinate (x, y) into a UI region.
// Returns regionNone for dividers and status bar, regionSidebar for sidebar,
// regionInput for the input area, and regionViewport for viewport content.
func (m *Model) detectRegion(x, y int) selectionRegion {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return regionNone
	}

	// The sidebar and its divider column span the full screen height
	// (including the input and status rows), so an x-based sidebar/divider
	// hit must be classified before any row-based (status/input) check.
	if sidebarVisible {
		if m.sidebarPosition == "right" {
			dividerStartX := m.width - sidebarWidth - 1
			if x >= m.width-sidebarWidth {
				return regionSidebar
			}
			if x == dividerStartX {
				return regionNone
			}
		} else {
			if x < sidebarWidth {
				return regionSidebar
			}
			if x == sidebarWidth {
				return regionNone
			}
		}
	}

	statusRow := m.height - 1
	if y == statusRow {
		return regionNone
	}

	inputChromeH := m.inputChromeHeight(contentWidth)
	inputStartRow := statusRow - inputChromeH
	if y >= inputStartRow {
		return regionInput
	}

	if y >= inputStartRow-2 {
		return regionNone
	}

	return regionViewport
}

// regionXBounds returns the [left, right) screen-column bounds of the content
// area for a given selection region (viewport or input), accounting for
// sidebar placement and the scrollbar-aware right padding. Shared by
// clampToRegion (which additionally clamps y and nudges x for click
// placement) and selectionHighlightBounds (which uses the bounds verbatim to
// constrain multi-line highlight). Returns (0, 0) for regionNone and
// regionSidebar.
func (m *Model) regionXBounds(region selectionRegion) (left, right int) {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	switch region {
	case regionViewport:
		left = 0
		right = m.width

		if sidebarVisible {
			if m.sidebarPosition == "right" {
				right = m.width - sidebarWidth - 1
			} else {
				left = sidebarWidth + 1
			}
		}

		left += 3
		right -= 3
		if left > right {
			left = right
		}
		return left, right

	case regionInput:
		sidebarOffset := 0

		if sidebarVisible {
			if m.sidebarPosition == "right" {
				sidebarOffset = 0
			} else {
				sidebarOffset = sidebarWidth + 1
			}
		}

		left = sidebarOffset + inputRailWidth + inputPadX
		right = sidebarOffset + contentWidth - 1
		if left > right {
			right = left
		}
		return left, right

	default:
		return 0, 0
	}
}

// clampToRegion clamps screen coordinates (x, y) to the content bounds of a
// given selection region (viewport or input), preventing multi-line selection
// from bleeding into padding, dividers, sidebar, or the other container.
// For regionNone and regionSidebar, returns (x, y) unchanged.
func (m *Model) clampToRegion(x, y int, region selectionRegion) (int, int) {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)
	statusRow := m.height - 1
	inputChromeH := m.inputChromeHeight(contentWidth)
	inputStartRow := statusRow - inputChromeH

	switch region {
	case regionViewport:
		left, right := m.regionXBounds(region)
		x = max(left, min(right-1, x))
		y = max(0, min(inputStartRow-3, y))
		return x, y

	case regionInput:
		left, right := m.regionXBounds(region)
		if sidebarVisible {
			x = max(left, min(right, x))
		} else {
			x = max(left, min(right-1, x))
		}
		y = max(inputStartRow, min(statusRow-1, y))
		return x, y

	default:
		return x, y
	}
}

// selectionHighlightBounds returns the [left, right) screen-column bounds of
// the currently active selection region's content area, for use by
// applyScreenHighlight to keep intermediate multi-line selection highlight
// out of the sidebar, dividers, and padding. Returns (0, 0) for regionNone,
// regionSidebar, or when no region is active, which preserves the legacy
// unconstrained behaviour.
func (m *Model) selectionHighlightBounds() (left, right int) {
	return m.regionXBounds(m.activeRegion)
}

// flushScreenFrame strips the frame View kept during a drag into
// m.screenLines. It is a no-op when no drag frame is pending.
func (m *Model) flushScreenFrame() {
	if !m.screenFramePending {
		return
	}
	m.screenLines = strings.Split(ansi.Strip(m.screenFrame), "\n")
	m.screenFrame, m.screenFramePending = "", false
}

// populateScreenLines renders the current frame exactly as View() does and
// stores its ANSI-stripped lines in m.screenLines. This duplicates the
// render+strip steps View() performs during an active selection drag,
// because click handling runs outside the render cycle and needs a freshly
// rendered frame on demand; having View() call this instead would double the
// render cost on every frame with an active selection.
func (m *Model) populateScreenLines() {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)
	base := m.renderBaseView(contentWidth, sidebarVisible)
	result := m.renderOverlayView(base, contentWidth)
	m.screenLines = strings.Split(ansi.Strip(result), "\n")
	m.screenFrame, m.screenFramePending = "", false
}
