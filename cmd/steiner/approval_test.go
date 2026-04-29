package main

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

func TestTUIApprovalResponderAllowsAndCachesAlwaysAllow(t *testing.T) {
	coordinator := &tuiApprovalCoordinator{}
	responder := newTUIApprovalResponder(coordinator)

	firstResponse := make(chan tool.ApprovalResponse, 1)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			Mode:     config.ApprovalModePrompt,
			Response: firstResponse,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.submit(tui.ApprovalSubmission{
		Tool:     "write",
		Mode:     "prompt",
		Decision: tui.ApprovalDecisionAlwaysAllow,
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
			Mode:     config.ApprovalModePrompt,
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

func TestTUIApprovalResponderDeniesDecisions(t *testing.T) {
	coordinator := &tuiApprovalCoordinator{}
	responder := newTUIApprovalResponder(coordinator)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "bash"},
			Mode:     config.ApprovalModePrompt,
			Response: responseCh,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.submit(tui.ApprovalSubmission{
		Tool:     "bash",
		Mode:     "prompt",
		Decision: tui.ApprovalDecisionDeny,
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestTUIApprovalResponderDoesNotDependOnTerminalHandoff(t *testing.T) {
	coordinator := &tuiApprovalCoordinator{}
	responder := newTUIApprovalResponder(coordinator)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "edit"},
			Mode:     config.ApprovalModePrompt,
			Response: responseCh,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.submit(tui.ApprovalSubmission{
		Tool:     "edit",
		Mode:     "prompt",
		Decision: tui.ApprovalDecisionAllowOnce,
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func waitForPendingApproval(t *testing.T, coordinator *tuiApprovalCoordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		pending := coordinator.pending
		coordinator.mu.Unlock()
		if pending != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval request was not registered")
}
