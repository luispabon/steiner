package tui

import (
	"context"
	"fmt"

	"slices"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
)

func (m *Model) handleKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if shouldIgnoreLeakedMouseRunes(msg, m.recentWheelMouseInput()) {
		return m, nil
	}
	if handled, next, cmd := m.handleOverlayKeyMsg(msg); handled {
		return next, cmd
	}
	if m.approval.active {
		return m.handleApprovalKey(msg)
	}

	m = m.resetCompletionState(msg)
	activeConversation := m.hasActiveConversation()
	if handled, next := m.handleConversationKeyMsg(msg, activeConversation); handled {
		return next, nil
	}
	if handled, next, cmd := m.handleNavigationKeyMsg(msg); handled {
		return next, cmd
	}
	return m.handleComposerKeyMsg(msg)
}

func (m *Model) recentWheelMouseInput() bool {
	if m.lastWheelMouseAt.IsZero() {
		return false
	}
	return time.Since(m.lastWheelMouseAt) <= 200*time.Millisecond
}

func (m *Model) handleOverlayKeyMsg(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	for _, handler := range overlayKeyHandlers {
		if !handler.matches(m) {
			continue
		}
		cmd := handler.handle(m, msg)
		return true, m, cmd
	}
	return false, m, nil
}

func (m *Model) resetCompletionState(msg tea.KeyPressMsg) *Model {
	if msg.Code == tea.KeyTab {
		return m
	}
	m.completionCandidates = nil
	m.completionIdx = 0
	return m
}

func (m *Model) hasActiveConversation() bool {
	return m.content.streamingPhase != "" || m.status.mode == "running" || m.status.mode == "approval"
}

// isCtrl reports whether the key press is Ctrl+key for the given letter rune.
func isCtrl(msg tea.KeyPressMsg, letter rune) bool {
	return msg.Mod&tea.ModCtrl != 0 && msg.Code == letter
}

func (m *Model) handleConversationKeyMsg(msg tea.KeyPressMsg, activeConversation bool) (bool, tea.Model) {
	if msg.Code == tea.KeyEsc && m.helpVisible {
		m.helpVisible = false
		return true, m
	}
	if activeConversation && (msg.Code == tea.KeyEsc || isCtrl(msg, 'c') || isCtrl(msg, 'd')) {
		return true, m.executeInterruptAction()
	}
	// During an active run, Enter queues a steer message instead of submitting normally.
	if activeConversation && msg.Code == tea.KeyEnter && !m.approval.active && !key.Matches(msg, m.input.KeyMap.InsertNewline) {
		return true, m.executeSteerAction()
	}
	if msg.String() == "?" && strings.TrimSpace(m.input.Value()) == "" {
		m.helpVisible = !m.helpVisible
		return true, m
	}
	return false, m
}

//nolint:gocyclo // navigation shortcuts intentionally stay explicit
func (m *Model) handleNavigationKeyMsg(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	// Handle Ctrl-key shortcuts before the switch (msg.Code alone can't match ctrl keys).
	switch {
	case isCtrl(msg, 'c') || isCtrl(msg, 'd'):
		if m.controller == nil && m.worktreePlan == nil {
			return true, m, tea.Quit
		}
		if m.controller == nil {
			_, cmd := m.beginExitFlow()
			return true, m, cmd
		}
		return true, m.openExitModal(), nil
	case isCtrl(msg, 'b'):
		m.sidebar.Toggle()
		m.layout()
		return true, m, nil
	case isCtrl(msg, 't'):
		m.openContextOverlayImmediate()
		return true, m, nil
	case isCtrl(msg, 'x'):
		m.content.ToggleLastDelegationOutput()
		m.syncViewport()
		return true, m, nil
	case isCtrl(msg, 'v'):
		next, cmd := m.handlePasteKey()
		return true, next, cmd
	}
	switch msg.Code {
	case tea.KeyTab:
		if msg.Mod&tea.ModShift != 0 {
			next := m.executeToggleModeAction()
			return true, next, nil
		}
		next, cmd := m.handleTabKey(msg)
		return true, next, cmd
	case tea.KeyUp:
		next, cmd := m.handleKeyUp(msg)
		return true, next, cmd
	case tea.KeyDown:
		next, cmd := m.handleKeyDown(msg)
		return true, next, cmd
	case tea.KeyPgUp:
		m.scrollUp(max(1, m.viewport.Height()))
		return true, m, nil
	case tea.KeyPgDown:
		m.scrollDown(max(1, m.viewport.Height()))
		return true, m, nil
	case tea.KeyEsc:
		return m.handleSelectionEscKey()
	default:
		return false, m, nil
	}
}

// handlePasteKey handles Ctrl-V: blocks the paste with an inline notice when
// the current model can't view images and no vision sub-agent is configured
// to route them, otherwise proceeds with the normal clipboard paste.
func (m *Model) handlePasteKey() (tea.Model, tea.Cmd) {
	if m.visionCapabilities != nil &&
		m.visionCapabilities.Get(m.primaryModel) == agent.VisionIncapable &&
		!m.visionCapabilities.SubAgentConfigured() {
		m.content.AppendLine(fmt.Sprintf("  %s can't view images and no vision sub-agent is configured; paste ignored. Configure sub_agent model for the \"vision\" agent type to analyse images.", m.primaryModel))
		m.syncViewport()
		return m, nil
	}
	return m, pasteImageCmd(m.imageStore)
}

// handleSelectionEscKey clears an active selection when Esc is pressed.
// Returns false (not consumed) when no selection exists so Esc can fall through.
func (m *Model) handleSelectionEscKey() (bool, tea.Model, tea.Cmd) {
	if m.selection.hasSelection() {
		m.clearSelectionAndDrag()
		return true, m, nil
	}
	return false, m, nil
}

func (m *Model) openContextOverlayImmediate() {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.RequestContextReport{}); err != nil {
		m.content.AppendLine(fmt.Sprintf("status: %v", err))
	}
}

func (m *Model) handleComposerKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEnter && (!m.approval.active && !key.Matches(msg, m.input.KeyMap.InsertNewline)) {
		return m.handleEnter()
	}

	if len(m.imageMarkers) > 0 {
		if result, handled := m.tryDeleteMarker(msg); handled {
			return result, nil
		}
	}

	if msg.Text != "" {
		for _, r := range msg.Text {
			if r == '/' && strings.TrimSpace(m.input.Value()) == "" {
				items := m.buildSlashOverlayItems()
				m.slashOverlay = m.slashOverlay.Open(items)
				m.slashOverlay.width = m.width
				m.slashOverlay.height = m.height
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				m.syncSlashOverlayWithComposer()
				return m, cmd
			}
			if r != '@' {
				continue
			}
			root := m.sidebar.workingDir
			if root == "" {
				root = "."
			}
			m.filePicker = m.filePicker.Open(root)
			m.filePicker.width = m.width
			m.filePicker.height = m.height
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.syncFilePickerWithComposer()
			return m, cmd
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	if len(m.imageMarkers) > 0 {
		m = m.applyMarkerPostEdit(msg)
	}

	m = m.maybeReopenPickers()
	m.relayoutInput()
	return m, cmd
}

func (m *Model) tryDeleteMarker(msg tea.KeyPressMsg) (*Model, bool) {
	value := m.input.Value()
	runeOff := cursorRuneOffset(value, m.input.Line(), m.cursorCol())

	if msg.Code == tea.KeyBackspace {
		if idx, _, atEnd, _ := markerAtCursor(value, runeOff, m.imageMarkers); atEnd && idx >= 0 {
			removedLen := len([]rune(m.imageMarkers[idx].label))
			value = removeMarkerFromValue(value, m.imageMarkers[idx])
			m.imageMarkers = slices.Delete(m.imageMarkers, idx, idx+1)
			value, m.imageMarkers = renumberMarkers(value, m.imageMarkers)
			m.restoreCursorFromRuneOffset(value, runeOff-removedLen)
			m.relayoutInput()
			return m, true
		}
	}

	if msg.Code == tea.KeyDelete {
		if idx, atStart, _, _ := markerAtCursor(value, runeOff, m.imageMarkers); atStart && idx >= 0 {
			value = removeMarkerFromValue(value, m.imageMarkers[idx])
			m.imageMarkers = slices.Delete(m.imageMarkers, idx, idx+1)
			value, m.imageMarkers = renumberMarkers(value, m.imageMarkers)
			m.restoreCursorFromRuneOffset(value, runeOff)
			m.relayoutInput()
			return m, true
		}
	}

	return m, false
}

func (m *Model) applyMarkerPostEdit(msg tea.KeyPressMsg) *Model {
	if msg.Code == tea.KeyLeft || msg.Code == tea.KeyRight {
		direction := 1
		if msg.Code == tea.KeyLeft {
			direction = -1
		}
		value := m.input.Value()
		runeOff := cursorRuneOffset(value, m.input.Line(), m.cursorCol())
		newOff := snapCursorPastMarkers(value, runeOff, m.imageMarkers, direction)
		if newOff != runeOff {
			m.restoreCursorFromRuneOffset(value, newOff)
		}
	}

	if isEditKey(msg) {
		value := m.input.Value()
		newValue, newMarkers := reconcileMarkers(value, m.imageMarkers)
		if newValue != value {
			runeOff := cursorRuneOffset(value, m.input.Line(), m.cursorCol())
			m.imageMarkers = newMarkers
			m.input.SetValue(newValue)
			maxOff := len([]rune(newValue))
			if runeOff > maxOff {
				runeOff = maxOff
			}
			m.restoreCursorFromRuneOffset(newValue, runeOff)
		} else {
			m.imageMarkers = newMarkers
		}
	}

	return m
}

func isEditKey(msg tea.KeyPressMsg) bool {
	switch msg.Code {
	case tea.KeyBackspace, tea.KeyDelete:
		return true
	}
	if isCtrl(msg, 'k') || isCtrl(msg, 'u') || isCtrl(msg, 'w') {
		return true
	}
	// Also check for printable characters (tea.KeyRunes equivalent)
	if msg.Text != "" {
		return true
	}
	return false
}

func (m *Model) maybeReopenPickers() *Model {
	if !m.filePicker.IsOpen() && len(m.filePicker.allEntries) > 0 {
		if _, _, _, ok := m.activeComposerToken('@'); ok {
			m.filePicker.OverlayShell = m.filePicker.openShell()
			m.filePicker.width = m.width
			m.filePicker.height = m.height
			m.syncFilePickerWithComposer()
		}
	}

	if !m.slashOverlay.IsOpen() && len(m.slashOverlay.allItems) > 0 {
		if _, start, _, ok := m.activeComposerToken('/'); ok && start == 0 {
			m.slashOverlay.OverlayShell = m.slashOverlay.openShell()
			m.slashOverlay.width = m.width
			m.slashOverlay.height = m.height
			m.syncSlashOverlayWithComposer()
		}
	}
	return m
}

func (m *Model) handleTabKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	current := m.input.Value()
	if !strings.HasPrefix(current, "/") {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	candidates := m.completionCandidates
	if len(candidates) == 0 {
		candidates = buildCompletionCandidates(current, m.skillNames, m.oneshotRunning)
		if len(candidates) == 0 {
			return m, nil
		}
		m.completionCandidates = candidates
		m.completionIdx = 0
	} else {
		m.completionIdx = (m.completionIdx + 1) % len(candidates)
	}
	m.input.SetValue(candidates[m.completionIdx])
	m.input.CursorEnd()
	return m, nil
}

func (m *Model) handleKeyUp(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.fileHistoryIdx >= 0 && m.fileHistoryIdx < len(m.fileHistory)-1 {
		m.fileHistoryIdx++
		m.input.SetValue(m.fileHistory[m.fileHistoryIdx])
		return m, nil
	}
	if len(m.fileHistory) > 0 && m.fileHistoryIdx < 0 {
		m.historyDraft = m.input.Value()
		m.fileHistoryIdx = 0
		m.input.SetValue(m.fileHistory[0])
		return m, nil
	}
	m.fileHistoryIdx = -1
	m.historyIdx = -1
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleKeyDown(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.fileHistoryIdx > 0 {
		m.fileHistoryIdx--
		m.input.SetValue(m.fileHistory[m.fileHistoryIdx])
		return m, nil
	}
	if m.fileHistoryIdx == 0 {
		m.input.SetValue(m.historyDraft)
		m.fileHistoryIdx = -1
		return m, nil
	}
	m.fileHistoryIdx = -1
	m.historyIdx = -1
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) executeSteerAction() tea.Model {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m
	}
	images := m.pendingImageBlocks()
	if !m.oneshotRunning && m.controller != nil {
		_ = m.controller.Handle(context.Background(), interactive.SteerPrompt{Text: text, Images: images})
	}
	// Send to oneshot steer channel if active (non-blocking)
	if m.oneshotSteerCh != nil {
		select {
		case m.oneshotSteerCh <- agent.SteerMessage{Text: text, Images: images}:
		default:
			// Channel full or closed, skip
		}
	}
	m.input.Reset()
	m.imageMarkers = nil
	m.content.AppendPendingSteer(text)
	m.steerQueued = true
	m.syncInputChrome()
	m.syncViewport()
	m.relayoutInput()
	return m
}
