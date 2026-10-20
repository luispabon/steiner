package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const delegationBodyOverhead = 9

func (m *Model) contentWidth() int {
	contentWidth := m.width
	if m.sidebar.Visible(m.width) {
		contentWidth = m.width - sidebarWidth - 1
	}
	return max(1, contentWidth)
}

func (m *Model) layout() {
	m.layoutContent(false)
}

// layoutContent sizes the viewport and syncs it. A window resize
// (resize=true) may defer the content reflow; the returned command arms it.
func (m *Model) layoutContent(resize bool) tea.Cmd {
	contentWidth := m.contentWidth()
	// ContentPane has PaddingTop(1)+PaddingLeft(3)+PaddingRight(3), so inner = contentWidth-6.
	// Total rows: top_pad(1) + viewport + hDivider(1) + input + activity + status(1).
	// The composer renders as a padded message-style card, so derive its height.
	inputRows, activityRows := m.computeInputRows(contentWidth)
	queuedRows := m.queuedSteerHeight(contentWidth)
	maxInputRows := max(1, m.height-4-activityRows-queuedRows)
	inputRows = min(inputRows, maxInputRows)
	m.viewport.SetWidth(max(1, contentWidth-6))
	m.viewport.SetHeight(max(1, m.height-3-inputRows-activityRows-queuedRows))
	// Set max delegation body lines: viewport height minus overhead for border/header/stats/hint.
	// Overhead: lipgloss border (2) + blank after box (1) + hint+newline (2) + header (1) + separator (1) + stats (1) = 8.
	// Using delegationBodyOverhead leaves one spare row so the box never grazes the viewport edge.
	m.content.maxDelegationBodyLines = max(0, m.viewport.Height()-delegationBodyOverhead)
	cmd := m.planReflow(resize)
	m.syncViewport()
	return cmd
}

// relayoutInput recalculates viewport height after the input content changes
// (e.g. on each keystroke). Cheaper than layout(): skips syncViewport when
// height is unchanged, avoiding a full content re-render on every key press.
func (m *Model) relayoutInput() {
	contentWidth := m.contentWidth()
	inputRows, activityRows := m.computeInputRows(contentWidth)
	queuedRows := m.queuedSteerHeight(contentWidth)
	maxInputRows := max(1, m.height-4-activityRows-queuedRows)
	inputRows = min(inputRows, maxInputRows)
	newHeight := max(1, m.height-3-inputRows-activityRows-queuedRows)
	if newHeight == m.viewport.Height() {
		return
	}
	m.viewport.SetHeight(newHeight)
	m.content.maxDelegationBodyLines = max(0, m.viewport.Height()-delegationBodyOverhead)
	m.syncViewport()
}

func (m *Model) computeInputRows(contentWidth int) (inputRows, activityRows int) {
	// Match the textarea's internal wrap width to the composer's render width.
	// The old SetWidth(99999) disabled the textarea's internal soft wrap so that
	// LineInfo().ColumnOffset reported an absolute cursor offset; cursor
	// placement now uses m.input.Column(), which is width-independent, so the
	// textarea can run at its natural width. Bubbles' word-wrap is quadratic in
	// row width, so the old width made every keystroke re-wrap the entire cursor
	// line with full grapheme segmentation instead of rows capped at the visible
	// width.
	m.input.SetWidth(m.inputInnerWidth(contentWidth))
	inputRows = m.inputChromeHeight(contentWidth)
	activityRows = m.activityRowHeight(contentWidth)
	return inputRows, activityRows
}

// setViewportLines is the single choke point for viewport content updates.
// The scroll model owns the one line slice visibleViewportContent slices, and
// vpViewCache invalidation lives here; calling m.viewport.SetLines directly
// would leave vpViewCache stale and serve frames that disagree with the new
// content. Do not bypass this choke point. lines must not contain '\n'.
func (m *Model) setViewportLines(lines []string) {
	m.viewport.SetLines(lines)
	// vpViewCache is keyed on scroll position, width and scrollbar presence,
	// none of which change when only the content does. Invalidating here
	// rather than in syncViewport keeps the cache tied to the choke point
	// this comment documents.
	m.vpViewCache = ""
}

func (m *Model) syncViewport() {
	width := m.contentRenderWidth()

	// The background escape is re-inserted after every ANSI reset in rendered
	// content (theme.WithBg semantics). This is necessary because terminals with
	// transparency enabled composite ANSI resets (\x1b[0m) with their
	// transparency setting, producing transparent gaps whenever nested
	// lipgloss/glamour renders emit a reset. lipgloss Background() on a
	// container does NOT fix this — it only fills padding/border cells.
	// Restoring the background and padding every line (WithBg + PadLines)
	// ensures every cell in the viewport has an explicit SGR 48 background,
	// making content fully opaque. bgFormat skips the work on scroll-only
	// updates and otherwise reformats only the lines from the first changed one.
	changed := m.bgFormat.update(m.content.blocks(width), width, m.resolvedPalette().ContentBG)

	widthChanged := width != m.fmtBgCacheWidth
	if changed || widthChanged {
		m.fmtBgCacheWidth = width

		// A width reflow invalidates row/col anchors (wrapping changes the
		// rendered rows), so the selection clears; same-width content changes
		// remap the selection through segment anchors and no longer clear it.
		// Scroll-only and pure viewport-height changes do not enter this block,
		// so their selections survive, and input-region drags are screen-anchored
		// and must survive reflow untouched.
		if m.activeRegion == regionViewport {
			if widthChanged {
				m.clearSelectionAndDrag()
			} else {
				m.remapViewportSelection()
			}
		}
	}

	lines := m.bgFormat.lines
	vw := m.viewport.Width()
	pad := max(m.viewport.Height()-len(lines), 0)
	m.contentTopPad = pad
	if pad > 0 {
		if m.padLineCacheWidth != vw || m.padLineCacheRendered == "" {
			m.padLineCacheWidth = vw
			m.padLineCacheRendered = lipgloss.NewStyle().
				Background(lipgloss.Color(m.resolvedPalette().ContentBG)).
				Render(strings.Repeat(" ", vw))
		}
		padded := make([]string, pad, pad+len(lines))
		for i := range padded {
			padded[i] = m.padLineCacheRendered
		}
		lines = append(padded, lines...)
	}
	m.setViewportLines(lines)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) scrollUp(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.autoScroll = false
	m.viewport.ScrollUp(lines)
}

func (m *Model) scrollDown(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.viewport.ScrollDown(lines)
	if m.viewport.AtBottom() {
		m.autoScroll = true
	}
}
