package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
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
	case tea.KeyCtrlC, tea.KeyCtrlD:
		return m.executeInterruptAction(), nil
	case tea.KeyRunes:
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
