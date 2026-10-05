package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// callIDRecordingExecutor records the reserved child agent ID each delegation
// handler observes, keyed by the handler's own tool-call ID.
type callIDRecordingExecutor struct {
	onExecute func(ctx context.Context, name, callID string) (any, error)
}

func (e callIDRecordingExecutor) Execute(ctx context.Context, name, callID string, _ map[string]any) (any, error) {
	return e.onExecute(ctx, name, callID)
}

// TestExecuteToolCalls_ReservedAgentIDsFollowCallOrder forces two delegation
// calls to complete in reverse order (call-b finishes before call-a) and
// asserts each handler still receives the child agent ID reserved for its own
// call. The shared mint counter stands in for the in-handler ID generator: if
// the reserved ID were not stamped onto the call's context, the later-starting
// handler would mint first and the IDs would be swapped.
func TestExecuteToolCalls_ReservedAgentIDsFollowCallOrder(t *testing.T) {
	t.Parallel()

	var mintMu sync.Mutex
	next := 0
	mint := func() string {
		mintMu.Lock()
		defer mintMu.Unlock()
		next++
		return fmt.Sprintf("child-%d", next)
	}

	bDone := make(chan struct{})
	var mu sync.Mutex
	observed := map[string]string{}
	executor := callIDRecordingExecutor{onExecute: func(ctx context.Context, _, callID string) (any, error) {
		id := DelegationAgentIDFrom(ctx)
		if id == "" {
			id = mint() // stand-in for the in-handler generateAgentID fallback
		}
		mu.Lock()
		observed[callID] = id
		mu.Unlock()
		switch callID {
		case "call-b":
			close(bDone)
		case "call-a":
			<-bDone
		}
		return "ok-" + callID, nil
	}}

	p := newTurnProgressor(RunRequest{
		Executor:               executor,
		Events:                 output.NoopSink{},
		ParallelClassOf:        func(string) ParallelClass { return ParallelClassDelegation },
		MaxParallelDelegations: 2,
		ReserveDelegationAgentID: func(toolName string) string {
			if toolName != "sub_agent" {
				return ""
			}
			return mint()
		},
	}, prompt.AssemblyOptions{}, nil)

	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "call-a", Name: "sub_agent"},
		{ID: "call-b", Name: "sub_agent"},
	}}}
	outcome := p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, calls)
	if outcome.Stop {
		t.Fatalf("executeToolCalls stopped unexpectedly: %v", outcome.State.StopReason)
	}

	if got := observed["call-a"]; got != "child-1" {
		t.Errorf("call-a reserved ID = %q, want child-1 (IDs must follow call order, not completion order)", got)
	}
	if got := observed["call-b"]; got != "child-2" {
		t.Errorf("call-b reserved ID = %q, want child-2 (IDs must follow call order, not completion order)", got)
	}
}

// TestExecuteToolCalls_NoReservationLeavesHandlersUnstamped verifies the
// fallback contract: without a reserver, handlers see no reservation and must
// mint their own ID (here, the executor's stand-in generator).
func TestExecuteToolCalls_NoReservationLeavesHandlersUnstamped(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	observed := map[string]string{}
	executor := callIDRecordingExecutor{onExecute: func(ctx context.Context, _, callID string) (any, error) {
		id := DelegationAgentIDFrom(ctx)
		mu.Lock()
		observed[callID] = id
		mu.Unlock()
		return "ok-" + callID, nil
	}}

	p := newTurnProgressor(RunRequest{
		Executor:               executor,
		Events:                 output.NoopSink{},
		ParallelClassOf:        func(string) ParallelClass { return ParallelClassDelegation },
		MaxParallelDelegations: 2,
	}, prompt.AssemblyOptions{}, nil)

	calls := provider.ChatResponse{Message: provider.Message{ToolCalls: []provider.ToolCall{
		{ID: "call-a", Name: "sub_agent"},
		{ID: "call-b", Name: "sub_agent"},
	}}}
	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, calls)

	if got := observed["call-a"]; got != "" {
		t.Errorf("call-a reservation = %q, want empty without a reserver", got)
	}
	if got := observed["call-b"]; got != "" {
		t.Errorf("call-b reservation = %q, want empty without a reserver", got)
	}
}
