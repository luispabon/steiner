package tui

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
)

// applyApprovalEvent opens the approval prompt for a requested tool call and
// clears it when the request is accepted or denied.
func (m *Model) applyApprovalEvent(event output.Event, payload output.ApprovalEvent) {
	switch event.Type {
	case output.EventTypeApprovalRequested:
		// Content retains every request. The tray only prompts the first.
		if !m.approval.active {
			if coordinator, ok := m.controller.(interface{ ApprovalHeadIdentity() string }); ok && coordinator.ApprovalHeadIdentity() != "" && coordinator.ApprovalHeadIdentity() != payload.CallID {
				break
			}
			m.approval = approvalState{
				active:         true,
				identity:       payload.CallID,
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
		m.approval = approvalState{}
		if !m.promoteNextApproval(payload.CallID) {
			m.status.mode = "running"
			m.input.Focus()
		}
		m.activity = m.activity.static(approvalResultLabel(event.Type), approvalDetail(payload))
	}
}

//nolint:gocyclo // approval segment variants stay centralized
func (m *Model) promoteNextApproval(resolvedIdentities ...string) bool {
	identity := ""
	if coordinator, ok := m.controller.(interface{ ApprovalHeadIdentity() string }); ok {
		identity = coordinator.ApprovalHeadIdentity()
	}
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
