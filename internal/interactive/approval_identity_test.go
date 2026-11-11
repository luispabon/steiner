package interactive

import (
	"context"
	"testing"
	"time"

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
		{name: "parent keeps provider call ID", callID: "call-1", want: "call-1"},
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
		{"child-1", "call-1"},
		{"child-2", "call-1"},
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

// TestApprovalResponderScopedEmptyCallIDIsNotSubmittable proves a scoped request
// without a call ID registers no matchable identity, so it can never be
// authorized as if it were a real request.
func TestApprovalResponderScopedEmptyCallIDIsNotSubmittable(t *testing.T) {
	t.Parallel()
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	ctx, cancel := context.WithCancel(tool.WithApprovalAgentScope(context.Background(), "child-1"))
	defer cancel()
	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(ctx, tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			Reason:   "sandbox_violation",
			Response: responseCh,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)

	if got := coordinator.HeadIdentity(); got != "" {
		t.Fatalf("head identity = %q, want empty for a scoped request without a call ID", got)
	}
	coordinator.Submit(SubmitApproval{Identity: ApprovalIdentity("child-1", ""), Tool: "write", Mode: "prompt", Decision: "allow_once"})
	assertNoApprovalResponse(t, responseCh)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval() did not return after cancellation")
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
