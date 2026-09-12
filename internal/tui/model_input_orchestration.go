package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
)

// conversationReader reports the live conversation; implemented by
// *interactive.Session. Kept narrow so callers never need to type-assert
// m.controller to the concrete session type.
type conversationReader interface {
	Conversation() []agent.Message
}

// conversationEmpty reports whether the current conversation is empty and no
// oneshot run is in flight, which is when an orchestration-level switch can
// apply immediately without a confirmation dialog. A controller that does not
// expose conversationReader is treated as non-empty so the dialog is shown
// rather than silently switching underneath an unknown controller.
func (m *Model) conversationEmpty() bool {
	if m.oneshotRunning {
		return false
	}
	if r, ok := m.controller.(conversationReader); ok {
		return len(r.Conversation()) == 0
	}
	return false
}

// openOrchestrationPicker seeds and opens the orchestration picker on the
// current level, reporting the unavailable status line instead when
// sub-agents are disabled. Returns whether the picker was opened.
func (m *Model) openOrchestrationPicker() bool {
	if !m.subAgentsEnabled {
		m.content.AppendLine("status: orchestration unavailable: sub-agents are disabled in config")
		return false
	}
	m.orchestrationPicker.styles = m.styles
	m.orchestrationPicker = m.orchestrationPicker.Open(m.orchestrationLevel)
	m.orchestrationPicker.width = m.width
	m.orchestrationPicker.height = m.height
	return true
}

// executeOpenOrchestrationPickerAction handles bare "/orchestration" from the
// composer, opening the picker and mirroring the reset/relayout the other
// picker-opening commands perform.
func (m *Model) executeOpenOrchestrationPickerAction() (tea.Model, tea.Cmd) {
	if !m.openOrchestrationPicker() {
		m.input.Reset()
		m.historyIdx = 0
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}
	m.input.SetValue("/orchestration ")
	m.input.CursorEnd()
	m.historyIdx = 0
	return m, nil
}

// executeSetOrchestrationLevelAction handles "/orchestration low|standard",
// routing through requestOrchestrationLevel for the same switch-or-confirm
// flow the picker uses.
func (m *Model) executeSetOrchestrationLevelAction(level string) (tea.Model, tea.Cmd) {
	cmd := m.requestOrchestrationLevel(config.OrchestrationLevel(level))
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, cmd
}

// executeInvalidOrchestrationLevelAction reports an unrecognized
// "/orchestration" argument.
func (m *Model) executeInvalidOrchestrationLevelAction(arg string) (tea.Model, tea.Cmd) {
	m.content.AppendLine(fmt.Sprintf("status: invalid orchestration level %q (use low or standard)", arg))
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

// requestOrchestrationLevel is the single apply-or-confirm entry point used
// by both the direct "/orchestration <level>" command and the picker's Enter
// key: it switches immediately when safe (empty conversation, no oneshot in
// flight) and otherwise opens a confirmation modal, since switching
// invalidates the cached system-prompt prefix.
func (m *Model) requestOrchestrationLevel(level config.OrchestrationLevel) tea.Cmd {
	if !m.subAgentsEnabled {
		m.content.AppendLine("status: orchestration unavailable: sub-agents are disabled in config")
		return nil
	}
	if level == m.orchestrationLevel {
		return nil
	}
	if m.conversationEmpty() {
		return m.applyOrchestrationLevel(level)
	}
	body := "Invalidates the prompt cache."
	if m.activity.busy() || m.oneshotRunning {
		body += " Applies after this turn."
	}
	m.pendingOrchestrationLevel = level
	m.orchestrationConfirm = openConfirmModal(m.width, m.height, confirmModalSpec{
		Title:         "orchestration",
		Heading:       fmt.Sprintf("Switch orchestration to %s?", level),
		Body:          body,
		CancelLabel:   "Cancel",
		ConfirmLabel:  fmt.Sprintf("Switch to %s", level),
		DefaultAction: confirmModalCancel,
	})
	return nil
}

// applyOrchestrationLevel dispatches the level switch to the controller. It
// never sets m.orchestrationLevel or the sidebar directly — those update only
// via the OrchestrationLevelChangedEvent handler, so the model's view of the
// active level always reflects what the session actually applied.
func (m *Model) applyOrchestrationLevel(level config.OrchestrationLevel) tea.Cmd {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchOrchestrationLevel{Level: level}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: orchestration switch failed: %v", err))
		}
	}
	return nil
}
