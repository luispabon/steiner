package interactive

import (
	"context"
	"errors"
	"testing"
)

type recordingDelegateCanceller struct {
	agentID  string
	discard  bool
	agentErr error
	allCalls int
	calls    int
}

func (c *recordingDelegateCanceller) CancelAgent(agentID string, discard bool) error {
	c.agentID = agentID
	c.discard = discard
	c.calls++
	return c.agentErr
}

func (c *recordingDelegateCanceller) CancelAll() error {
	c.allCalls++
	return c.agentErr
}

func TestCancelDelegateForwardsArguments(t *testing.T) {
	canceller := &recordingDelegateCanceller{}
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})

	if err := s.Handle(context.Background(), CancelDelegate{AgentID: "child-7", Discard: true}); err != nil {
		t.Fatalf("Handle(CancelDelegate) = %v, want nil", err)
	}
	if canceller.calls != 1 {
		t.Fatalf("CancelAgent calls = %d, want 1", canceller.calls)
	}
	if canceller.agentID != "child-7" || !canceller.discard {
		t.Fatalf("CancelAgent arguments = (%q, %v), want (%q, true)", canceller.agentID, canceller.discard, "child-7")
	}

	if err := s.Handle(context.Background(), CancelDelegate{AgentID: "child-8", Discard: false}); err != nil {
		t.Fatalf("Handle(CancelDelegate) = %v, want nil", err)
	}
	if canceller.agentID != "child-8" || canceller.discard {
		t.Fatalf("CancelAgent arguments = (%q, %v), want (%q, false)", canceller.agentID, canceller.discard, "child-8")
	}
}

func TestCancelDelegateReportsCancellerError(t *testing.T) {
	canceller := &recordingDelegateCanceller{agentErr: errors.New("delegate already finished; worktree retained")}
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})

	if err := s.Handle(context.Background(), CancelDelegate{AgentID: "child-7", Discard: true}); err == nil || err.Error() != "delegate already finished; worktree retained" {
		t.Fatalf("Handle(CancelDelegate) = %v, want canceller error", err)
	}
}

func TestCancelAllDelegatesCallsCanceller(t *testing.T) {
	canceller := &recordingDelegateCanceller{}
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})

	if err := s.Handle(context.Background(), CancelAllDelegates{}); err != nil {
		t.Fatalf("Handle(CancelAllDelegates) = %v, want nil", err)
	}
	if canceller.allCalls != 1 {
		t.Fatalf("CancelAll calls = %d, want 1", canceller.allCalls)
	}
}

func TestDelegateCancellationRequiresCanceller(t *testing.T) {
	s := testNewSession(t, Dependencies{})
	for _, action := range []Action{CancelDelegate{AgentID: "child"}, CancelAllDelegates{}} {
		if err := s.Handle(context.Background(), action); err == nil {
			t.Errorf("Handle(%T) = nil, want error", action)
		}
	}
}

func TestInterruptActiveRunDoesNotUseDelegateCanceller(t *testing.T) {
	canceller := &recordingDelegateCanceller{agentErr: errors.New("unexpected delegate cancellation")}
	ctx, cancel := context.WithCancel(context.Background())
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})
	s.runController.Set(cancel)

	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("Handle(InterruptActiveRun) = %v, want nil", err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("InterruptActiveRun did not interrupt active run")
	}
	if canceller.calls != 0 || canceller.allCalls != 0 {
		t.Fatalf("delegate canceller calls = (%d, %d), want (0, 0)", canceller.calls, canceller.allCalls)
	}
}

var _ DelegateCanceller = (*recordingDelegateCanceller)(nil)
