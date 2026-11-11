package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func scopedApprovalRequestedEvent(agentID, requestID, preview string) output.Event {
	return output.WithAgentScope(output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn:      1,
		Tool:      "write",
		CallID:    "call-shared",
		RequestID: requestID,
		Mode:      "prompt",
		Preview:   preview,
		Kind:      "path",
	}), agentID)
}

func scopedApprovalDecisionEvent(agentID string, accepted bool, requestID, preview string) output.Event {
	params := output.ApprovalEventParams{
		Turn:      1,
		Tool:      "write",
		CallID:    "call-shared",
		RequestID: requestID,
		Mode:      "prompt",
		Preview:   preview,
		Kind:      "path",
	}
	if accepted {
		params.Message = "approved"
		return output.WithAgentScope(output.NewApprovalAcceptedEvent(params), agentID)
	}
	params.Message = "denied"
	return output.WithAgentScope(output.NewApprovalDeniedEvent(params), agentID)
}

func findApprovalPill(t *testing.T, buffer *contentBuffer, requestID string) *approvalPillData {
	t.Helper()
	for i := range buffer.segments {
		segment := &buffer.segments[i]
		if segment.kind == segmentApprovalPill && segment.approvalData != nil && segment.approvalData.identity == requestID {
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

// A scoped child approval reaches the shared tray and submits the unique
// request ID its coordinator registration uses.
func TestApprovalScopedRequestPromptsWithQualifiedIdentity(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	identity, responseCh := coordinator.Begin("write", "", "path", "child-1")

	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", identity, `{"path":"child.txt"}`)})
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
	if len(submissions) != 1 || submissions[0].Identity != identity {
		t.Fatalf("submissions = %+v, want one submission for %q", submissions, identity)
	}

	coordinator.Submit(interactive.SubmitApproval{Identity: identity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got := <-responseCh; got.Identity != identity {
		t.Fatalf("coordinator delivered %q, want %q", got.Identity, identity)
	}
	coordinator.Finish(responseCh)
}

// Same-scope reuse reproduction: two requests from one agent with one provider
// call ID. Events arrive B then A, the tray must show A (the FIFO head), the
// submitted decision must target A, promotion shows B, a stale cancellation for
// A must not disturb B, and deciding B targets B.
func TestApprovalScopedDuplicateSameScopeIDRoutesByRequestID(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	aID, aCh := coordinator.Begin("write", "", "path", "child-1")
	bID, bCh := coordinator.Begin("write", "", "path", "child-1")
	if aID == bID {
		t.Fatalf("duplicate same-scope requests share identity %q", aID)
	}

	// Events arrive in reverse registration order.
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", bID, `{"path":"b.txt"}`)})
	if m.approval.active {
		t.Fatal("queued request B opened the tray")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", aID, `{"path":"a.txt"}`)})
	if !m.approval.active {
		t.Fatal("head request A did not open the tray")
	}
	if got, want := m.approval.identity, aID; got != want {
		t.Fatalf("tray identity = %q, want head A %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"a.txt"}`; got != want {
		t.Fatalf("tray preview = %q, want A preview %q", got, want)
	}

	// The decision must target the displayed request A, not the queued B.
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != aID {
		t.Fatalf("submissions = %+v, want the displayed A %q", submissions, aID)
	}
	coordinator.Submit(interactive.SubmitApproval{Identity: aID, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got := <-aCh; got.Identity != aID {
		t.Fatalf("A channel received %q, want %q", got.Identity, aID)
	}
	coordinator.Finish(aCh)

	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", true, aID, `{"path":"a.txt"}`)})
	if !m.approval.active {
		t.Fatal("queued B was not promoted")
	}
	if got, want := m.approval.identity, bID; got != want {
		t.Fatalf("promoted identity = %q, want B %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"b.txt"}`; got != want {
		t.Fatalf("promoted preview = %q, want B preview %q", got, want)
	}

	// A stale cancellation for A must resolve nothing and must not reprompt A.
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", false, aID, `{"path":"a.txt"}`)})
	if !m.approval.active {
		t.Fatal("stale A cancellation cleared the tray")
	}
	if got, want := m.approval.identity, bID; got != want {
		t.Fatalf("stale A cancellation changed the tray to %q, want B %q", got, want)
	}
	if pill := findApprovalPill(t, &m.content, bID); pill == nil || pill.resolved {
		t.Fatalf("B pill = %#v, want still pending after stale A cancellation", pill)
	}
	if pill := findApprovalPill(t, &m.content, aID); pill == nil || !pill.resolved {
		t.Fatalf("A pill = %#v, want resolved", pill)
	}

	// Deciding the displayed B targets B.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions = ctrl.submitApprovals()
	if len(submissions) != 2 || submissions[1].Identity != bID {
		t.Fatalf("submissions = %+v, want second submission for B %q", submissions, bID)
	}
	coordinator.Submit(interactive.SubmitApproval{Identity: bID, Tool: "write", Mode: "prompt", Decision: "deny"})
	if got := <-bCh; got.Identity != bID {
		t.Fatalf("B channel received %q, want %q", got.Identity, bID)
	}
	coordinator.Finish(bCh)
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", false, bID, `{"path":"b.txt"}`)})
	if m.approval.active {
		t.Fatal("tray stayed open after the B decision")
	}
}

// Cancelling the head scoped request advances the FIFO to the queued request,
// keeping that request's own preview.
func TestApprovalScopedCancellationPromotesQueuedRequest(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	childID, childCh := coordinator.Begin("write", "", "path", "child-1")
	parentID, parentCh := coordinator.Begin("read", "", "path", "")

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-parent", RequestID: parentID, Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})
	if m.approval.active {
		t.Fatal("queued parent request opened the tray")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", childID, `{"path":"child.txt"}`)})
	if !m.approval.active || m.approval.identity != childID {
		t.Fatalf("tray identity = %q, want %q", m.approval.identity, childID)
	}

	coordinator.Finish(childCh)
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", false, childID, `{"path":"child.txt"}`)})
	if !m.approval.active {
		t.Fatal("cancellation did not promote the queued parent request")
	}
	if got, want := m.approval.identity, parentID; got != want {
		t.Fatalf("promoted identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"parent.txt"}`; got != want {
		t.Fatalf("promoted preview = %q, want %q", got, want)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != parentID {
		t.Fatalf("submissions = %+v, want the promoted parent %q", submissions, parentID)
	}
	coordinator.Finish(parentCh)
}

// Content attribution keys on the request ID: a decision can only resolve the
// request whose ID it carries, and empty IDs produce no control at all.
func TestApprovalScopedContentKeepsRequestsOnTheirOwnPreview(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{collapseState: make(map[int]bool)}
	parentCall := &toolCallSegment{tool: "write", callID: "call-shared"}
	buffer.segments = []contentSegment{{kind: segmentToolCall, toolData: parentCall}}

	parentPreview := `{"path":"parent.txt"}`
	parentID := "approval-parent"
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", RequestID: parentID, Mode: "prompt", Preview: parentPreview, Kind: "path"}))
	if !parentCall.approvalPending || parentCall.approvalIdentity != parentID {
		t.Fatalf("parent approval state = %#v, want pending %q", parentCall, parentID)
	}

	childPreview := `{"path":"child.txt"}`
	childID := "approval-child"
	buffer.appendApprovalRequestedEvent(output.WithAgentScope(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", RequestID: childID, Mode: "prompt", Preview: childPreview, Kind: "path"}), "child-1"))
	if parentCall.approvalPreview != parentPreview || parentCall.approvalIdentity != parentID {
		t.Fatalf("scoped request overwrote the parent approval: %#v", parentCall)
	}
	childPill := findApprovalPill(t, buffer, childID)
	if childPill == nil {
		t.Fatalf("no approval pill with identity %q", childID)
	}
	if childPill.preview != childPreview || childPill.agentID != "child-1" {
		t.Fatalf("child pill attribution = (%q, %q), want (child-1, child preview)", childPill.agentID, childPill.preview)
	}

	// An empty request ID produces no approval control.
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-empty", Mode: "prompt", Kind: "path"}))
	if pill := findApprovalPill(t, buffer, ""); pill != nil {
		t.Fatal("empty request ID created an approval control")
	}

	// A decision for another request resolves nothing here.
	buffer.appendApprovalDecisionEvent(output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", RequestID: "approval-other", Mode: "prompt", Kind: "path"}))
	if childPill.resolved || parentCall.approvalResolved {
		t.Fatal("another request's decision resolved this request")
	}

	buffer.appendApprovalDecisionEvent(output.WithAgentScope(output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", RequestID: childID, Mode: "prompt", Kind: "path"}), "child-1"))
	if !childPill.resolved || !childPill.accepted {
		t.Fatalf("child pill = %#v, want resolved and accepted", childPill)
	}
	if parentCall.approvalResolved {
		t.Fatal("scoped decision resolved the parent approval")
	}

	buffer.appendApprovalDecisionEvent(output.NewApprovalDeniedEvent(output.ApprovalEventParams{Tool: "write", CallID: "call-shared", RequestID: parentID, Mode: "prompt", Kind: "path"}))
	if !parentCall.approvalResolved || parentCall.approvalAccepted {
		t.Fatalf("parent approval state = %#v, want resolved and denied", parentCall)
	}
}

// The tray selection must stay with the displayed request and be visible on it.
func TestApprovalScopedSelectionStaysWithDisplayedRequest(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-parent", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-parent", RequestID: "approval-parent", Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})

	pillID := "approval-pill"
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", pillID, `{"path":"child.txt"}`)})
	m.approval = approvalState{active: true, identity: pillID, tool: "write", mode: "prompt", preview: `{"path":"child.txt"}`, kind: "path"}
	pill := findApprovalPill(t, &m.content, pillID)
	if pill == nil {
		t.Fatalf("no approval pill with identity %q", pillID)
	}
	before := m.content.renderApprovalPill(pill, 100)

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Fatalf("selectedAction = %d, want %d", got, want)
	}
	if got, want := pill.selectedAction, 1; got != want {
		t.Fatalf("pill selectedAction = %d, want %d", got, want)
	}
	parentCall := findToolCallSegment(&m.content, "call-parent")
	if parentCall == nil {
		t.Fatal("parent tool call segment missing")
	}
	if got, want := parentCall.approvalSelectedAction, 0; got != want {
		t.Fatalf("parent approvalSelectedAction = %d, want %d", got, want)
	}
	after := m.content.renderApprovalPill(pill, 100)
	if after == before {
		t.Fatal("pill render did not change with the tray selection")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := pill.selectedAction, 2; got != want {
		t.Fatalf("pill selectedAction after second move = %d, want %d", got, want)
	}
	if got := m.content.renderApprovalPill(pill, 100); got == after {
		t.Fatal("pill render did not change again after the second move")
	}
}

// A cached grant for one child emits requested and accepted events without
// registering, so it must not displace the promptable FIFO head, reset the
// composer, or notify, and its events still resolve their own control.
func TestApprovalScopedCachedGrantLeavesTrayAndQueueAlone(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	fn := &fakeNotifier{avail: true, notified: make(chan struct{}, 4)}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Controller: ctrl, Notifier: fn}, nil)

	headID, headCh := coordinator.Begin("write", "", "path", "child-1")
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", headID, `{"path":"head.txt"}`)})
	if !m.approval.active || m.approval.identity != headID {
		t.Fatalf("tray identity = %q, want %q", m.approval.identity, headID)
	}
	_ = fn.waitForCall(t)
	fn.reset()

	m.input.SetValue("draft instruction")
	m.input.Focus()
	cachedID := "approval-cached"
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-2", cachedID, `{"path":"cached.txt"}`)})
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-2", true, cachedID, `{"path":"cached.txt"}`)})

	if !m.approval.active {
		t.Fatal("cached grant closed the active tray")
	}
	if got, want := m.approval.identity, headID; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.preview, `{"path":"head.txt"}`; got != want {
		t.Fatalf("approval.preview = %q, want %q", got, want)
	}
	if got, want := m.input.Value(), "draft instruction"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	if got, want := coordinator.HeadIdentity(), headID; got != want {
		t.Fatalf("coordinator head = %q, want %q", got, want)
	}
	if pill := findApprovalPill(t, &m.content, cachedID); pill == nil || !pill.resolved || !pill.accepted {
		t.Fatalf("cached grant pill = %#v, want resolved and accepted", pill)
	}
	if pill := findApprovalPill(t, &m.content, headID); pill == nil || pill.resolved {
		t.Fatalf("head pill = %#v, want still pending", pill)
	}
	fn.expectNoCall(t)

	coordinator.Finish(headCh)
}

// Queue depth must follow the coordinator FIFO head, not transcript order. A
// child pill appended before a parent embedded approval must not claim the
// "+N waiting" suffix when the parent registered first.
func TestApprovalQueueDepthFollowsCoordinatorHeadAcrossMixedSegments(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-parent", nil)})
	parentID, parentCh := coordinator.Begin("write", "", "path", "")
	childID, childCh := coordinator.Begin("write", "", "path", "child-1")

	// Transcript order: child pill first, parent embedded approval second.
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", childID, `{"path":"child.txt"}`)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-parent", RequestID: parentID, Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})

	parentCall := findToolCallSegment(&m.content, "call-parent")
	if parentCall == nil || !parentCall.approvalPending {
		t.Fatalf("parent approval state = %#v, want pending", parentCall)
	}
	childPill := findApprovalPill(t, &m.content, childID)
	if childPill == nil {
		t.Fatalf("no child pill with identity %q", childID)
	}
	if got, want := parentCall.approvalQueueDepth, 1; got != want {
		t.Fatalf("parent queue depth = %d, want %d (coordinator head)", got, want)
	}
	if got, want := childPill.queueDepth, 0; got != want {
		t.Fatalf("child pill queue depth = %d, want %d", got, want)
	}
	if !m.approval.active || m.approval.identity != parentID {
		t.Fatalf("tray identity = %q, want head %q", m.approval.identity, parentID)
	}

	coordinator.Finish(parentCh)
	coordinator.Finish(childCh)
}

// Terminal cleanup drops unresolved pills but keeps the outcome of resolved
// ones, matching how stale tool approvals are cleared.
func TestApprovalClearStateDropsPendingPills(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{collapseState: make(map[int]bool), styles: testStyles("#e8814b")}
	pendingID := "approval-pending"
	resolvedID := "approval-resolved"
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", RequestID: pendingID, Mode: "prompt", Kind: "path"}))
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", RequestID: resolvedID, Mode: "prompt", Kind: "path"}))
	buffer.appendApprovalDecisionEvent(output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Tool: "write", RequestID: resolvedID, Message: "ok", Kind: "path"}))

	pending := findApprovalPill(t, buffer, pendingID)
	resolved := findApprovalPill(t, buffer, resolvedID)
	if pending == nil || resolved == nil {
		t.Fatalf("pills = %#v/%#v, want both present", pending, resolved)
	}

	buffer.clearApprovalState()

	if !pending.cleared {
		t.Fatal("pending pill not cleared by terminal cleanup")
	}
	if resolved.cleared || !resolved.resolved || !resolved.accepted {
		t.Fatalf("resolved pill = %#v, want its outcome preserved", resolved)
	}
	if got := buffer.renderApprovalPillSegment(contentSegment{kind: segmentApprovalPill, approvalData: pending}, 100); got != "" {
		t.Fatalf("cleared pill render = %q, want empty", got)
	}
}

// Promotion resets the displayed selection to the tray selection so a stale
// highlight on the promoted segment cannot disagree with the submitted action.
func TestApprovalPromotionSyncsDisplayedSelection(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	pillID := "approval-next"
	m.content.segments = append(m.content.segments, contentSegment{
		kind: segmentApprovalPill,
		approvalData: &approvalPillData{
			tool:           "write",
			identity:       pillID,
			selectedAction: 2,
		},
	})

	if !m.promoteNextApproval(pillID) {
		t.Fatal("promotion failed")
	}
	pill := m.content.segments[0].approvalData
	if got, want := m.approval.selectedAction, 0; got != want {
		t.Fatalf("tray selectedAction = %d, want %d", got, want)
	}
	if got, want := pill.selectedAction, 0; got != want {
		t.Fatalf("promoted pill selectedAction = %d, want %d", got, want)
	}
}
