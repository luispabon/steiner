package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
)

func (m *Model) selectedApprovalDecision() ApprovalDecision {
	if m.approval.selectedAction < 0 || m.approval.selectedAction >= len(approvalDecisions) {
		return ApprovalDecisionAllowOnce
	}
	return approvalDecisions[m.approval.selectedAction]
}

func (m *Model) moveApprovalSelection(delta int) *Model {
	if !m.approval.active || len(approvalDecisions) == 0 {
		return m
	}
	count := len(approvalDecisions)
	next := (m.approval.selectedAction + delta) % count
	if next < 0 {
		next += count
	}
	m.approval.selectedAction = next
	// Sync to the embedded tool segment so it re-renders with the new selection.
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		seg := &m.content.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved {
				seg.toolData.approvalSelectedAction = next
				seg.renderDirty = true
				m.content.gen++
				return m
			}
		case segmentToolCallGroup:
			if seg.toolGroupData == nil {
				continue
			}
			for j := len(seg.toolGroupData.entries) - 1; j >= 0; j-- {
				entry := seg.toolGroupData.entries[j]
				if entry != nil && entry.approvalPending && !entry.approvalResolved {
					entry.approvalSelectedAction = next
					seg.renderDirty = true
					m.content.gen++
					return m
				}
			}
		}
	}
	return m
}

func (m *Model) executeApprovalDecision(decision ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitApproval{
			Tool:     m.approval.tool,
			Mode:     m.approval.mode,
			Decision: string(decision),
		}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.approval = approvalState{}
	m.status.mode = "running"
	m.activity = m.activity.static("approval submitted", string(decision))
	m.input.Reset()
	m.input.Focus()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}
