package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const dragAutoScrollInterval = 60 * time.Millisecond

// dragAutoScrollTickMsg carries the drag epoch so a stale tick from a
// previous press cannot scroll after a new click, release, or Esc clear.
type dragAutoScrollTickMsg struct{ epoch int }

// handleMouseEventMsg dispatches the mouse event message subtypes and the drag
// auto-scroll tick, keeping Update's cyclomatic complexity under the gocyclo
// threshold.
func (m *Model) handleMouseEventMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case mouseClickMsg:
		return m.handleMouseClickMsg(msg)
	case mouseMotionMsg:
		return m.handleMouseMotionMsg(msg)
	case mouseReleaseMsg:
		return m.handleMouseReleaseMsg(msg)
	case mouseWheelMsg:
		return m.handleMouseWheelMsg(msg)
	case dragAutoScrollTickMsg:
		return m.handleDragAutoScrollTick(msg)
	}
	return m, nil
}

// nextClickCount returns the click count for a click at clickPos/clickTime,
// cycling 1->2->3->1 when it falls within 500ms and ±1 cell of the model's
// last recorded click, or resetting to 1 otherwise.
func (m *Model) nextClickCount(clickPos selectionPoint, clickTime time.Time) int {
	timeDiff := clickTime.Sub(m.lastClickTime)
	const clickTimeout = 500 * time.Millisecond
	isWithinTime := timeDiff <= clickTimeout && timeDiff >= 0
	if !isWithinTime {
		return 1
	}

	colDiff := m.lastClickPos.col - clickPos.col
	lineDiff := m.lastClickPos.line - clickPos.line
	if colDiff < 0 {
		colDiff = -colDiff
	}
	if lineDiff < 0 {
		lineDiff = -lineDiff
	}
	if colDiff > 1 || lineDiff > 1 {
		return 1
	}

	count := m.clickCount + 1
	if count > 3 {
		count = 1
	}
	return count
}

// handleMultiClickSelection completes a double- or triple-click word/line
// selection at the clamped viewport coordinates and queues a clipboard copy.
func (m *Model) handleMultiClickSelection(clampedX, clampedY int) (tea.Model, tea.Cmd) {
	m.populateScreenLines()
	lines := m.screenLines
	left, right := m.selectionHighlightBounds()
	var startLine, endLine, startCol, endCol int
	if m.clickCount == 2 {
		startLine, endLine = clampedY, clampedY
		if clampedY >= 0 && clampedY < len(lines) {
			startCol, endCol = wordBoundsAt(lines[clampedY], clampedX)
		}
	} else {
		startLine, endLine, startCol, endCol = logicalLineBounds(lines, clampedY, left, right)
	}
	m.selection = selectionState{
		start: selectionPoint{line: startLine, col: startCol},
		end:   selectionPoint{line: endLine, col: endCol},
	}
	m.mousePressX = -1
	m.mousePressY = -1
	var text string
	if m.activeRegion == regionViewport {
		left, _ := m.regionXBounds(regionViewport)
		// logicalLineBounds can extend endLine into the chrome rows below the
		// viewport (divider/input); clamp the frame rows to the viewport content
		// range before converting to content lines so a triple-click at the
		// bottom cannot copy an off-screen line.
		startLine = max(startLine, m.viewportContentTopRow())
		endLine = min(endLine, m.viewportContentBottomRow())
		if startLine > endLine {
			m.selection = m.selection.clear()
			return m, nil
		}
		startLine = m.contentLineAtScreenY(startLine)
		endLine = m.contentLineAtScreenY(endLine)
		startCol -= left
		endCol -= left
		startLine, startAnchor := m.viewportSelectionEndpoint(startLine)
		endLine, endAnchor := m.viewportSelectionEndpoint(endLine)
		m.selection = selectionState{
			start:       selectionPoint{line: startLine, col: startCol},
			end:         selectionPoint{line: endLine, col: endCol},
			startAnchor: startAnchor,
			endAnchor:   endAnchor,
		}
		text = m.extractViewportText()
	} else {
		text = extractText(lines, m.selection, left, right)
	}
	if text != "" {
		return m, copyToClipboard(text)
	}
	return m, nil
}

func (m *Model) handleMouseClickMsg(msg mouseClickMsg) (tea.Model, tea.Cmd) {
	m.dragScrollDir = 0
	m.dragScrollTicking = false
	m.dragScrollEpoch++

	clickPos := selectionPoint{line: msg.y, col: msg.x}
	clickTime := time.Now()

	m.clickCount = m.nextClickCount(clickPos, clickTime)
	m.lastClickTime = clickTime
	m.lastClickPos = clickPos
	m.activeRegion = m.detectRegion(msg.x, msg.y)

	m.sidebarPressX, m.sidebarPressY = -1, -1
	if m.activeRegion == regionSidebar {
		m.sidebarPressX, m.sidebarPressY = msg.x, msg.y
	}
	if m.activeRegion == regionNone || m.activeRegion == regionSidebar {
		m.selection = m.selection.clear()
		m.mousePressX = -1
		m.mousePressY = -1
		return m, nil
	}

	clampedX, clampedY := m.clampToRegion(msg.x, msg.y, m.activeRegion)

	if m.activeRegion == regionViewport && (m.clickCount == 2 || m.clickCount == 3) {
		return m.handleMultiClickSelection(clampedX, clampedY)
	}

	start := m.anchorPoint(clampedX, clampedY)
	m.selection = selectionState{start: start, end: start, active: true}
	if m.activeRegion == regionViewport {
		m.selection.start.line, m.selection.startAnchor = m.viewportSelectionEndpoint(start.line)
		m.selection.end.line = m.selection.start.line
		m.selection.endAnchor = m.selection.startAnchor
	}
	m.mousePressX = msg.x
	m.mousePressY = msg.y
	return m, nil
}

func (m *Model) handleMouseMotionMsg(msg mouseMotionMsg) (tea.Model, tea.Cmd) {
	if m.mousePressX >= 0 {
		var cmd tea.Cmd
		// A drag over new coordinates abandons any follow-to-bottom scrolling;
		// the pointer position and selection define the view now.
		if m.activeRegion == regionViewport && (msg.x != m.mousePressX || msg.y != m.mousePressY) {
			m.autoScroll = false
		}
		// Edge auto-scroll: holding the mouse past the viewport top or bottom
		// scrolls the view while extending the selection. A repeating tick keeps
		// scrolling until the pointer moves back inside or the button releases.
		if m.activeRegion == regionViewport {
			top := m.viewportContentTopRow()
			bottom := m.viewportContentBottomRow()
			dir := 0
			if msg.y <= top && !m.viewport.AtTop() {
				dir = -1
			} else if msg.y >= bottom && !m.viewport.AtBottom() {
				dir = 1
			}
			m.dragScrollDir = dir
			m.dragLastX, m.dragLastY = msg.x, msg.y
			if dir != 0 && !m.dragScrollTicking {
				m.dragScrollTicking = true
				epoch := m.dragScrollEpoch
				cmd = tea.Tick(dragAutoScrollInterval, func(time.Time) tea.Msg {
					return dragAutoScrollTickMsg{epoch: epoch}
				})
			}
		}
		clampedX, clampedY := m.clampToRegion(msg.x, msg.y, m.activeRegion)
		m.selection.end = m.anchorPoint(clampedX, clampedY)
		m.anchorViewportSelectionEnd()
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleMouseReleaseMsg(msg mouseReleaseMsg) (tea.Model, tea.Cmd) {
	m.dragScrollEpoch++
	if m.activeRegion == regionSidebar && m.sidebarPressX >= 0 && msg.x == m.sidebarPressX && msg.y == m.sidebarPressY {
		m.sidebarPressX, m.sidebarPressY = -1, -1
		return m, m.sidebarRosterClick(msg.x, msg.y)
	}
	m.sidebarPressX, m.sidebarPressY = -1, -1
	// mousePressX < 0 means the press was already fully handled at click time
	// (e.g. word/line selection from a double/triple-click); nothing left to do.
	if m.mousePressX < 0 {
		return m, nil
	}

	clampedX, clampedY := m.clampToRegion(msg.x, msg.y, m.activeRegion)
	m.selection.end = m.anchorPoint(clampedX, clampedY)
	m.anchorViewportSelectionEnd()
	m.dragScrollDir = 0
	m.dragScrollTicking = false
	var cmd tea.Cmd
	if m.mousePressX != msg.x || m.mousePressY != msg.y {
		m.selection.active = false
		var text string
		if m.activeRegion == regionViewport {
			text = m.extractViewportText()
		} else {
			left, right := m.selectionHighlightBounds()
			m.flushScreenFrame()
			text = extractText(m.screenLines, m.selection, left, right)
		}
		if text != "" {
			m.mousePressX = -1
			m.mousePressY = -1
			cmd = copyToClipboard(text)
		}
	} else {
		m.selection = m.selection.clear()
		if m.activeRegion == regionViewport && msg.y >= m.viewportContentTopRow() && msg.y <= m.viewportContentBottomRow() {
			m.handleLeftClick(msg.y)
		}
	}
	m.mousePressX = -1
	m.mousePressY = -1
	return m, cmd
}

// handleDragAutoScrollTick scrolls one line per tick while the drag hovers
// past a viewport edge and extends the selection to the tracked pointer
// position. Re-arms the tick so scrolling continues until the pointer moves
// back inside the viewport or the button releases. A tick whose epoch no
// longer matches the current drag (new click, release, or Esc) is stale and
// stops the tick without scrolling.
func (m *Model) handleDragAutoScrollTick(msg dragAutoScrollTickMsg) (tea.Model, tea.Cmd) {
	// A stale tick belongs to a previous press; ignore it without touching
	// the current drag state so dragScrollTicking stays accurate for any new
	// drag and no duplicate tick chain is armed.
	if msg.epoch != m.dragScrollEpoch {
		return m, nil
	}
	if m.mousePressX < 0 || m.dragScrollDir == 0 {
		m.dragScrollTicking = false
		return m, nil
	}
	if m.dragScrollDir < 0 {
		if m.viewport.AtTop() {
			m.dragScrollDir = 0
			m.dragScrollTicking = false
			return m, nil
		}
		m.viewport.ScrollUp(1)
	} else {
		if m.viewport.AtBottom() {
			m.dragScrollDir = 0
			m.dragScrollTicking = false
			return m, nil
		}
		m.viewport.ScrollDown(1)
	}
	m.autoScroll = false
	clampedX, clampedY := m.clampToRegion(m.dragLastX, m.dragLastY, m.activeRegion)
	m.selection.end = m.anchorPoint(clampedX, clampedY)
	m.anchorViewportSelectionEnd()
	epoch := m.dragScrollEpoch
	return m, tea.Tick(dragAutoScrollInterval, func(time.Time) tea.Msg {
		return dragAutoScrollTickMsg{epoch: epoch}
	})
}

// handleMouseWheelMsg scrolls the context overlay when the pointer is inside
// it and the transcript otherwise. It never reaches the composer: the composer
// window follows the cursor, so scrolling the textarea's own viewport has no
// visible effect.
func (m *Model) handleMouseWheelMsg(msg mouseWheelMsg) (tea.Model, tea.Cmd) {
	m.lastWheelMouseAt = time.Now()
	if m.contextOverlayCapturesMouse(msg.x, msg.y) {
		switch msg.direction {
		case "up":
			m.contextOverlay = m.contextOverlay.scrollUp(m.viewport.mouseWheelDelta)
		case "down":
			m.contextOverlay = m.contextOverlay.scrollDown(m.viewport.mouseWheelDelta)
		}
		return m, nil
	}

	switch msg.direction {
	case "up":
		m.scrollUp(m.viewport.mouseWheelDelta)
	case "down":
		m.scrollDown(m.viewport.mouseWheelDelta)
	}
	return m, nil
}
