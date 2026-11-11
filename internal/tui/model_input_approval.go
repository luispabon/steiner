package tui

import (
	"context"

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
	m.syncApprovalSelection(next)
	return m
}

// syncApprovalSelection syncs the highlighted action to the segment that
// renders the tray's active request, so the visible control re-renders with the
// new selection. Both embedded tool-call approvals and standalone pills are
// updated, and only the request matching the tray identity is touched.
func (m *Model) syncApprovalSelection(next int) {
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		seg := &m.content.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if m.syncToolCallSelection(seg, next) {
				return
			}
		case segmentToolCallGroup:
			if m.syncToolGroupSelection(seg, next) {
				return
			}
		case segmentApprovalPill:
			if seg.approvalData != nil && !seg.approvalData.resolved && m.approvalTargets(seg.approvalData.identity) {
				seg.approvalData.selectedAction = next
				seg.renderDirty = true
				m.content.gen++
				return
			}
		}
	}
}

func (m *Model) syncToolCallSelection(seg *contentSegment, next int) bool {
	if seg.toolData == nil || !seg.toolData.approvalPending || seg.toolData.approvalResolved || !m.approvalTargets(seg.toolData.approvalIdentity) {
		return false
	}
	seg.toolData.approvalSelectedAction = next
	seg.renderDirty = true
	m.content.gen++
	return true
}

func (m *Model) syncToolGroupSelection(seg *contentSegment, next int) bool {
	if seg.toolGroupData == nil {
		return false
	}
	for j := len(seg.toolGroupData.entries) - 1; j >= 0; j-- {
		entry := seg.toolGroupData.entries[j]
		if entry == nil || !entry.approvalPending || entry.approvalResolved || !m.approvalTargets(entry.approvalIdentity) {
			continue
		}
		entry.approvalSelectedAction = next
		seg.renderDirty = true
		m.content.gen++
		return true
	}
	return false
}

// approvalTargets reports whether an embedded approval is the request shown in
// the tray. The tray identity is scope-qualified, so a provider call ID shared
// by another agent never moves the selection of a different request.
func (m *Model) approvalTargets(identity string) bool {
	return m.approval.identity == "" || m.approval.identity == identity
}

func (m *Model) executeApprovalDecision(decision ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitApproval{
			Identity: m.approval.identity,
			Tool:     m.approval.tool,
			Mode:     m.approval.mode,
			Decision: string(decision),
		}); err != nil {
			m.appendError(err)
		}
	}
	m.approval = approvalState{}
	m.status.mode = "running"
	m.activity = m.activity.static("approval submitted", string(decision))
	m.input.Reset()
	m.input.Focus()
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}
