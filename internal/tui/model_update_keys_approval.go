package tui

import tea "charm.land/bubbletea/v2"

func (m *Model) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if (msg.Code == tea.KeyEsc || isCtrl(msg, 'c') || isCtrl(msg, 'd')) && m.content.HasActiveDelegations() {
		return m.openDelegateCancelModal(), nil
	}
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
		if decision, ok := m.approvalRuneDecision(msg.String()); ok {
			return m.executeApprovalDecision(decision)
		}
	}
	return m, nil
}

// approvalRuneDecision maps a printable approval key to its decision. A
// no-persist request cannot be granted for the session, so its 'a' key is
// inert and reports no decision.
func (m *Model) approvalRuneDecision(key string) (ApprovalDecision, bool) {
	switch key {
	case "y":
		return ApprovalDecisionAllowOnce, true
	case "a":
		if m.approval.noPersist {
			return "", false
		}
		return ApprovalDecisionAlwaysAllow, true
	case "n":
		return ApprovalDecisionDeny, true
	}
	return "", false
}
