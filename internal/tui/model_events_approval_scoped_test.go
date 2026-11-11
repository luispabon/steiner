package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func scopedApprovalRequestedEvent(agentID, callID, preview string) output.Event {
	return output.WithAgentScope(output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn:    1,
		Tool:    "write",
		CallID:  callID,
		Mode:    "prompt",
		Preview: preview,
		Kind:    "path",
	}), agentID)
}

func scopedApprovalDecisionEvent(agentID string, accepted bool, callID, preview string) output.Event {
	params := output.ApprovalEventParams{
		Turn:    1,
		Tool:    "write",
		CallID:  callID,
		Mode:    "prompt",
		Preview: preview,
		Kind:    "path",
	}
	if accepted {
		params.Message = "approved"
		return output.WithAgentScope(output.NewApprovalAcceptedEvent(params), agentID)
	}
	params.Message = "denied"
	return output.WithAgentScope(output.NewApprovalDeniedEvent(params), agentID)
}

func findApprovalPill(t *testing.T, buffer *contentBuffer, identity string) *approvalPillData {
	t.Helper()
	for i := range buffer.segments {
		segment := &buffer.segments[i]
		if segment.kind == segmentApprovalPill && segment.approvalData != nil && segment.approvalData.identity == identity {
			return segment.approvalData
		}
	}
	return nil
}

func findToolCallSegment(buffer *contentBuffer, callID string) *toolCallSegment {
	for i := range buffer.segments {
		segment := &buffer.segments[i]
		if segment.kind == segmentToolCall && segment.toolData != nil && segment.toolData.callID == callID {
			return segment.toolData
		}
	}
	return nil
}

// A scoped child approval reaches the shared tray and submits the qualified
// identity its coordinator registration uses.
func TestApprovalScopedRequestPromptsWithQualifiedIdentity(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	identity := interactive.ApprovalIdentity("child-1", "call-1")
	responseCh := coordinator.Begin(identity, "write", "", "path", "child-1")

	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", "call-1", `{"path":"child.txt"}`)})
	if !m.approval.active {
		t.Fatal("scoped approval request did not open the tray")
	}
	if got, want := m.approval.identity, identity; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"child.txt"}`; got != want {
		t.Fatalf("approval.preview = %q, want %q", got, want)
	}
	if got, want := m.status.mode, "approval"; got != want {
		t.Fatalf("status mode = %q, want %q", got, want)
	}
	pill := findApprovalPill(t, &m.content, identity)
	if pill == nil {
		t.Fatalf("no approval pill with identity %q", identity)
	}
	if pill.agentID != "child-1" || pill.preview != `{"path":"child.txt"}` {
		t.Fatalf("pill attribution = (%q, %q), want (child-1, child preview)", pill.agentID, pill.preview)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Identity, identity; got != want {
		t.Fatalf("submission.Identity = %q, want %q", got, want)
	}

	coordinator.Submit(interactive.SubmitApproval{Identity: identity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got, want := <-responseCh, identity; got.Identity != want {
		t.Fatalf("coordinator delivered %q, want %q", got.Identity, want)
	}
}

// Two children sharing one provider call ID must keep independent previews and
// promote in FIFO order without cross-attributing the decision.
func TestApprovalScopedSharedCallIDKeepsAgentAttribution(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	firstIdentity := interactive.ApprovalIdentity("child-1", "call-1")
	secondIdentity := interactive.ApprovalIdentity("child-2", "call-1")
	if firstIdentity == secondIdentity {
		t.Fatalf("scoped identities are equal: %q", firstIdentity)
	}
	firstCh := coordinator.Begin(firstIdentity, "write", "", "path", "child-1")
	secondCh := coordinator.Begin(secondIdentity, "write", "", "path", "child-2")

	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-2", "call-1", `{"path":"child-2.txt"}`)})
	if m.approval.active {
		t.Fatal("queued child-2 request opened the tray")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", "call-1", `{"path":"child-1.txt"}`)})
	if !m.approval.active {
		t.Fatal("child-1 request did not open the tray")
	}
	if got, want := m.approval.identity, firstIdentity; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"child-1.txt"}`; got != want {
		t.Fatalf("approval.preview = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != firstIdentity {
		t.Fatalf("submissions = %+v, want one child-1 submission", submissions)
	}
	coordinator.Submit(interactive.SubmitApproval{Identity: firstIdentity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got := <-firstCh; got.Identity != firstIdentity {
		t.Fatalf("child-1 channel received identity %q, want %q", got.Identity, firstIdentity)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", true, "call-1", `{"path":"child-1.txt"}`)})
	if !m.approval.active {
		t.Fatal("queued child-2 request was not promoted")
	}
	if got, want := m.approval.identity, secondIdentity; got != want {
		t.Fatalf("promoted approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"child-2.txt"}`; got != want {
		t.Fatalf("promoted approval.preview = %q, want %q", got, want)
	}
	if pill := findApprovalPill(t, &m.content, firstIdentity); pill == nil || !pill.resolved || !pill.accepted {
		t.Fatalf("child-1 pill = %#v, want resolved and accepted", pill)
	}
	if pill := findApprovalPill(t, &m.content, secondIdentity); pill == nil || pill.resolved {
		t.Fatalf("child-2 pill = %#v, want still pending", pill)
	}

	coordinator.Finish(secondCh)
}

// Cancelling the head scoped request advances the FIFO to the queued request,
// keeping that request's own preview.
func TestApprovalScopedCancellationPromotesQueuedRequest(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	childIdentity := interactive.ApprovalIdentity("child-1", "call-child")
	childCh := coordinator.Begin(childIdentity, "write", "", "path", "child-1")
	parentCh := coordinator.Begin("call-parent", "read", "", "path", "")

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-parent", Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})
	if m.approval.active {
		t.Fatal("queued parent request opened the tray")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", "call-child", `{"path":"child.txt"}`)})
	if !m.approval.active || m.approval.identity != childIdentity {
		t.Fatalf("tray identity = %q, want %q", m.approval.identity, childIdentity)
	}

	// Cancellation unregisters the child request before its denied event is emitted.
	coordinator.Finish(childCh)
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", false, "call-child", `{"path":"child.txt"}`)})
	if !m.approval.active {
		t.Fatal("cancellation did not promote the queued parent request")
	}
	if got, want := m.approval.identity, "call-parent"; got != want {
		t.Fatalf("promoted approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"parent.txt"}`; got != want {
		t.Fatalf("promoted approval.preview = %q, want %q", got, want)
	}
	if got, want := m.status.mode, "approval"; got != want {
		t.Fatalf("status mode = %q, want %q", got, want)
	}
	if pill := findApprovalPill(t, &m.content, childIdentity); pill == nil || !pill.resolved || pill.accepted {
		t.Fatalf("cancelled child pill = %#v, want resolved and denied", pill)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != "call-parent" {
		t.Fatalf("submissions = %+v, want the promoted parent submission", submissions)
	}
	coordinator.Finish(parentCh)
}

// Content attribution keeps a scoped request on its own pill: a shared call ID
// never embeds into the parent tool call or resolves another agent's request.
func TestApprovalScopedContentKeepsRequestsOnTheirOwnPreview(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{collapseState: make(map[int]bool)}
	parentCall := &toolCallSegment{tool: "write", callID: "call-shared"}
	buffer.segments = []contentSegment{{kind: segmentToolCall, toolData: parentCall}}

	parentPreview := `{"path":"parent.txt"}`
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", Mode: "prompt", Preview: parentPreview, Kind: "path"}))
	if !parentCall.approvalPending || parentCall.approvalIdentity != "call-shared" {
		t.Fatalf("parent approval state = %#v, want pending call-shared", parentCall)
	}

	childPreview := `{"path":"child.txt"}`
	buffer.appendApprovalRequestedEvent(output.WithAgentScope(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", Mode: "prompt", Preview: childPreview, Kind: "path"}), "child-1"))
	if parentCall.approvalPreview != parentPreview || parentCall.approvalIdentity != "call-shared" {
		t.Fatalf("scoped request overwrote the parent approval: %#v", parentCall)
	}
	childIdentity := interactive.ApprovalIdentity("child-1", "call-shared")
	childPill := findApprovalPill(t, buffer, childIdentity)
	if childPill == nil {
		t.Fatalf("no approval pill with identity %q", childIdentity)
	}
	if childPill.preview != childPreview || childPill.agentID != "child-1" {
		t.Fatalf("child pill attribution = (%q, %q), want (child-1, child preview)", childPill.agentID, childPill.preview)
	}

	// Another agent's decision for the same call ID resolves nothing here.
	buffer.appendApprovalDecisionEvent(output.WithAgentScope(output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", Mode: "prompt", Kind: "path"}), "child-2"))
	if childPill.resolved || parentCall.approvalResolved {
		t.Fatal("child-2 decision resolved another request with the same call ID")
	}

	// The matching scoped decision resolves only the scoped request.
	buffer.appendApprovalDecisionEvent(output.WithAgentScope(output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", Mode: "prompt", Kind: "path"}), "child-1"))
	if !childPill.resolved || !childPill.accepted {
		t.Fatalf("child pill = %#v, want resolved and accepted", childPill)
	}
	if parentCall.approvalResolved {
		t.Fatal("scoped decision resolved the parent approval")
	}

	buffer.appendApprovalDecisionEvent(output.NewApprovalDeniedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", Mode: "prompt", Kind: "path"}))
	if !parentCall.approvalResolved || parentCall.approvalAccepted {
		t.Fatalf("parent approval state = %#v, want resolved and denied", parentCall)
	}
}

// The tray selection must stay with the displayed request: a scoped approval
// never moves the selection of another pending tool call segment.
func TestApprovalScopedSelectionStaysWithDisplayedRequest(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-parent", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-parent", Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})

	childIdentity := interactive.ApprovalIdentity("child-1", "call-1")
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", "call-1", `{"path":"child.txt"}`)})
	m.approval = approvalState{active: true, identity: childIdentity, tool: "write", mode: "prompt", preview: `{"path":"child.txt"}`, kind: "path"}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Fatalf("selectedAction = %d, want %d", got, want)
	}
	parentCall := findToolCallSegment(&m.content, "call-parent")
	if parentCall == nil {
		t.Fatal("parent tool call segment missing")
	}
	if got, want := parentCall.approvalSelectedAction, 0; got != want {
		t.Fatalf("parent approvalSelectedAction = %d, want %d", got, want)
	}
}
