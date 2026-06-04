package interactive

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

func TestSessionSnapshotSinkCapturesAPIRequestEvent(t *testing.T) {
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	snaps := s.SnapshotStore()

	_, ok := snaps.Snapshot()
	if ok {
		t.Fatal("expected no snapshot initially")
	}

	s.EventSink().Emit(output.NewAPIRequestEvent(
		"test-model",
		nil, nil, nil, nil,
		prompt.ModelTokenBudget{},
	))

	snap, ok := snaps.Snapshot()
	if !ok {
		t.Fatal("expected snapshot after APIRequestEvent")
	}
	if snap.Model != "test-model" {
		t.Fatalf("snapshot model = %q, want %q", snap.Model, "test-model")
	}
}

func TestSessionSnapshotSinkIgnoresNonAPIRequestEvents(t *testing.T) {
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	snaps := s.SnapshotStore()

	s.EventSink().Emit(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))

	_, ok := snaps.Snapshot()
	if ok {
		t.Fatal("expected no snapshot after non-API-request event")
	}
}

func TestSessionApproverReturnsNonNil(t *testing.T) {
	var events []output.Event
	sink := output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	})

	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	responder := s.Approver(sink)

	if responder == nil {
		t.Fatal("Approver() returned nil")
	}
}

func TestApprovalResponderAllowsAndCachesAlwaysAllow(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator)

	firstResponse := make(chan tool.ApprovalResponse, 1)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			Reason:   "sandbox_violation",
			Response: firstResponse,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Tool:     "write",
		Mode:     "prompt",
		Decision: "always_allow",
	})

	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("first response = %#v, want %#v", got, want)
	}

	secondResponse := make(chan tool.ApprovalResponse, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			Reason:   "sandbox_violation",
			Response: secondResponse,
		})
	}()

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second RequestApproval() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second RequestApproval() blocked, want cached always-allow response")
	}

	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("second response = %#v, want %#v", got, want)
	}
}

func TestApprovalResponderDeniesDecisions(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "bash"},
			Reason:   "sandbox_violation",
			Response: responseCh,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Tool:     "bash",
		Mode:     "prompt",
		Decision: "deny",
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestApprovalResponderDoesNotDependOnTerminalHandoff(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "edit"},
			Reason:   "sandbox_violation",
			Response: responseCh,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Tool:     "edit",
		Mode:     "prompt",
		Decision: "allow_once",
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestApprovalResponseForDecision(t *testing.T) {
	tests := []struct {
		decision string
		want     tool.ApprovalResponse
	}{
		{"always_allow", tool.ApprovalResponse{Allow: true, Message: "always allowed"}},
		{"allow_once", tool.ApprovalResponse{Allow: true, Message: "approved"}},
		{"deny", tool.ApprovalResponse{Allow: false, Message: "denied"}},
		{"unknown", tool.ApprovalResponse{Allow: false, Message: "denied"}},
	}
	for _, tt := range tests {
		got := approvalResponseForDecision(tt.decision)
		if got != tt.want {
			t.Errorf("approvalResponseForDecision(%q) = %#v, want %#v", tt.decision, got, tt.want)
		}
	}
}

func waitForPendingApproval(t *testing.T, coordinator *ApprovalCoordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if coordinator.HasPending() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval request was not registered")
}
