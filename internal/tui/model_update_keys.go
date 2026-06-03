package tui

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
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
	switch {
	case m.exitModal.IsOpen():
		next, cmd := m.handleExitModalKey(msg)
		return true, next, cmd
	case m.palette.IsOpen():
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
		return true, m, cmd
	case m.slashOverlay.IsOpen():
		next, cmd := m.handleSlashOverlayKey(msg)
		return true, next, cmd
	case m.fileList.IsOpen():
		var cmd tea.Cmd
		m.fileList, cmd = m.fileList.Update(msg)
		return true, m, cmd
	case m.contextOverlay.IsOpen():
		return true, m.handleContextOverlayKey(msg), nil
	case m.filePicker.IsOpen():
		next, cmd := m.handleFilePickerKey(msg)
		return true, next, cmd
	case m.sessionPicker.IsOpen():
		next, cmd := m.handleSessionPickerKey(msg)
		return true, next, cmd
	case m.planPicker.IsOpen():
		next, cmd := m.handlePlanPickerKey(msg)
		return true, next, cmd
	case m.modelPicker.IsOpen():
		next, cmd := m.handleModelPickerKey(msg)
		return true, next, cmd
	default:
		return false, m, nil
	}
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
	case tea.KeyCtrlP:
		m.palette = m.palette.Open()
		return true, m, nil
	case tea.KeyCtrlB:
		m.sidebar.Toggle()
		m.layout()
		return true, m, nil
	case tea.KeyCtrlX:
		m.content.ToggleLastDelegationOutput()
		m.syncViewport()
		return true, m, nil
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

func (m Model) handleComposerKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter && (!m.approval.active && !key.Matches(msg, m.input.KeyMap.InsertNewline)) {
		return m.handleEnter()
	}

	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r == '/' && strings.TrimSpace(m.input.Value()) == "" {
				// Open slash overlay when "/" is typed at start of input
				items := m.buildSlashOverlayItems()
				m.slashOverlay = m.slashOverlay.Open(items)
				m.slashOverlay.width = m.width
				m.slashOverlay.height = m.height
				// Mirror "/" into the input box so typed text is visible
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
	m = m.maybeReopenPickers()
	return m, cmd
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

func (m Model) handleExitModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyLeft, tea.KeyUp:
		m.exitModal = m.exitModal.moveSelection(-1)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.exitModal = m.exitModal.moveSelection(1)
	case tea.KeyEnter, tea.KeyCtrlC, tea.KeyCtrlD:
		return m.confirmExitModal()
	case tea.KeyEsc:
		m.exitModal = m.exitModal.closeExitModal()
	}
	return m, nil
}

func (m Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyLeft, tea.KeyUp:
		return m.moveApprovalSelection(-1), nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		return m.moveApprovalSelection(1), nil
	case tea.KeyEnter:
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	case tea.KeyEsc:
		return m.executeApprovalDecision(ApprovalDecisionDeny)
	default:
		return m, nil
	}
}

func (m Model) handleContextOverlayKey(msg tea.KeyMsg) tea.Model {
	switch msg.Type {
	case tea.KeyEsc:
		m.contextOverlay = m.contextOverlay.closeContextOverlay()
	case tea.KeyUp:
		m.contextOverlay = m.contextOverlay.scrollUp(1)
	case tea.KeyDown:
		m.contextOverlay = m.contextOverlay.scrollDown(1)
	case tea.KeyPgUp:
		m.contextOverlay = m.contextOverlay.scrollUp(contextOverlayMaxLines)
	case tea.KeyPgDown:
		m.contextOverlay = m.contextOverlay.scrollDown(contextOverlayMaxLines)
	}
	return m
}

func (m Model) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.replaceComposerToken('@', "")
		m.filePicker = m.filePicker.Close()
	case tea.KeyEnter, tea.KeyTab:
		if m.filePicker.selection >= 0 && len(m.filePicker.candidates) > 0 {
			selected := m.filePicker.candidates[m.filePicker.selection]
			m.replaceComposerToken('@', selected+" ")
			m.filePicker = m.filePicker.Close()
		}
	case tea.KeyUp, tea.KeyDown:
		var cmd tea.Cmd
		m.filePicker, cmd = m.filePicker.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncFilePickerWithComposer()
		return m, cmd
	}
	return m, nil
}

func (m Model) handleSessionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.sessionPicker = m.sessionPicker.Close()
	case tea.KeyEnter:
		if m.sessionPicker.selection >= 0 && len(m.sessionPicker.candidates) > 0 {
			selected := m.sessionPicker.candidates[m.sessionPicker.selection]
			m.sessionPicker = m.sessionPicker.Close()
			if m.controller != nil {
				if err := m.controller.Handle(context.Background(), interactive.LoadSession{SessionID: selected.ID}); err != nil {
					m.content.AppendLine("status: " + err.Error())
				}
			}
		}
	default:
		var cmd tea.Cmd
		m.sessionPicker, cmd = m.sessionPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handlePlanPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.planPicker = m.planPicker.Close()
	case tea.KeyEnter:
		if name := m.planPicker.SelectedName(); name != "" {
			m.planPicker = m.planPicker.Close()
			m.input.SetValue(m.input.Value() + name)
			m.input.CursorEnd()
		}
	default:
		var cmd tea.Cmd
		m.planPicker, cmd = m.planPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleModelPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.modelPicker = m.modelPicker.Close()
		m.input.Reset()
		m.historyIdx = 0
	case tea.KeyEnter:
		if name := m.modelPicker.SelectedName(); name != "" {
			m.modelPicker = m.modelPicker.Close()
			m.input.Reset()
			m.historyIdx = 0
			return m.executeModelAction(name)
		}
	default:
		var cmd tea.Cmd
		m.modelPicker, cmd = m.modelPicker.Update(msg)
		return m, cmd
	}
	return m, nil
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
		candidates = buildCompletionCandidates(current, m.skillNames, m.modelNames)
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

func (m Model) handleSlashOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.replaceComposerToken('/', "")
		m.slashOverlay = m.slashOverlay.Close()
	case tea.KeyEnter, tea.KeyTab:
		if selected := m.slashOverlay.SelectedItem(); selected != nil {
			m.replaceComposerToken('/', selected.command+" ")
			m.slashOverlay = m.slashOverlay.Close()
			switch selected.command {
			case "/model":
				m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
				m.modelPicker.width = m.width
				m.modelPicker.height = m.height
				m.historyIdx = 0
			case "/implement", "/review":
				m.planPicker = m.planPicker.Open(selected.command)
				m.planPicker.width = m.width
				m.planPicker.height = m.height
			}
		}
	case tea.KeyUp, tea.KeyDown:
		var cmd tea.Cmd
		m.slashOverlay, cmd = m.slashOverlay.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		val := m.input.Value()
		switch {
		case strings.TrimRight(val, " ") == "/model" && strings.HasSuffix(val, " "):
			m.slashOverlay = m.slashOverlay.Close()
			m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
			m.modelPicker.width = m.width
			m.modelPicker.height = m.height
			m.historyIdx = 0
			return m, cmd
		case strings.TrimRight(val, " ") == "/implement" && strings.HasSuffix(val, " "):
			m.slashOverlay = m.slashOverlay.Close()
			m.planPicker = m.planPicker.Open("/implement")
			m.planPicker.width = m.width
			m.planPicker.height = m.height
			return m, cmd
		case strings.TrimRight(val, " ") == "/review" && strings.HasSuffix(val, " "):
			m.slashOverlay = m.slashOverlay.Close()
			m.planPicker = m.planPicker.Open("/review")
			m.planPicker.width = m.width
			m.planPicker.height = m.height
			return m, cmd
		}
		m.syncSlashOverlayWithComposer()
		return m, cmd
	}
	return m, nil
}

func (m *Model) syncSlashOverlayWithComposer() {
	if !m.slashOverlay.IsOpen() {
		return
	}
	token, _, _, ok := m.activeComposerToken('/')
	if !ok {
		m.slashOverlay = m.slashOverlay.Close()
		return
	}
	m.slashOverlay.syncQuery(token)
}

func (m *Model) syncFilePickerWithComposer() {
	if !m.filePicker.IsOpen() {
		return
	}
	token, _, _, ok := m.activeComposerToken('@')
	if !ok {
		m.filePicker = m.filePicker.Close()
		return
	}
	m.filePicker.syncQuery(strings.TrimPrefix(token, "@"))
}

func (m *Model) replaceComposerToken(prefix rune, replacement string) {
	_, start, end, ok := m.activeComposerToken(prefix)
	if !ok {
		return
	}
	value := []rune(m.input.Value())
	next := string(value[:start]) + replacement + string(value[end:])
	m.input.SetValue(next)
	m.input.CursorEnd()
}

func (m Model) activeComposerToken(prefix rune) (string, int, int, bool) {
	value := m.input.Value()
	cursor := composerCursorOffset(m.input)
	return composerTokenAtCursor(value, cursor, prefix)
}

func composerCursorOffset(input textarea.Model) int {
	lines := strings.Split(input.Value(), "\n")
	line := input.Line()
	if line < 0 {
		line = 0
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}
	offset := 0
	for i := 0; i < line; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	lineInfo := input.LineInfo()
	col := lineInfo.StartColumn + lineInfo.ColumnOffset
	lineRunes := []rune(lines[line])
	if col < 0 {
		col = 0
	}
	if col > len(lineRunes) {
		col = len(lineRunes)
	}
	return offset + col
}

func composerTokenAtCursor(value string, cursor int, prefix rune) (string, int, int, bool) {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	start := cursor
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start >= len(runes) || runes[start] != prefix {
		return "", 0, 0, false
	}

	end := cursor
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	return string(runes[start:end]), start, end, true
}

func (m Model) executeSteerAction() tea.Model {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m
	}
	if m.controller != nil {
		_ = m.controller.Handle(context.Background(), interactive.SteerPrompt{Text: text})
	}
	m.input.Reset()
	m.content.AppendPendingSteer(text)
	m.steerQueued = true
	m.syncInputChrome()
	return m
}
