package interactive

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// TestApprovalResponderDuplicateSameScopeRequestsGetUniqueIDs reproduces the
// same-scope reuse finding: two requests from one agent with one provider call
// ID must register distinct identities, and a decision for one resolves only
// that request.
func TestApprovalResponderDuplicateSameScopeRequestsGetUniqueIDs(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	requestedIDs := make(chan string, 2)
	responder := newApprovalResponder(coordinator, output.SinkFunc(func(event output.Event) {
		if event.Type != output.EventTypeApprovalRequested {
			return
		}
		if payload, ok := event.Payload.(output.ApprovalEvent); ok {
			requestedIDs <- payload.RequestID
		}
	}))

	request := func() (chan tool.ApprovalResponse, chan error) {
		responseCh := make(chan tool.ApprovalResponse, 1)
		done := make(chan error, 1)
		go func() {
			done <- responder.RequestApproval(
				tool.WithApprovalAgentScope(context.Background(), "child-1"),
				tool.ApprovalRequest{
					Tool:     tool.ToolDef{Name: "write"},
					CallID:   "call-dup",
					Reason:   "sandbox_violation",
					Response: responseCh,
					Kind:     tool.ApprovalKindPath,
				},
			)
		}()
		return responseCh, done
	}

	firstResponse, firstDone := request()
	firstID := <-requestedIDs
	secondResponse, secondDone := request()
	secondID := <-requestedIDs
	if firstID == "" || secondID == "" {
		t.Fatalf("requested IDs = %q/%q, want non-empty", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("duplicate same-scope requests share identity %q", firstID)
	}
	waitForApprovalDepth(t, coordinator, 2)
	if got := coordinator.HeadIdentity(); got != firstID {
		t.Fatalf("head identity = %q, want %q", got, firstID)
	}

	// Deciding the queued request must resolve only that request.
	coordinator.Submit(SubmitApproval{Identity: secondID, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if err := <-secondDone; err != nil {
		t.Fatalf("second RequestApproval() error = %v", err)
	}
	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("second response = %#v, want %#v", got, want)
	}
	assertNoApprovalResponse(t, firstResponse)
	if got := coordinator.HeadIdentity(); got != firstID {
		t.Fatalf("head identity after second decision = %q, want %q", got, firstID)
	}

	coordinator.Submit(SubmitApproval{Identity: firstID, Tool: "write", Mode: "prompt", Decision: "deny"})
	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("first response = %#v, want %#v", got, want)
	}
	if coordinator.HasPending() {
		t.Fatal("coordinator still has a pending approval")
	}
}

// TestApprovalResponderEmptyCallIDFailsClosedWithoutStallingQueue proves a
// request without a call ID is denied without registering. Registering it
// would leave an unpromptable FIFO head and block every later request.
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
			requested, ok := events[0].Payload.(output.ApprovalEvent)
			if !ok || requested.RequestID == "" {
				t.Fatalf("requested event payload = %#v, want a request ID", events[0].Payload)
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
			wantIdentity := coordinator.HeadIdentity()
			if wantIdentity == "" {
				t.Fatal("valid request has no head identity")
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
// served without registering and without touching the FIFO. Its events still
// carry a unique request ID so the transcript can resolve them.
func TestApprovalResponderScopedCachedGrantSkipsRegistration(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	requestedIDs := make(chan string, 2)
	responder := newApprovalResponder(coordinator, output.SinkFunc(func(event output.Event) {
		if event.Type != output.EventTypeApprovalRequested {
			return
		}
		if payload, ok := event.Payload.(output.ApprovalEvent); ok {
			requestedIDs <- payload.RequestID
		}
	}))

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
	firstID := <-requestedIDs
	coordinator.Submit(SubmitApproval{Identity: firstID, Tool: "write", Mode: "prompt", Decision: "always_allow"})
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
	secondID := <-requestedIDs
	if secondID == "" || secondID == firstID {
		t.Fatalf("cached grant request IDs = %q/%q, want distinct non-empty", firstID, secondID)
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
