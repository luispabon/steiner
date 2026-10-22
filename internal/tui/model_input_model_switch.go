package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
)

// openModelPickerFromKeybind opens the model picker for alt+m. The composer is
// not touched, so the draft and cursor survive the picker lifecycle.
func (m *Model) openModelPickerFromKeybind() {
	m.openModelPickerFromSlashCommand()
	m.pickerFromKeybind = true
}

// openProfilePickerFromKeybind opens the profile picker for alt+p. The composer
// is not touched, so the draft and cursor survive the picker lifecycle.
func (m *Model) openProfilePickerFromKeybind() {
	m.openProfilePickerFromSlashCommand()
	m.pickerFromKeybind = true
}

// settleComposerAfterPicker runs when a picker closes for good. It clears the
// composer for slash-opened pickers. Keybind-opened pickers never touched the
// composer, so their draft and cursor are left alone. The origin flag is
// cleared so it cannot leak into a later slash-opened flow.
func (m *Model) settleComposerAfterPicker() {
	if !m.pickerFromKeybind {
		m.input.Reset()
	}
	m.pickerFromKeybind = false
}

// modelSwitchChangesEffective reports whether switching to name with reasoning
// changes the active model or its effective reasoning. Reasoning is compared by
// value. A nil reasoning keeps the target's stored override, which the TUI can
// only read for the active alias.
func (m *Model) modelSwitchChangesEffective(name string, reasoning *provider.ReasoningOverride) bool {
	if name != m.currentModelAlias {
		return true
	}
	target := m.currentReasoningOverrideFor(name)
	if reasoning != nil {
		target = *reasoning
	}
	return target != m.currentReasoningOverrideFor(m.currentModelAlias)
}

// modelSwitchNeedsConfirm reports whether a switch that changes the model or
// its effective reasoning must confirm first. A oneshot run or a non-empty
// conversation needs confirmation. A controller that cannot report its
// conversation is treated as empty. The production controller,
// *interactive.Session, always reports it.
func (m *Model) modelSwitchNeedsConfirm(name string, reasoning *provider.ReasoningOverride) bool {
	if !m.modelSwitchChangesEffective(name, reasoning) {
		return false
	}
	if m.oneshotRunning {
		return true
	}
	r, ok := m.controller.(conversationReader)
	return ok && len(r.Conversation()) > 0
}

// requestModelSwitch is the single entry point for model switches from the
// model picker, the reasoning step, and typed /model. It commits immediately
// unless the switch changes the model or its reasoning while a conversation is
// non-empty, since switching invalidates the cached system-prompt prefix. In
// that case it opens a confirmation modal and commits on confirm.
func (m *Model) requestModelSwitch(name string, reasoning *provider.ReasoningOverride) {
	if !m.modelSwitchNeedsConfirm(name, reasoning) {
		m.commitModelSwitch(name, reasoning)
		return
	}
	body := "Invalidates the prompt cache."
	if m.switchAppliesAfterTurn() {
		body += " Applies after this turn."
	}
	m.pendingModelName = name
	m.pendingModelReasoning = reasoning
	m.modelSwitchConfirm = openConfirmModal(m.width, m.height, confirmModalSpec{
		Title:         "model",
		Heading:       fmt.Sprintf("Switch model to %s?", name),
		Body:          body,
		LeftLabel:     "Cancel",
		RightLabel:    fmt.Sprintf("Switch to %s", name),
		DefaultAction: confirmModalLeft,
	})
}

// commitModelSwitch dispatches the switch to the controller and updates the
// model view. On controller failure the view is left unchanged. The status line
// notes when the switch only applies after the in-flight turn.
func (m *Model) commitModelSwitch(name string, reasoning *provider.ReasoningOverride) {
	afterTurn := m.switchAppliesAfterTurn()
	providerBaseURL := m.sidebar.provider
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: name, Reasoning: reasoning}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", name))
			return
		}
	}
	if baseURL, ok := m.modelBaseURLs[name]; ok {
		providerBaseURL = baseURL
	}
	if reasoning != nil {
		m.reasoningLabels[name] = reasoningOverrideLabel(*reasoning)
	}
	m.applyModelSelection(name, providerBaseURL)
	status := fmt.Sprintf("status: model switched to %s", name)
	if afterTurn {
		status += " (applies after this turn)"
	}
	m.content.AppendLine(status)
}

// handleModelSwitchConfirmModalKey resolves the model switch confirmation.
// Confirm commits the pending switch; cancel and esc drop it.
func (m *Model) handleModelSwitchConfirmModalKey(msg tea.KeyPressMsg) {
	var result confirmModalResult
	m.modelSwitchConfirm, result = m.modelSwitchConfirm.handleKey(msg)
	switch result {
	case confirmModalResultChosen:
		m.modelSwitchConfirm = m.modelSwitchConfirm.close()
		name, reasoning := m.pendingModelName, m.pendingModelReasoning
		m.clearPendingModelSwitch()
		if m.modelSwitchConfirm.selectedAction() == confirmModalRight {
			m.commitModelSwitch(name, reasoning)
			m.relayoutInput()
			m.syncViewport()
		}
	case confirmModalResultDismissed:
		m.clearPendingModelSwitch()
	}
}

func (m *Model) clearPendingModelSwitch() {
	m.pendingModelName = ""
	m.pendingModelReasoning = nil
}
