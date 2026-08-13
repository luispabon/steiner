package tui

import tea "charm.land/bubbletea/v2"

func (m *Model) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m = m.moveApprovalSelection(-1)
		m.syncViewport()
		return m, nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m = m.moveApprovalSelection(1)
		m.syncViewport()
		return m, nil
	case tea.KeyEnter:
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	case tea.KeyEsc:
		return m.executeApprovalDecision(ApprovalDecisionDeny)
	}
	if isCtrl(msg, 'c') || isCtrl(msg, 'd') {
		return m.executeInterruptAction(), nil
	}
	// Handle printable characters (tea.KeyRunes equivalent)
	if msg.Text != "" {
		switch msg.String() {
		case "y":
			return m.executeApprovalDecision(ApprovalDecisionAllowOnce)
		case "a":
			return m.executeApprovalDecision(ApprovalDecisionAlwaysAllow)
		case "n":
			return m.executeApprovalDecision(ApprovalDecisionDeny)
		}
	}
	return m, nil
}
