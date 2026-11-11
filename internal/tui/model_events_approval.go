package tui

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
)

// applyApprovalEvent opens the approval prompt for a requested tool call and
// clears it when the request is accepted or denied. With an approval
// coordinator only the FIFO head prompts, and a decision for any other request
// leaves a still-pending head in place. The tray identity is the request ID
// carried by the event, so a decision can only resolve the request that
// produced it.
func (m *Model) applyApprovalEvent(event output.Event, payload output.ApprovalEvent) {
	identity := payload.RequestID
	// An event without a request ID cannot be tied to a coordinator
	// registration, so it never prompts, clears the tray, or promotes.
	if identity == "" {
		return
	}
	switch event.Type {
	case output.EventTypeApprovalRequested:
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
			m.syncApprovalSelection(m.approval.selectedAction)
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

// approvalCoordinatorView is the controller surface that keeps the tray and the
// terminal reset aligned with the coordinator's FIFO queue. A controller that
// does not implement it leaves the event stream in charge of the tray.
type approvalCoordinatorView interface {
	ApprovalHeadIdentity() string
	ApprovalPendingIdentities() []string
}

// approvalHead returns the coordinator's FIFO head identity and reports whether
// the controller coordinates approvals at all.
func (m *Model) approvalHead() (head string, coordinated bool) {
	coordinator, ok := m.controller.(approvalCoordinatorView)
	if !ok {
		return "", false
	}
	return coordinator.ApprovalHeadIdentity(), true
}

// approvalSnapshot returns the coordinator's FIFO head and the identities it
// still has registered, reporting false when the controller does not coordinate
// approvals. A terminal reset preserves exactly that registered set, so no live
// request loses its control and no unregistered control survives. The set is
// read before the head: a request that registers concurrently is preserved,
// while an identity left behind by a concurrent Finish is harmless because its
// decision event resolves the control.
func (m *Model) approvalSnapshot() (head string, preserve map[string]struct{}, coordinated bool) {
	coordinator, ok := m.controller.(approvalCoordinatorView)
	if !ok {
		return "", nil, false
	}
	registered := coordinator.ApprovalPendingIdentities()
	preserve = make(map[string]struct{}, len(registered))
	for _, identity := range registered {
		if identity != "" {
			preserve[identity] = struct{}{}
		}
	}
	return coordinator.ApprovalHeadIdentity(), preserve, true
}

// approvalRequestPromptable reports whether a requested approval should open the
// tray and notify. Without a coordinator the event stream drives the tray; with
// one, only a non-empty identity matching the FIFO head prompts, because cached
// grants emit request events without registering.
func (m *Model) approvalRequestPromptable(identity string) bool {
	// An empty request ID can never be matched to a submission, so it never
	// prompts, with or without a coordinator.
	if identity == "" {
		return false
	}
	head, coordinated := m.approvalHead()
	if !coordinated {
		return true
	}
	return identity == head
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
	identity, coordinated := m.approvalHead()
	if identity == "" {
		// A coordinator with no unclaimed request has nothing to promote.
		// Falling back to transcript order could re-prompt a request that was
		// already decided.
		if coordinated {
			return false
		}
		if len(resolvedIdentities) > 0 {
			identity = resolvedIdentities[0]
		}
	}
	for i := range m.content.segments {
		seg := &m.content.segments[i]
		if seg.kind == segmentToolCall && seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved && (identity == "" || seg.toolData.approvalIdentity == identity) {
			tc := seg.toolData
			m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
			m.status.mode = "approval"
			m.input.Blur()
			m.syncApprovalSelection(m.approval.selectedAction)
			return true
		}
		if seg.kind == segmentToolCallGroup && seg.toolGroupData != nil {
			for _, tc := range seg.toolGroupData.entries {
				if tc != nil && tc.approvalPending && !tc.approvalResolved && (identity == "" || tc.approvalIdentity == identity) {
					m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
					m.status.mode = "approval"
					m.input.Blur()
					m.syncApprovalSelection(m.approval.selectedAction)
					return true
				}
			}
		}
		if seg.kind == segmentApprovalPill && seg.approvalData != nil && !seg.approvalData.resolved && !seg.approvalData.cleared && (identity == "" || seg.approvalData.identity == identity) {
			ad := seg.approvalData
			m.approval = approvalState{active: true, identity: ad.identity, tool: ad.tool, mode: ad.mode, preview: ad.preview, kind: ad.kind, server: ad.server, mcpToolName: ad.mcpToolName}
			m.status.mode = "approval"
			m.input.Blur()
			m.syncApprovalSelection(m.approval.selectedAction)
			return true
		}
	}
	return false
}
