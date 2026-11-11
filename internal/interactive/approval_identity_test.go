package interactive

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestApprovalIdentityQualifiesAgentScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		agentID string
		callID  string
		want    string
	}{
		{name: "parent is encoded too", callID: "call-1", want: "0::call-1"},
		{name: "child is scope qualified", agentID: "child-1", callID: "call-1", want: "7:child-1:call-1"},
		{name: "empty call ID has no identity", agentID: "child-1", want: ""},
		{name: "empty parts have no identity", want: ""},
		{name: "separators in both parts stay encoded", agentID: "a:b", callID: "c:d", want: "3:a:b:c:d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ApprovalIdentity(tt.agentID, tt.callID); got != tt.want {
				t.Errorf("ApprovalIdentity(%q, %q) = %q, want %q", tt.agentID, tt.callID, got, tt.want)
			}
		})
	}
}

func TestApprovalIdentityIsInjectiveAcrossAgents(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{"", "call-1"},
		{"", "1:a:x"},
		{"child-1", "call-1"},
		{"child-2", "call-1"},
		{"a", "x"},
		{"child-1:call-1", ""},
		{"a:b", "c:d"},
		{"a", "b:c:d"},
		{"3", ":a:b:c:d"},
	}
	seen := make(map[string][2]string, len(pairs))
	for _, pair := range pairs {
		identity := ApprovalIdentity(pair[0], pair[1])
		if previous, ok := seen[identity]; ok {
			t.Fatalf("ApprovalIdentity(%q, %q) collides with ApprovalIdentity(%q, %q) at %q", pair[0], pair[1], previous[0], previous[1], identity)
		}
		seen[identity] = pair
	}
}

// TestApprovalIdentityParentCallIDCannotImpersonateScopedRequest covers the
// reviewed collision: a parent call ID that literally spells an encoded scoped
// identity must not map to that child request's coordinator key.
func TestApprovalIdentityParentCallIDCannotImpersonateScopedRequest(t *testing.T) {
	t.Parallel()
	parentIdentity := ApprovalIdentity("", "1:a:x")
	childIdentity := ApprovalIdentity("a", "x")
	if parentIdentity == childIdentity {
		t.Fatalf("parent identity %q equals child identity %q", parentIdentity, childIdentity)
	}
	if got, want := parentIdentity, "0::1:a:x"; got != want {
		t.Fatalf("parent identity = %q, want %q", got, want)
	}
	if got, want := childIdentity, "1:a:x"; got != want {
		t.Fatalf("child identity = %q, want %q", got, want)
	}
}

// TestApprovalResponderRoutesSharedCallIDByAgentScope proves two children that
// reuse one provider call ID register distinct coordinator identities and each
// receives only its own decision.
func TestApprovalResponderRoutesSharedCallIDByAgentScope(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	request := func(agentID string) (chan tool.ApprovalResponse, chan error) {
		responseCh := make(chan tool.ApprovalResponse, 1)
		done := make(chan error, 1)
		ctx := tool.WithApprovalAgentScope(context.Background(), agentID)
		go func() {
			done <- responder.RequestApproval(ctx, tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "write"},
				CallID:   "call-shared",
				Reason:   "sandbox_violation",
				Response: responseCh,
				Kind:     tool.ApprovalKindPath,
			})
		}()
		return responseCh, done
	}

	firstResponse, firstDone := request("child-1")
	waitForPendingApproval(t, coordinator)
	secondResponse, secondDone := request("child-2")
	waitForApprovalDepth(t, coordinator, 2)

	firstIdentity := ApprovalIdentity("child-1", "call-shared")
	secondIdentity := ApprovalIdentity("child-2", "call-shared")
	if firstIdentity == secondIdentity {
		t.Fatalf("scoped identities are equal: %q", firstIdentity)
	}
	if got := coordinator.HeadIdentity(); got != firstIdentity {
		t.Fatalf("head identity = %q, want %q", got, firstIdentity)
	}

	// The raw provider call ID names no scoped request, so it resolves nothing.
	coordinator.Submit(SubmitApproval{Identity: "call-shared", Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got := coordinator.PendingDepth(); got != 2 {
		t.Fatalf("pending depth after raw call ID submission = %d, want 2", got)
	}
	assertNoApprovalResponse(t, firstResponse)
	assertNoApprovalResponse(t, secondResponse)

	// A decision for the queued child resolves only that child.
	coordinator.Submit(SubmitApproval{Identity: secondIdentity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if err := <-secondDone; err != nil {
		t.Fatalf("child-2 RequestApproval() error = %v", err)
	}
	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("child-2 response = %#v, want %#v", got, want)
	}
	assertNoApprovalResponse(t, firstResponse)
	if got := coordinator.HeadIdentity(); got != firstIdentity {
		t.Fatalf("head identity after child-2 decision = %q, want %q", got, firstIdentity)
	}

	coordinator.Submit(SubmitApproval{Identity: firstIdentity, Tool: "write", Mode: "prompt", Decision: "deny"})
	if err := <-firstDone; err != nil {
		t.Fatalf("child-1 RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("child-1 response = %#v, want %#v", got, want)
	}
	if coordinator.HasPending() {
		t.Fatal("coordinator still has a pending approval")
	}
}

// TestApprovalResponderParentCallIDCannotClaimScopedRequest proves a parent
// call ID that spells a scoped encoding does not resolve that child request.
func TestApprovalResponderParentCallIDCannotClaimScopedRequest(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	request := func(ctx context.Context, callID string) (chan tool.ApprovalResponse, chan error) {
		responseCh := make(chan tool.ApprovalResponse, 1)
		done := make(chan error, 1)
		go func() {
			done <- responder.RequestApproval(ctx, tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "write"},
				CallID:   callID,
				Reason:   "sandbox_violation",
				Response: responseCh,
				Kind:     tool.ApprovalKindPath,
			})
		}()
		return responseCh, done
	}

	childResponse, childDone := request(tool.WithApprovalAgentScope(context.Background(), "a"), "x")
	waitForPendingApproval(t, coordinator)
	parentResponse, parentDone := request(context.Background(), "1:a:x")
	waitForApprovalDepth(t, coordinator, 2)

	childIdentity := ApprovalIdentity("a", "x")
	parentIdentity := ApprovalIdentity("", "1:a:x")
	if childIdentity == parentIdentity {
		t.Fatalf("parent and child identities are equal: %q", childIdentity)
	}
	if got := coordinator.HeadIdentity(); got != childIdentity {
		t.Fatalf("head identity = %q, want %q", got, childIdentity)
	}

	// The raw colliding string names no registered request.
	coordinator.Submit(SubmitApproval{Identity: "1:a:x", Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got := coordinator.PendingDepth(); got != 2 {
		t.Fatalf("pending depth after colliding submission = %d, want 2", got)
	}
	assertNoApprovalResponse(t, childResponse)
	assertNoApprovalResponse(t, parentResponse)

	coordinator.Submit(SubmitApproval{Identity: childIdentity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if err := <-childDone; err != nil {
		t.Fatalf("child RequestApproval() error = %v", err)
	}
	if got, want := <-childResponse, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("child response = %#v, want %#v", got, want)
	}
	assertNoApprovalResponse(t, parentResponse)

	coordinator.Submit(SubmitApproval{Identity: parentIdentity, Tool: "write", Mode: "prompt", Decision: "deny"})
	if err := <-parentDone; err != nil {
		t.Fatalf("parent RequestApproval() error = %v", err)
	}
	if got, want := <-parentResponse, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("parent response = %#v, want %#v", got, want)
	}
}

// TestApprovalResponderEmptyCallIDFailsClosedWithoutStallingQueue proves a
// request without a call ID has no identity to match, so it is denied without
// registering. Registering it would leave an unpromptable FIFO head and block
// every later request.
func TestApprovalResponderEmptyCallIDFailsClosedWithoutStallingQueue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		ctx       func() context.Context
		wantScope string
	}{
		{name: "parent", ctx: context.Background},
		{name: "scoped child", wantScope: "child-1", ctx: func() context.Context {
			return tool.WithApprovalAgentScope(context.Background(), "child-1")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator := &ApprovalCoordinator{}
			eventCh := make(chan output.Event, 4)
			responder := newApprovalResponder(coordinator, output.SinkFunc(func(event output.Event) {
				eventCh <- event
			}))

			responseCh := make(chan tool.ApprovalResponse, 1)
			if err := responder.RequestApproval(tt.ctx(), tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "write"},
				Reason:   "sandbox_violation",
				Response: responseCh,
				Kind:     tool.ApprovalKindPath,
			}); err != nil {
				t.Fatalf("RequestApproval() error = %v", err)
			}
			if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: approvalMissingCallIDMessage}); got != want {
				t.Fatalf("response = %#v, want %#v", got, want)
			}
			if coordinator.HasPending() {
				t.Fatalf("pending depth = %d, want 0 for an uncorrelatable request", coordinator.PendingDepth())
			}

			var events []output.Event
			for len(events) < 2 {
				select {
				case event := <-eventCh:
					events = append(events, event)
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for approval events: %#v", events)
				}
			}
			if events[0].Type != output.EventTypeApprovalRequested || events[1].Type != output.EventTypeApprovalDenied {
				t.Fatalf("events = %#v, want requested then denied", events)
			}
			if got := events[0].Scope.AgentID; got != tt.wantScope {
				t.Fatalf("requested event scope = %q, want %q", got, tt.wantScope)
			}

			// A later valid request still heads and resolves the queue.
			validCh := make(chan tool.ApprovalResponse, 1)
			validDone := make(chan error, 1)
			go func() {
				validDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
					Tool:     tool.ToolDef{Name: "write"},
					CallID:   "call-after-empty",
					Reason:   "sandbox_violation",
					Response: validCh,
					Kind:     tool.ApprovalKindPath,
				})
			}()
			waitForPendingApproval(t, coordinator)
			wantIdentity := ApprovalIdentity("", "call-after-empty")
			if got := coordinator.HeadIdentity(); got != wantIdentity {
				t.Fatalf("head identity = %q, want %q", got, wantIdentity)
			}
			coordinator.Submit(SubmitApproval{Identity: wantIdentity, Tool: "write", Mode: "prompt", Decision: "allow_once"})
			if err := <-validDone; err != nil {
				t.Fatalf("valid RequestApproval() error = %v", err)
			}
			if got, want := <-validCh, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
				t.Fatalf("valid response = %#v, want %#v", got, want)
			}
		})
	}
}

// TestApprovalResponderScopedCachedGrantSkipsRegistration pins the existing
// session-wide always-allow contract: an explicit always_allow decision caches
// the granted tool for the session, so a later request from another agent is
// served without registering and without touching the FIFO.
func TestApprovalResponderScopedCachedGrantSkipsRegistration(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	firstResponse := make(chan tool.ApprovalResponse, 1)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- responder.RequestApproval(
			tool.WithApprovalAgentScope(context.Background(), "child-1"),
			tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "write"},
				CallID:   "call-first",
				Reason:   "sandbox_violation",
				Response: firstResponse,
				Kind:     tool.ApprovalKindPath,
			},
		)
	}()
	waitForPendingApproval(t, coordinator)
	coordinator.Submit(SubmitApproval{Identity: coordinator.HeadIdentity(), Tool: "write", Mode: "prompt", Decision: "always_allow"})
	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("first response = %#v, want %#v", got, want)
	}

	secondResponse := make(chan tool.ApprovalResponse, 1)
	if err := responder.RequestApproval(
		tool.WithApprovalAgentScope(context.Background(), "child-2"),
		tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			CallID:   "call-second",
			Reason:   "sandbox_violation",
			Response: secondResponse,
			Kind:     tool.ApprovalKindPath,
		},
	); err != nil {
		t.Fatalf("cached scoped RequestApproval() error = %v", err)
	}
	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("cached scoped response = %#v, want %#v", got, want)
	}
	if coordinator.HasPending() {
		t.Fatalf("cached scoped request registered: pending depth = %d", coordinator.PendingDepth())
	}
}

func waitForApprovalDepth(t *testing.T, coordinator *ApprovalCoordinator, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if coordinator.PendingDepth() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("approval pending depth = %d, want %d", coordinator.PendingDepth(), want)
}

func assertNoApprovalResponse(t *testing.T, responseCh chan tool.ApprovalResponse) {
	t.Helper()
	select {
	case response := <-responseCh:
		t.Fatalf("unexpected approval response: %#v", response)
	default:
	}
}
