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

// findApprovalControl returns the tool-call control carrying identity, whether
// it renders as a standalone call or as an entry in a tool-call group.
func findApprovalControl(buffer *contentBuffer, identity string) *toolCallSegment {
	for i := range buffer.segments {
		segment := &buffer.segments[i]
		switch segment.kind {
		case segmentToolCall:
			if segment.toolData != nil && segment.toolData.approvalIdentity == identity {
				return segment.toolData
			}
		case segmentToolCallGroup:
			if segment.toolGroupData == nil {
				continue
			}
			for _, entry := range segment.toolGroupData.entries {
				if entry != nil && entry.approvalIdentity == identity {
					return entry
				}
			}
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

	buffer.clearApprovalStateExcept(nil)

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

// An async child can still be waiting on approval when the top-level run
// finishes. The run-finished reset must preserve that head, its content and its
// tray instead of orphaning the child.
func TestApprovalAsyncChildSurvivesRunFinishedReset(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	childID, childCh := coordinator.Begin("write", "", "path", "child-1")
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", childID, `{"path":"child.txt"}`)})
	if !m.approval.active || m.approval.identity != childID {
		t.Fatalf("tray identity = %q, want %q", m.approval.identity, childID)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "complete", "", "", nil)})

	if !m.approval.active {
		t.Fatal("run-finished reset orphaned the pending async child approval")
	}
	if got, want := m.approval.identity, childID; got != want {
		t.Fatalf("tray identity after reset = %q, want %q", got, want)
	}
	if got, want := m.status.mode, "approval"; got != want {
		t.Fatalf("status mode after reset = %q, want %q", got, want)
	}
	pill := findApprovalPill(t, &m.content, childID)
	if pill == nil || pill.cleared {
		t.Fatalf("child pill = %#v, want preserved and rendered", pill)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != childID {
		t.Fatalf("submissions = %+v, want the preserved child %q", submissions, childID)
	}
	coordinator.Finish(childCh)
}

// A stale cancellation for a request that finished before the reset must not
// clear or replace the preserved async child head.
func TestApprovalStaleCancellationAfterResetKeepsChildHead(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	parentID, parentCh := coordinator.Begin("read", "", "path", "")
	childID, childCh := coordinator.Begin("write", "", "path", "child-1")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-parent", RequestID: parentID, Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", childID, `{"path":"child.txt"}`)})
	if !m.approval.active || m.approval.identity != parentID {
		t.Fatalf("tray identity = %q, want parent head %q", m.approval.identity, parentID)
	}

	// The parent request finishes; the still-running child becomes the head.
	coordinator.Finish(parentCh)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "complete", "", "", nil)})
	if !m.approval.active || m.approval.identity != childID {
		t.Fatalf("tray identity after reset = %q, want child %q", m.approval.identity, childID)
	}

	// A stale denial for the finished parent request arrives late.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalDeniedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-parent", RequestID: parentID, Mode: "prompt", Preview: `{"path":"parent.txt"}`, Kind: "path"})})

	if !m.approval.active {
		t.Fatal("stale cancellation cleared the preserved child tray")
	}
	if got, want := m.approval.identity, childID; got != want {
		t.Fatalf("tray identity after stale cancellation = %q, want %q", got, want)
	}
	if pill := findApprovalPill(t, &m.content, childID); pill == nil || pill.cleared || pill.resolved {
		t.Fatalf("child pill = %#v, want still pending", pill)
	}

	coordinator.Finish(childCh)
}

// terminalResetCase names an event that resets terminal state and clears the
// tray apart from requests the coordinator still has registered.
type terminalResetCase struct {
	name  string
	event output.Event
}

func terminalResetCases() []terminalResetCase {
	return []terminalResetCase{
		{name: "run finished", event: output.NewRunFinishedEvent(1, "complete", "", "", nil)},
		{name: "stop reason", event: output.NewStopReasonEvent(1, "cancelled", nil)},
	}
}

// A review reproduction of the terminal reset bug: a registered head pill and a
// registered tail tool call. The reset must keep both controls live, leave the
// queue depth on the head, and promote the tail with its own preview once the
// head is decided.
func TestApprovalTerminalResetPreservesRegisteredHeadPillAndToolCallTail(t *testing.T) {
	t.Parallel()
	for _, tc := range terminalResetCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coordinator := &interactive.ApprovalCoordinator{}
			ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
			m := newModel(Config{Controller: ctrl}, nil)

			m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-tail", nil)})
			headID, headCh := coordinator.Begin("write", "", "path", "child-1")
			tailID, tailCh := coordinator.Begin("write", "", "path", "")
			m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", headID, `{"path":"head.txt"}`)})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-tail", RequestID: tailID, Mode: "prompt", Preview: `{"path":"tail.txt"}`, Kind: "path"})})
			if !m.approval.active || m.approval.identity != headID {
				t.Fatalf("tray identity = %q, want head %q", m.approval.identity, headID)
			}

			m = updateModel(t, m, runtimeEventMsg{Event: tc.event})

			if !m.approval.active || m.approval.identity != headID {
				t.Fatalf("tray identity after reset = %q, want head %q", m.approval.identity, headID)
			}
			if got, want := m.approval.preview, `{"path":"head.txt"}`; got != want {
				t.Fatalf("tray preview after reset = %q, want %q", got, want)
			}
			if got, want := m.status.mode, "approval"; got != want {
				t.Fatalf("status mode after reset = %q, want %q", got, want)
			}
			headPill := findApprovalPill(t, &m.content, headID)
			if headPill == nil || headPill.cleared || headPill.resolved {
				t.Fatalf("head pill = %#v, want preserved and pending", headPill)
			}
			if got, want := headPill.queueDepth, 1; got != want {
				t.Fatalf("head queue depth = %d, want %d", got, want)
			}
			tailCall := findToolCallSegment(&m.content, "call-tail")
			if tailCall == nil || !tailCall.approvalPending || tailCall.approvalResolved || tailCall.approvalIdentity != tailID {
				t.Fatalf("tail tool call = %#v, want pending request %q", tailCall, tailID)
			}
			if got, want := tailCall.approvalQueueDepth, 0; got != want {
				t.Fatalf("tail queue depth = %d, want %d", got, want)
			}

			updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			submissions := ctrl.submitApprovals()
			if len(submissions) != 1 || submissions[0].Identity != headID {
				t.Fatalf("submissions = %+v, want the head %q", submissions, headID)
			}

			coordinator.Submit(interactive.SubmitApproval{Identity: headID, Tool: "write", Decision: "allow_once"})
			coordinator.Finish(headCh)
			m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", true, headID, `{"path":"head.txt"}`)})

			if !m.approval.active || m.approval.identity != tailID {
				t.Fatalf("tray identity after head decision = %q, want tail %q", m.approval.identity, tailID)
			}
			if got, want := m.approval.preview, `{"path":"tail.txt"}`; got != want {
				t.Fatalf("tail tray preview = %q, want %q", got, want)
			}
			updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			submissions = ctrl.submitApprovals()
			if len(submissions) != 2 || submissions[1].Identity != tailID {
				t.Fatalf("submissions = %+v, want the tail %q second", submissions, tailID)
			}

			coordinator.Submit(interactive.SubmitApproval{Identity: tailID, Tool: "write", Decision: "allow_once"})
			coordinator.Finish(tailCh)
		})
	}
}

// A registered pill behind a registered embedded approval must stay visible and
// then prompt when it becomes the FIFO head. Previously the reset cleared it, so
// promotion showed nothing and the selection could not match the request.
func TestApprovalTerminalResetPromotesPreservedPillTail(t *testing.T) {
	t.Parallel()
	for _, tc := range terminalResetCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coordinator := &interactive.ApprovalCoordinator{}
			ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
			m := newModel(Config{Controller: ctrl}, nil)

			m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-head", nil)})
			headID, headCh := coordinator.Begin("write", "", "path", "")
			tailID, tailCh := coordinator.Begin("write", "", "path", "child-2")
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-head", RequestID: headID, Mode: "prompt", Preview: `{"path":"head.txt"}`, Kind: "path"})})
			m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-2", tailID, `{"path":"tail-pill.txt"}`)})
			if !m.approval.active || m.approval.identity != headID {
				t.Fatalf("tray identity = %q, want head %q", m.approval.identity, headID)
			}

			m = updateModel(t, m, runtimeEventMsg{Event: tc.event})

			headCall := findToolCallSegment(&m.content, "call-head")
			if headCall == nil || !headCall.approvalPending || headCall.approvalResolved {
				t.Fatalf("head tool call = %#v, want preserved and pending", headCall)
			}
			if got, want := headCall.approvalQueueDepth, 1; got != want {
				t.Fatalf("head queue depth = %d, want %d", got, want)
			}
			tailPill := findApprovalPill(t, &m.content, tailID)
			if tailPill == nil || tailPill.cleared || tailPill.resolved {
				t.Fatalf("tail pill = %#v, want preserved and pending", tailPill)
			}
			if got, want := tailPill.queueDepth, 0; got != want {
				t.Fatalf("tail queue depth = %d, want %d", got, want)
			}

			coordinator.Submit(interactive.SubmitApproval{Identity: headID, Tool: "write", Decision: "allow_once"})
			coordinator.Finish(headCh)
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-head", RequestID: headID, Mode: "prompt", Preview: `{"path":"head.txt"}`, Message: "ok", Kind: "path"})})

			if !m.approval.active || m.approval.identity != tailID {
				t.Fatalf("tray identity after head decision = %q, want tail %q", m.approval.identity, tailID)
			}
			if got, want := m.approval.preview, `{"path":"tail-pill.txt"}`; got != want {
				t.Fatalf("tail tray preview = %q, want %q", got, want)
			}
			tailPill = findApprovalPill(t, &m.content, tailID)
			if tailPill == nil || tailPill.cleared {
				t.Fatalf("promoted tail pill = %#v, want rendered", tailPill)
			}
			if got, want := tailPill.selectedAction, m.approval.selectedAction; got != want {
				t.Fatalf("pill selection = %d, want the tray selection %d", got, want)
			}

			updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			submissions := ctrl.submitApprovals()
			if len(submissions) != 1 || submissions[0].Identity != tailID {
				t.Fatalf("submissions = %+v, want the tail %q", submissions, tailID)
			}

			coordinator.Submit(interactive.SubmitApproval{Identity: tailID, Tool: "write", Decision: "allow_once"})
			coordinator.Finish(tailCh)
		})
	}
}

// A control whose request is no longer registered is stale: the terminal reset
// must clear it, keep it out of the queue depth, and never leave a tray open for
// it. Without a coordinator nothing is registered, so the same reset applies.
func TestApprovalTerminalResetClearsUnregisteredControls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		coordinated bool
	}{
		{name: "coordinated", coordinated: true},
		{name: "no coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := &testController{}
			var controller interactive.Controller = base
			var coordinator *interactive.ApprovalCoordinator
			if tc.coordinated {
				coordinator = &interactive.ApprovalCoordinator{}
				controller = &approvalCoordinatorController{testController: base, coordinator: coordinator}
			}
			m := newModel(Config{Controller: controller}, nil)

			m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-stale", nil)})
			staleID := "approval-stale"
			if coordinator != nil {
				// The request unregisters before its event is applied, so the
				// reset sees a control with no live registration.
				var ch chan interactive.SubmitApproval
				staleID, ch = coordinator.Begin("write", "", "path", "")
				coordinator.Finish(ch)
			}
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-stale", RequestID: staleID, Mode: "prompt", Preview: `{"path":"stale.txt"}`, Kind: "path"})})

			m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "complete", "", "", nil)})

			stale := findToolCallSegment(&m.content, "call-stale")
			if stale == nil || stale.approvalPending || stale.approvalResolved {
				t.Fatalf("stale tool call = %#v, want cleared", stale)
			}
			if got, want := stale.approvalQueueDepth, 0; got != want {
				t.Fatalf("stale queue depth = %d, want %d", got, want)
			}
			if m.approval.active {
				t.Fatalf("tray identity = %q, want no prompt for an unregistered request", m.approval.identity)
			}
			if got := base.submitApprovals(); len(got) != 0 {
				t.Fatalf("submissions = %+v, want none", got)
			}
		})
	}
}

// A coordinated queue whose requests were all decided has no unclaimed head,
// which is not the same as having no coordinator. The reset keeps the controls
// because their decision events are still in flight, attaches no queue depth
// suffix, and promotes nothing from transcript order.
func TestApprovalTerminalResetWithNoUnclaimedHeadPromptsNothing(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-a", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-b", nil)})
	aID, aCh := coordinator.Begin("write", "", "path", "")
	bID, bCh := coordinator.Begin("read", "", "path", "")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-a", RequestID: aID, Mode: "prompt", Preview: `{"path":"a.txt"}`, Kind: "path"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-b", RequestID: bID, Mode: "prompt", Preview: `{"path":"b.txt"}`, Kind: "path"})})
	coordinator.Submit(interactive.SubmitApproval{Identity: aID, Tool: "write", Decision: "allow_once"})
	coordinator.Submit(interactive.SubmitApproval{Identity: bID, Tool: "read", Decision: "allow_once"})
	if head := coordinator.HeadIdentity(); head != "" {
		t.Fatalf("coordinator head = %q, want no unclaimed head", head)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "complete", "", "", nil)})

	if m.approval.active {
		t.Fatalf("tray identity = %q, want no prompt without an unclaimed head", m.approval.identity)
	}
	for _, requestID := range []string{aID, bID} {
		call := findApprovalControl(&m.content, requestID)
		if call == nil || !call.approvalPending || call.approvalResolved {
			t.Fatalf("%s control = %#v, want its decision still pending", requestID, call)
		}
		if got, want := call.approvalQueueDepth, 0; got != want {
			t.Fatalf("%s queue depth = %d, want %d", requestID, got, want)
		}
	}
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ctrl.submitApprovals(); len(got) != 0 {
		t.Fatalf("submissions = %+v, want none", got)
	}

	// The in-flight decision events still resolve their own controls.
	coordinator.Finish(aCh)
	coordinator.Finish(bCh)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-a", RequestID: aID, Mode: "prompt", Preview: `{"path":"a.txt"}`, Message: "ok", Kind: "path"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-b", RequestID: bID, Mode: "prompt", Preview: `{"path":"b.txt"}`, Message: "ok", Kind: "path"})})
	for _, requestID := range []string{aID, bID} {
		if call := findApprovalControl(&m.content, requestID); call == nil || !call.approvalResolved {
			t.Fatalf("%s control = %#v, want resolved by its decision event", requestID, call)
		}
	}
	if m.approval.active {
		t.Fatalf("tray identity = %q, want no prompt after the decision events", m.approval.identity)
	}
}

// A root approval cancelled by an interrupt must not survive the stop reset:
// its responder unregisters before its denied event, and the interrupt
// suppresses that event, so the reset is what clears its control. Registered
// async children survive the reset because their contexts are detached from the
// cancelled run, and a still-registered parent request keeps its tail control.
func TestApprovalStopResetClearsCancelledRootAndKeepsRegisteredRequests(t *testing.T) {
	t.Parallel()
	coordinator := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coordinator}
	m := newModel(Config{Controller: ctrl}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-root", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-tail", nil)})
	rootID, rootCh := coordinator.Begin("write", "", "path", "")
	childID, childCh := coordinator.Begin("write", "", "path", "child-1")
	tailID, tailCh := coordinator.Begin("read", "", "path", "")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-root", RequestID: rootID, Mode: "prompt", Preview: `{"path":"root.txt"}`, Kind: "path"})})
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalRequestedEvent("child-1", childID, `{"path":"child.txt"}`)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-tail", RequestID: tailID, Mode: "prompt", Preview: `{"path":"tail.txt"}`, Kind: "path"})})
	if !m.approval.active || m.approval.identity != rootID {
		t.Fatalf("tray identity = %q, want root head %q", m.approval.identity, rootID)
	}

	// The user interrupts. The cancelled root request unregisters first, and the
	// TUI suppresses its denied event until the run finishes.
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.interruptPending {
		t.Fatal("interruptPending = false, want true while the run unwinds")
	}
	coordinator.Finish(rootCh)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalDeniedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-root", RequestID: rootID, Mode: "prompt", Preview: `{"path":"root.txt"}`, Message: "context canceled", Kind: "path"})})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "cancelled", nil)})

	rootCall := findApprovalControl(&m.content, rootID)
	if rootCall == nil || rootCall.approvalPending || rootCall.approvalResolved {
		t.Fatalf("cancelled root segment = %#v, want its control cleared", rootCall)
	}
	if !m.approval.active || m.approval.identity != childID {
		t.Fatalf("tray identity after stop = %q, want the async child %q", m.approval.identity, childID)
	}
	if got, want := m.approval.preview, `{"path":"child.txt"}`; got != want {
		t.Fatalf("tray preview after stop = %q, want %q", got, want)
	}
	if got, want := m.status.mode, "approval"; got != want {
		t.Fatalf("status mode after stop = %q, want %q", got, want)
	}
	childPill := findApprovalPill(t, &m.content, childID)
	if childPill == nil || childPill.cleared || childPill.resolved {
		t.Fatalf("child pill = %#v, want preserved and pending", childPill)
	}
	if got, want := childPill.queueDepth, 1; got != want {
		t.Fatalf("child queue depth = %d, want %d", got, want)
	}
	tailCall := findApprovalControl(&m.content, tailID)
	if tailCall == nil || !tailCall.approvalPending || tailCall.approvalResolved || tailCall.approvalIdentity != tailID {
		t.Fatalf("parent tail segment = %#v, want pending request %q", tailCall, tailID)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 || submissions[0].Identity != childID {
		t.Fatalf("submissions = %+v, want only the async child %q", submissions, childID)
	}

	coordinator.Submit(interactive.SubmitApproval{Identity: childID, Tool: "write", Decision: "allow_once"})
	coordinator.Finish(childCh)
	m = updateModel(t, m, runtimeEventMsg{Event: scopedApprovalDecisionEvent("child-1", true, childID, `{"path":"child.txt"}`)})
	if !m.approval.active || m.approval.identity != tailID {
		t.Fatalf("tray identity after child decision = %q, want the parent tail %q", m.approval.identity, tailID)
	}
	if got, want := m.approval.preview, `{"path":"tail.txt"}`; got != want {
		t.Fatalf("tail tray preview = %q, want %q", got, want)
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions = ctrl.submitApprovals()
	if len(submissions) != 2 || submissions[1].Identity != tailID {
		t.Fatalf("submissions = %+v, want the parent tail %q second", submissions, tailID)
	}

	coordinator.Submit(interactive.SubmitApproval{Identity: tailID, Tool: "read", Decision: "allow_once"})
	coordinator.Finish(tailCh)
}
