package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

func worktreeCleanupModalSpec(count int) confirmModalSpec {
	return confirmModalSpec{
		Title:         "worktrees",
		Heading:       fmt.Sprintf("Clean up %d worktrees?", count),
		Body:          "Cleanup removes delegate branches and may discard unmerged child work.",
		LeftLabel:     "Exit without cleaning",
		RightLabel:    "Clean up on exit",
		DefaultAction: confirmModalLeft,
	}
}

//nolint:unparam // returns Model for modal helper consistency
func (m *Model) openWorktreeCleanupModal(width, height, count int) *Model {
	m.worktreeCleanupModal = openConfirmModal(width, height, worktreeCleanupModalSpec(count))
	return m
}

//nolint:unparam // handler returns tea.Model to match overlay dispatch conventions
func (m *Model) handleWorktreeCleanupModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrl(msg, 'c') || isCtrl(msg, 'd') {
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	var result confirmModalResult
	m.worktreeCleanupModal, result = m.worktreeCleanupModal.handleKey(msg)
	switch result {
	case confirmModalResultChosen:
		m.worktreeCleanupModal = m.worktreeCleanupModal.close()
		m.exitFlowPhase = exitFlowPhaseNone
		if m.worktreeCleanupModal.selectedAction() == confirmModalRight && m.worktreePlan != nil {
			m.worktreePlan.Request()
		}
		return m.doExit()
	case confirmModalResultDismissed:
		m.exitFlowPhase = exitFlowPhaseNone
	}
	return m, nil
}
