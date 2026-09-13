package tui

import (
	tea "charm.land/bubbletea/v2"
)

func exitModalSpec() confirmModalSpec {
	return confirmModalSpec{
		Title:         "exit",
		Heading:       "Exit steiner?",
		Body:          "Leave the interactive session and return to the shell.",
		LeftLabel:     "Cancel",
		RightLabel:    "Exit",
		DefaultAction: confirmModalRight,
	}
}

func (m *Model) openExitModal() *Model {
	m.exitModal = openConfirmModal(m.width, m.height, exitModalSpec())
	return m
}

//nolint:unparam // handler returns tea.Model to match overlay dispatch conventions
func (m *Model) handleExitModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrl(msg, 'c') || isCtrl(msg, 'd') {
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	var result confirmModalResult
	m.exitModal, result = m.exitModal.handleKey(msg)
	if result != confirmModalResultChosen {
		return m, nil
	}
	if m.exitModal.selectedAction() == confirmModalRight {
		// Modal intentionally stays open: beginExitFlow closes it when it enters the
		// worktree counting phase; on the direct doExit path it remains open until
		// the runtime quits (asserted by model_test.go).
		return m.beginExitFlow()
	}
	m.exitModal = m.exitModal.close()
	return m, nil
}
