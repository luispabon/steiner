package tui

import (
	"context"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
)

func (m Model) handleExitModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.exitModal = m.exitModal.moveSelection(-1)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.exitModal = m.exitModal.moveSelection(1)
	case tea.KeyEnter:
		return m.confirmExitModal()
	case tea.KeyEsc:
		m.exitModal = m.exitModal.closeExitModal()
	}
	if isCtrl(msg, 'c') || isCtrl(msg, 'd') {
		return m.confirmExitModal()
	}
	return m, nil
}

func (m Model) handleContextOverlayKey(msg tea.KeyPressMsg) tea.Model {
	switch msg.Code {
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

func (m Model) handleFilePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
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

func (m Model) handleSessionPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		m.sessionPicker = m.sessionPicker.Close()
	case tea.KeyEnter:
		if m.sessionPicker.selection >= 0 && len(m.sessionPicker.candidates) > 0 {
			selected := m.sessionPicker.candidates[m.sessionPicker.selection]
			m.sessionPicker = m.sessionPicker.Close()
			if m.sessionResetCleanup != nil {
				m.sessionResetCleanup()
			}
			if m.controller != nil {
				ctrl := m.controller
				sid := selected.ID
				return m, func() tea.Msg {
					_ = ctrl.Handle(context.Background(), interactive.LoadSession{SessionID: sid})
					return nil
				}
			}
		}
	}
	// Handle printable characters (tea.KeyRunes equivalent)
	if msg.Text != "" {
		if msg.String() == "f" {
			if m.sessionPicker.selection >= 0 && len(m.sessionPicker.candidates) > 0 {
				selected := m.sessionPicker.candidates[m.sessionPicker.selection]
				m.sessionPicker = m.sessionPicker.Close()
				if m.sessionResetCleanup != nil {
					m.sessionResetCleanup()
				}
				if m.controller != nil {
					ctrl := m.controller
					sid := selected.ID
					return m, func() tea.Msg {
						_ = ctrl.Handle(context.Background(), interactive.ForkSavedSession{SessionID: sid})
						return nil
					}
				}
			}
		} else {
			var cmd tea.Cmd
			m.sessionPicker, cmd = m.sessionPicker.Update(msg)
			return m, cmd
		}
	} else {
		var cmd tea.Cmd
		m.sessionPicker, cmd = m.sessionPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleOneshotResumePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		m.oneshotResumePicker = m.oneshotResumePicker.Close()
	case tea.KeyEnter:
		if runID := m.oneshotResumePicker.SelectedRunID(); runID != "" {
			m.oneshotResumePicker = m.oneshotResumePicker.Close()
			return m.executeResumeOneshotAction(runID)
		}
	default:
		var cmd tea.Cmd
		m.oneshotResumePicker, cmd = m.oneshotResumePicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handlePlanPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
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

func (m Model) handleAccentPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		m.accentPicker = m.accentPicker.Close()
		m.input.Reset()
		m.historyIdx = 0
	case tea.KeyEnter:
		if name := m.accentPicker.SelectedName(); name != "" {
			m.accentPicker = m.accentPicker.Close()
			m.input.Reset()
			m.historyIdx = 0
			return m.executeSetAccentAction(name)
		}
	default:
		var cmd tea.Cmd
		m.accentPicker, cmd = m.accentPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleModelPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		m.modelPicker = m.modelPicker.Close()
		if !m.modelPicker.IsWorkflowHandoff() {
			m.input.Reset()
			m.historyIdx = 0
		}
	case tea.KeyEnter:
		if name := m.modelPicker.SelectedName(); name != "" {
			isWorkflowHandoff := m.modelPicker.IsWorkflowHandoff()
			m.modelPicker = m.modelPicker.Close()
			if isWorkflowHandoff {
				m.workflowHandoff.modelAlias = name
				m.workflowHandoff.modelSource = "selected for handoff"
				return m, nil
			}
			caps := m.modelReasoningCapabilities[name]
			if len(caps.SupportedEfforts) == 0 {
				m = m.resolveReasoningForAliasIfPending(name)
				caps = m.modelReasoningCapabilities[name]
			}
			if len(caps.SupportedEfforts) == 0 {
				m.input.Reset()
				m.historyIdx = 0
				return m.executeModelAction(name, nil)
			}
			m.reasoningPicker = m.reasoningPicker.Open(name, caps, m.currentReasoningOverrideFor(name), m.modelReasoningEfforts[name])
			m.reasoningPicker.width = m.width
			m.reasoningPicker.height = m.height
			return m, nil
		}
	default:
		var cmd tea.Cmd
		m.modelPicker, cmd = m.modelPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleReasoningPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		modelName := m.reasoningPicker.modelName
		m.reasoningPicker = m.reasoningPicker.Close()
		m.modelPicker = m.modelPicker.Open(m.modelNames, modelName)
		m.modelPicker.width = m.width
		m.modelPicker.height = m.height
	case tea.KeyEnter:
		if opt, ok := m.reasoningPicker.SelectedOption(); ok {
			modelName := m.reasoningPicker.modelName
			m.reasoningPicker = m.reasoningPicker.Close()
			m.input.Reset()
			m.historyIdx = 0
			override := reasoningOverrideFromOption(opt)
			return m.executeModelAction(modelName, &override)
		}
	default:
		var cmd tea.Cmd
		m.reasoningPicker, cmd = m.reasoningPicker.Update(msg)
		return m, cmd
	}
	return m, nil
}

// reasoningOverrideProvider is the narrow controller capability
// currentReasoningOverrideFor needs: reporting the session's active
// reasoning override.
type reasoningOverrideProvider interface {
	CurrentReasoningOverride() provider.ReasoningOverride
}

// currentReasoningOverrideFor returns the session's stored reasoning
// override for modelName when it is the currently active model alias. The
// session only tracks the override for the active alias without an
// arbitrary-alias getter, so other aliases report the zero value (no
// override).
func (m Model) currentReasoningOverrideFor(modelName string) provider.ReasoningOverride {
	if m.controller == nil || modelName != m.currentModelAlias {
		return provider.ReasoningOverride{}
	}
	sess, ok := m.controller.(reasoningOverrideProvider)
	if !ok {
		return provider.ReasoningOverride{}
	}
	return sess.CurrentReasoningOverride()
}

// reasoningOverrideFromOption converts a picker selection into the override
// value sent to the controller.
func reasoningOverrideFromOption(opt reasoningEffortOption) provider.ReasoningOverride {
	if opt.IsProviderDefault {
		return provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault}
	}
	return provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: opt.Value}
}

func (m Model) handleSlashOverlayKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeySpace {
		msg = tea.KeyPressMsg{Text: " "}
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.replaceComposerToken('/', "")
		m.slashOverlay = m.slashOverlay.Close()
	case tea.KeyEnter, tea.KeyTab:
		if selected := m.slashOverlay.SelectedItem(); selected != nil {
			m = m.completeSlashOverlayCommand(selected.command)
		}
	case tea.KeyUp, tea.KeyDown:
		var cmd tea.Cmd
		m.slashOverlay, cmd = m.slashOverlay.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if next, ok := m.openPickerForCompletedSlashCommand(m.input.Value()); ok {
			return next, cmd
		}
		m.syncSlashOverlayWithComposer()
		return m, cmd
	}
	return m, nil
}

func (m Model) completeSlashOverlayCommand(command string) Model {
	m.replaceComposerToken('/', command+" ")
	m.slashOverlay = m.slashOverlay.Close()
	switch command {
	case "/implement", "/review":
		m.planPicker = m.planPicker.Open(command)
		m.planPicker.width = m.width
		m.planPicker.height = m.height
	case "/model":
		m = m.openModelPickerFromSlashCommand()
	case "/accent":
		m = m.openAccentPickerFromSlashCommand()
	}
	return m
}

func (m Model) openPickerForCompletedSlashCommand(value string) (Model, bool) {
	if !strings.HasSuffix(value, " ") {
		return m, false
	}
	command := strings.TrimRight(value, " ")
	switch command {
	case "/model":
		m.slashOverlay = m.slashOverlay.Close()
		return m.openModelPickerFromSlashCommand(), true
	case "/accent":
		m.slashOverlay = m.slashOverlay.Close()
		return m.openAccentPickerFromSlashCommand(), true
	case "/implement", "/review":
		m.slashOverlay = m.slashOverlay.Close()
		m.planPicker = m.planPicker.Open(command)
		m.planPicker.width = m.width
		m.planPicker.height = m.height
		return m, true
	}
	return m, false
}

func (m Model) openModelPickerFromSlashCommand() Model {
	m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height
	m.historyIdx = 0
	return m
}

func (m Model) openAccentPickerFromSlashCommand() Model {
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height
	m.historyIdx = 0
	return m
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
