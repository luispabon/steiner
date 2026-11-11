package tui

import (
	"fmt"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

// applyApprovalEvent opens the approval prompt for a requested tool call and
// clears it when the request is accepted or denied. With an approval
// coordinator only the FIFO head prompts, and a decision for any other request
// leaves a still-pending head in place. Scoped child requests share the same
// coordinator FIFO but use a scope-qualified identity, so a call ID reused by
// another agent never matches the wrong request.
func (m *Model) applyApprovalEvent(event output.Event, payload output.ApprovalEvent) {
	identity := interactive.ApprovalIdentity(event.Scope.AgentID, payload.CallID)
	switch event.Type {
	case output.EventTypeApprovalRequested:
		// A scoped request without a call ID has no identity the coordinator
		// could ever match, so it must never become promptable.
		if event.Scope.AgentID != "" && identity == "" {
			return
		}
		// Content retains every request. The tray only prompts the FIFO head,
		// so cached grants that never registered stay silent.
		if !m.approval.active && m.approvalRequestPromptable(identity) {
			m.approval = approvalState{
				active:         true,
				identity:       identity,
				tool:           payload.Tool,
				mode:           payload.Mode,
				preview:        payload.Preview,
				kind:           payload.Kind,
				server:         payload.Server,
				mcpToolName:    payload.ToolName,
				selectedAction: 0,
			}
			m.status.mode = "approval"
			m.activity = m.activity.static("approval required", approvalDetail(payload))
			m.input.Reset()
			m.input.Blur()
			m.notifyBlocking(fmt.Sprintf("Tool approval required: %s", payload.Tool))
		}
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
		if m.decisionPreservesActiveApproval(identity) {
			return
		}
		m.approval = approvalState{}
		if !m.promoteNextApproval(identity) {
			m.status.mode = "running"
			m.input.Focus()
		}
		m.activity = m.activity.static(approvalResultLabel(event.Type), approvalDetail(payload))
	}
}

// approvalHead returns the coordinator's FIFO head identity and reports whether
// the controller coordinates approvals at all.
func (m *Model) approvalHead() (head string, coordinated bool) {
	coordinator, ok := m.controller.(interface{ ApprovalHeadIdentity() string })
	if !ok {
		return "", false
	}
	return coordinator.ApprovalHeadIdentity(), true
}

// approvalRequestPromptable reports whether a requested approval should open the
// tray and notify. Without a coordinator the event stream drives the tray; with
// one, only a non-empty identity matching the FIFO head prompts, because cached
// grants emit request events without registering.
func (m *Model) approvalRequestPromptable(identity string) bool {
	head, coordinated := m.approvalHead()
	if !coordinated {
		return true
	}
	return identity != "" && identity == head
}

// decisionPreservesActiveApproval reports whether a decision for identity
// resolves a different request while the coordinator still heads the active
// approval, so the active tray and its selection must stay in place.
func (m *Model) decisionPreservesActiveApproval(identity string) bool {
	if !m.approval.active || m.approval.identity == "" || m.approval.identity == identity {
		return false
	}
	head, coordinated := m.approvalHead()
	return coordinated && head == m.approval.identity
}

//nolint:gocyclo // approval segment variants stay centralized
func (m *Model) promoteNextApproval(resolvedIdentities ...string) bool {
	identity, _ := m.approvalHead()
	if identity == "" && len(resolvedIdentities) > 0 {
		identity = resolvedIdentities[0]
	}
	for i := range m.content.segments {
		seg := &m.content.segments[i]
		if seg.kind == segmentToolCall && seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved && (identity == "" || seg.toolData.approvalIdentity == identity) {
			tc := seg.toolData
			m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
			m.status.mode = "approval"
			m.input.Blur()
			return true
		}
		if seg.kind == segmentToolCallGroup && seg.toolGroupData != nil {
			for _, tc := range seg.toolGroupData.entries {
				if tc != nil && tc.approvalPending && !tc.approvalResolved && (identity == "" || tc.approvalIdentity == identity) {
					m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
					m.status.mode = "approval"
					m.input.Blur()
					return true
				}
			}
		}
		if seg.kind == segmentApprovalPill && seg.approvalData != nil && !seg.approvalData.resolved && (identity == "" || seg.approvalData.identity == identity) {
			ad := seg.approvalData
			m.approval = approvalState{active: true, identity: ad.identity, tool: ad.tool, mode: ad.mode, preview: ad.preview, kind: ad.kind, server: ad.server, mcpToolName: ad.mcpToolName}
			m.status.mode = "approval"
			m.input.Blur()
			return true
		}
	}
	return false
}
