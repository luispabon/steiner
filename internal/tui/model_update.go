package tui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type syncDebounceFiredMsg struct{ seq int }

const dragAutoScrollInterval = 60 * time.Millisecond

// dragAutoScrollTickMsg carries the drag epoch so a stale tick from a
// previous press cannot scroll after a new click, release, or Esc clear.
type dragAutoScrollTickMsg struct{ epoch int }

type modelReasoningResolvedMsg struct {
	capabilities map[string]provider.ReasoningCapabilities
	efforts      map[string]string
}

func syncDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(_ time.Time) tea.Msg {
		return syncDebounceFiredMsg{seq: seq}
	})
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case toggleThinkingMsg:
		return m.handleToggleThinkingMsg(msg)
	case setAccentMsg:
		return m.handleSetAccentMsg(msg)
	case tickMsg:
		return m.handleTickMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case runtimeEventMsg:
		return m.handleRuntimeEventMsg(msg)
	case bridgeClosedMsg:
		return m.handleBridgeClosedMsg(msg)
	case gitRefreshDoneMsg:
		m.syncSidebar()
		return m, nil
	case modelReasoningResolvedMsg:
		return m.handleModelReasoningResolvedMsg(msg)
	case syncDebounceFiredMsg:
		return m.handleSyncDebounceFiredMsg(msg)
	case clipboardImageMsg:
		return m.handleClipboardImageMsg(msg)
	case phaseTransitionFailedMsg:
		m.content.AppendLine(fmt.Sprintf("status: phase transition failed: %v", msg.err))
		return m, nil
	case mouseClickMsg, mouseMotionMsg, mouseReleaseMsg, mouseWheelMsg, dragAutoScrollTickMsg:
		return m.handleMouseEventMsg(msg)
	case tea.KeyPressMsg:
		return m.handleKeyMsg(msg)
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.relayoutInput()
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

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

func (m *Model) handleSyncDebounceFiredMsg(msg syncDebounceFiredMsg) (tea.Model, tea.Cmd) {
	if msg.seq == m.syncDebounceSeq && m.contentDirty {
		m.syncViewport()
		m.contentDirty = false
	}
	return m, nil
}

func (m *Model) handleModelReasoningResolvedMsg(msg modelReasoningResolvedMsg) (tea.Model, tea.Cmd) {
	m.modelReasoningCapabilities = msg.capabilities
	m.modelReasoningEfforts = msg.efforts
	m.reasoningBatchResolved = true
	m.reasoningLabels = newReasoningLabels(m.modelReasoningEfforts, m.modelReasoningCapabilities)
	m.status.reasoning = m.reasoningLabels[m.currentModelAlias]
	m.sidebar.reasoning = m.reasoningLabels[m.currentModelAlias]
	m.syncSidebar()
	return m, nil
}

func (m *Model) clearConversationState() (tea.Model, tea.Cmd) {
	if m.sessionResetCleanup != nil {
		m.sessionResetCleanup()
	}
	m.content.Clear()
	m.selection = m.selection.clear()
	m.clearDragState()
	m.imageMarkers = nil
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	for name := range m.enabledSkills {
		m.enabledSkills[name] = false
	}
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.syncSidebar()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.ClearConversation{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m *Model) handleToggleThinkingMsg(_ toggleThinkingMsg) (tea.Model, tea.Cmd) {
	m.showThinking = !m.showThinking
	m.content.showThinking = m.showThinking
	if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
		fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
	}
	for i := range m.content.segments {
		if m.content.segments[i].kind == segmentThinkingBlock {
			m.content.segments[i].renderDirty = true
		}
	}
	m.content.gen++
	m.syncViewport()
	return m, nil
}

func (m *Model) handleSetAccentMsg(msg setAccentMsg) (tea.Model, tea.Cmd) {
	accentHex := resolveAccentPreset(msg.preset, rand.Intn)
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"]
	}
	m.accentPreset = msg.preset
	s := theme.BuildStyles(accentHex)
	m.styles = &s
	m.content.styles = m.styles
	m.content.setGlamourStyleSheet(accentHex)
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = m.activity.withStyles(m.styles)
	m.applyInputStyles()
	m.slashOverlay.styles = m.styles
	m.fileList.styles = m.styles
	m.mcpOverlay.styles = m.styles
	m.filePicker.styles = m.styles
	m.sessionPicker.styles = m.styles
	m.modelPicker.styles = m.styles
	m.reasoningPicker.styles = m.styles
	m.planPicker.styles = m.styles
	m.accentPicker.styles = m.styles
	m.oneshotResumePicker.styles = m.styles
	if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
		fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
	}
	for i := range m.content.segments {
		m.content.segments[i].renderDirty = true
	}
	m.content.gen++
	m.syncViewport()
	return m, nil
}

func (m *Model) handleTickMsg(_ tickMsg) (tea.Model, tea.Cmd) {
	m.content.tickCount++
	m.sidebar.tickCount = m.content.tickCount
	m.activity = m.activity.advance()
	m.status.promptUsed = m.sidebar.promptUsed
	m.status.contextBudget = m.sidebar.contextBudget
	m.syncInputChrome()
	if m.git != nil {
		_ = m.git.takeError()
	}
	// Clear any render error captured during the last cycle.
	if m.content.lastRenderErr != nil {
		m.content.lastRenderErr = nil
	}
	// Advance delegation spinners when delegations are in flight.
	if m.content.HasActiveDelegations() {
		m.content.AdvanceDelegationSpinners()
	}
	// Advance compaction spinners when a compaction is in progress.
	if m.content.HasActiveCompactions() {
		m.content.AdvanceCompactionSpinners()
	}
	if m.contentDirty || m.content.streaming || m.compaction.Active() || m.content.HasActiveDelegations() || m.content.HasActiveCompactions() || m.sidebar.mcpConnecting {
		m.syncViewport()
		m.contentDirty = false
	}
	if m.needsTicking() {
		m.ticking = true
		return m, tickCmd()
	}
	m.ticking = false
	return m, nil
}

func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.fileList.OverlayShell = m.fileList.WithDimensions(msg.Width, msg.Height)
	m.mcpOverlay.OverlayShell = m.mcpOverlay.WithDimensions(msg.Width, msg.Height)

	m.sessionPicker = m.sessionPicker.withDimensions(msg.Width, msg.Height)
	m.oneshotResumePicker = m.oneshotResumePicker.withDimensions(msg.Width, msg.Height)
	if m.contextOverlay.IsOpen() {
		m.contextOverlay.OverlayShell = m.contextOverlay.WithDimensions(msg.Width, msg.Height)
		m.contextOverlay = m.contextOverlay.reflow()
	}
	m.exitModal.OverlayShell = m.exitModal.WithDimensions(msg.Width, msg.Height)
	m.workflowHandoff.OverlayShell = m.workflowHandoff.WithDimensions(msg.Width, msg.Height)

	// Use content area width for bottom-anchored overlays so they don't
	// overflow into the sidebar area.
	contentW := msg.Width
	if m.sidebar.Visible(msg.Width) {
		contentW = msg.Width - sidebarWidth - 1
	}
	m.filePicker.OverlayShell = m.filePicker.WithDimensions(contentW, msg.Height)
	m.slashOverlay.OverlayShell = m.slashOverlay.WithDimensions(contentW, msg.Height)
	m.layout()
	return m, nil
}

func (m *Model) handleRuntimeEventMsg(msg runtimeEventMsg) (tea.Model, tea.Cmd) {
	eventCmd := m.applyEvent(msg.Event)
	var cmds []tea.Cmd
	if eventCmd != nil {
		cmds = append(cmds, eventCmd)
	}
	if tickCmd := m.ensureTicking(); tickCmd != nil {
		cmds = append(cmds, tickCmd)
	}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) handleBridgeClosedMsg(_ bridgeClosedMsg) (tea.Model, tea.Cmd) {
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
			text = extractText(m.screenLines, m.selection, left, right)
		}
		if text != "" {
			m.mousePressX = -1
			m.mousePressY = -1
			cmd = copyToClipboard(text)
		}
	} else {
		m.selection = m.selection.clear()
		m.handleLeftClick(msg.y)
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
		m.viewport.ScrollUp(1)
	} else {
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

func (m *Model) handleMouseWheelMsg(msg mouseWheelMsg) (tea.Model, tea.Cmd) {
	m.lastWheelMouseAt = time.Now()
	switch msg.direction {
	case "up":
		m.scrollUp(m.viewport.mouseWheelDelta)
	case "down":
		m.scrollDown(m.viewport.mouseWheelDelta)
	}
	return m, nil
}

func (m *Model) handleClipboardImageMsg(msg clipboardImageMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	label := nextMarkerLabel(m.imageMarkers)
	m.imageMarkers = append(m.imageMarkers, imageMarker{label: label, image: msg.block})
	m.input.InsertString(label)
	m.syncInputChrome()
	return m, nil
}
