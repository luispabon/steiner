package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
)

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) recentWheelMouseInput() bool {
	if m.lastWheelMouseAt.IsZero() {
		return false
	}
	return time.Since(m.lastWheelMouseAt) <= 200*time.Millisecond
}

func (m Model) handleOverlayKeyMsg(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	if !m.hasOpenOverlay() {
		return false, m, nil
	}
	for _, handler := range overlayKeyHandlers {
		if !handler.matches(m) {
			continue
		}
		next := m
		cmd := handler.handle(&next, msg)
		return true, next, cmd
	}
	return false, m, nil
}

func (m Model) resetCompletionState(msg tea.KeyMsg) Model {
	if msg.Type == tea.KeyTab {
		return m
	}
	m.completionCandidates = nil
	m.completionIdx = 0
	return m
}

func (m Model) hasActiveConversation() bool {
	return m.content.streamingPhase != "" || m.status.mode == "running" || m.status.mode == "approval"
}

func (m Model) handleConversationKeyMsg(msg tea.KeyMsg, activeConversation bool) (bool, tea.Model) {
	if activeConversation && (msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlD) {
		return true, m.executeInterruptAction()
	}
	// During an active run, Enter queues a steer message instead of submitting normally.
	if activeConversation && msg.Type == tea.KeyEnter && !m.approval.active && !key.Matches(msg, m.input.KeyMap.InsertNewline) {
		return true, m.executeSteerAction()
	}
	if msg.String() == "?" && strings.TrimSpace(m.input.Value()) == "" {
		m.helpVisible = !m.helpVisible
		return true, m
	}
	if msg.Type == tea.KeyEsc && m.helpVisible {
		m.helpVisible = false
		return true, m
	}
	return false, m
}

func (m Model) handleNavigationKeyMsg(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD:
		if m.controller == nil {
			return true, m, tea.Quit
		}
		return true, m.openExitModal(), nil
	case tea.KeyCtrlB:
		m.sidebar.Toggle()
		m.layout()
		return true, m, nil
	case tea.KeyCtrlT:
		m.openContextOverlayImmediate()
		return true, m, nil
	case tea.KeyCtrlX:
		m.content.ToggleLastDelegationOutput()
		m.syncViewport()
		return true, m, nil
	case tea.KeyCtrlV:
		return true, m, pasteImageCmd()
	case tea.KeyTab:
		next, cmd := m.handleTabKey(msg)
		return true, next, cmd
	case tea.KeyUp:
		next, cmd := m.handleKeyUp(msg)
		return true, next, cmd
	case tea.KeyDown:
		next, cmd := m.handleKeyDown(msg)
		return true, next, cmd
	case tea.KeyPgUp:
		m.scrollUp(max(1, m.viewport.Height))
		return true, m, nil
	case tea.KeyPgDown:
		m.scrollDown(max(1, m.viewport.Height))
		return true, m, nil
	case tea.KeyEsc:
		return m.handleSelectionEscKey()
	default:
		return false, m, nil
	}
}

// handleSelectionEscKey clears an active selection when Esc is pressed.
// Returns false (not consumed) when no selection exists so Esc can fall through.
func (m Model) handleSelectionEscKey() (bool, tea.Model, tea.Cmd) {
	if m.selection.hasSelection() {
		m.selection = m.selection.clear()
		return true, m, nil
	}
	return false, m, nil
}

func (m Model) hasOpenOverlay() bool {
	return m.modelPicker.IsOpen() ||
		m.workflowHandoff.IsOpen() ||
		m.exitModal.IsOpen() ||
		m.slashOverlay.IsOpen() ||
		m.fileList.IsOpen() ||
		m.contextOverlay.IsOpen() ||
		m.filePicker.IsOpen() ||
		m.sessionPicker.IsOpen() ||
		m.oneshotResumePicker.IsOpen() ||
		m.planPicker.IsOpen()
}

func (m Model) openContextOverlayImmediate() {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.RequestContextReport{}); err != nil {
		m.content.AppendLine(fmt.Sprintf("status: %v", err))
	}
}

func (m Model) handleComposerKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter && (!m.approval.active && !key.Matches(msg, m.input.KeyMap.InsertNewline)) {
		return m.handleEnter()
	}

	if len(m.imageMarkers) > 0 {
		if result, handled := m.tryDeleteMarker(msg); handled {
			return result, nil
		}
	}

	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
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

func (m Model) tryDeleteMarker(msg tea.KeyMsg) (Model, bool) {
	value := m.input.Value()
	runeOff := cursorRuneOffset(value, m.input.Line(), m.cursorCol())

	if msg.Type == tea.KeyBackspace {
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

	if msg.Type == tea.KeyDelete {
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

func (m Model) applyMarkerPostEdit(msg tea.KeyMsg) Model {
	if msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight {
		direction := 1
		if msg.Type == tea.KeyLeft {
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

func isEditKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyBackspace, tea.KeyDelete, tea.KeyRunes,
		tea.KeyCtrlK, tea.KeyCtrlU, tea.KeyCtrlW:
		return true
	}
	return false
}

func (m Model) maybeReopenPickers() Model {
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

func (m Model) handleTabKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	current := m.input.Value()
	if !strings.HasPrefix(current, "/") {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	candidates := m.completionCandidates
	if len(candidates) == 0 {
		candidates = buildCompletionCandidates(current, m.skillNames, m.modelNames, m.oneshotRunning)
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

func (m Model) handleKeyUp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) handleKeyDown(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) executeSteerAction() tea.Model {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m
	}
	if !m.oneshotRunning && m.controller != nil {
		_ = m.controller.Handle(context.Background(), interactive.SteerPrompt{Text: text})
	}
	// Send to oneshot steer channel if active (non-blocking)
	if m.oneshotSteerCh != nil {
		select {
		case m.oneshotSteerCh <- text:
		default:
			// Channel full or closed, skip
		}
	}
	m.input.Reset()
	m.content.AppendPendingSteer(text)
	m.steerQueued = true
	m.syncInputChrome()
	m.syncViewport()
	return m
}
