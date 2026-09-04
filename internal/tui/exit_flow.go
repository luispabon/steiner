package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
)

type worktreeCountMsg struct {
	count int
	err   error
}

func (m *Model) doExit() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.RequestExit{}); err != nil {
			m.appendError(err)
		}
		return m, nil
	}
	return m, tea.Quit
}

func (m *Model) beginExitFlow() (tea.Model, tea.Cmd) {
	if m.exitFlowPhase != exitFlowPhaseNone {
		return m, nil
	}
	if m.worktreePlan == nil || m.status.mode == "running" {
		return m.doExit()
	}
	m.exitModal = m.exitModal.closeExitModal()
	m.exitFlowPhase = exitFlowPhaseCounting
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		count, err := m.worktreePlan.Count(ctx)
		return worktreeCountMsg{count: count, err: err}
	}
}

func (m *Model) handleWorktreeCountMsg(msg worktreeCountMsg) (tea.Model, tea.Cmd) {
	if m.exitFlowPhase != exitFlowPhaseCounting {
		return m, nil
	}
	if msg.err != nil || msg.count <= 0 || m.status.mode == "running" {
		m.exitFlowPhase = exitFlowPhaseNone
		return m.doExit()
	}
	m.exitFlowPhase = exitFlowPhaseCleanup
	m.openWorktreeCleanupModal(m.width, m.height, msg.count)
	return m, nil
}
