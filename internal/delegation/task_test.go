package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestFailedDelegateReason_IncludesError(t *testing.T) {
	t.Parallel()
	err := errors.New("deadline exceeded")
	reason := failedDelegateReason(err, agent.RunState{})
	if !strings.Contains(reason, "delegation failed: deadline exceeded") {
		t.Fatalf("expected failure reason, got: %s", reason)
	}
}

func TestFailedDelegateReason_CountsToolActivity(t *testing.T) {
	t.Parallel()
	err := errors.New("deadline exceeded")
	state := agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "read"}}}}}
	reason := failedDelegateReason(err, state)
	if !strings.Contains(reason, "activity before failure: 1 tool call(s)") {
		t.Errorf("expected tool activity count in reason, got: %s", reason)
	}
}

func TestFailedDelegateReason_CancellationSaysSessionPreserved(t *testing.T) {
	t.Parallel()
	reason := failedDelegateReason(context.Canceled, agent.RunState{})
	if !strings.Contains(reason, "session is preserved") || !strings.Contains(reason, "follow_up") {
		t.Fatalf("expected preserved-session follow_up note, got: %s", reason)
	}
}

func TestCancelledDelegateReason_ZeroTurnsTellsParentSessionIsPreserved(t *testing.T) {
	t.Parallel()
	reason := cancelledDelegateReason(agent.RunState{StopReason: agent.StopReasonCancelled})
	if !strings.Contains(reason, "session is preserved") || !strings.Contains(reason, "follow_up") {
		t.Fatalf("expected preserved-session follow_up note, got: %s", reason)
	}
}

func TestCancelledDelegateReason_NamesLastToolWithoutArguments(t *testing.T) {
	t.Parallel()
	state := agent.RunState{
		TurnCount: 3, TokenCount: 1500, StopReason: agent.StopReasonCancelled,
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "glob", Arguments: map[string]any{"pattern": "**/*_test.go"}}}}},
	}
	reason := cancelledDelegateReason(state)
	if !strings.Contains(reason, "last activity: glob;") || strings.Contains(reason, "**/*_test.go") || !strings.Contains(reason, "session is preserved") {
		t.Errorf("unexpected cancellation reason: %s", reason)
	}
}

func TestSpawnDelegateRunsChildOnceAtTurnLimit(t *testing.T) {
	t.Parallel()
	calls := 0
	state := agent.RunState{
		StopReason:   agent.StopReasonMaxTurns,
		TurnCount:    2,
		TokenCount:   17,
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "read"}}}},
	}
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		calls++
		if req.Limits.MaxTurns != 2 {
			t.Errorf("MaxTurns = %d, want 2", req.Limits.MaxTurns)
		}
		return state, nil
	}}

	result, gotState, usage, err := SpawnDelegate(context.Background(), Spec{AgentID: "single-run", Limits: Limits{MaxTurns: 2}}, agent.RunRequest{Limits: agent.Limits{MaxTurns: 2}}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	if gotState.StopReason != agent.StopReasonMaxTurns || usage != tokenUsageOf(state) {
		t.Fatalf("state/usage = (%s, %+v), want max-turn state and %+v", gotState.StopReason, usage, tokenUsageOf(state))
	}
	if got := result.Value.(Result).TurnCount; got != state.TurnCount {
		t.Fatalf("result turns = %d, want %d", got, state.TurnCount)
	}
}

func TestTurnBudgetNoticeFunc(t *testing.T) {
	t.Parallel()
	got := turnBudgetNoticeFunc()(21, 30)
	for _, field := range []string{"21", "30", "9", "remaining"} {
		if !strings.Contains(got, field) {
			t.Errorf("turn budget notice %q does not express dynamic field %q", got, field)
		}
	}
}

func TestSpawnDelegate_SetsInitialTurnBudgetNotice(t *testing.T) {
	t.Parallel()
	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}
	_, _, _, err := SpawnDelegate(context.Background(), Spec{AgentID: "initial-notice"}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if capturedReq.TurnBudgetNotice == nil {
		t.Fatal("expected TurnBudgetNotice to be set on the child run")
	}
	if got := capturedReq.TurnBudgetNotice(21, 30); !strings.Contains(got, "You have used 21 of 30 turns (9 remaining).") {
		t.Errorf("unexpected turn budget notice: %q", got)
	}
}

func TestSpawnDelegate_DoesNotEmitStartedEvent(t *testing.T) {
	t.Parallel()
	sink := &collectingSink{}
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) { return successRunState(), nil }}
	req := agent.RunRequest{ResolvedModel: provider.ResolvedModel{Alias: "inferx/deepseek-v4-flash"}}
	spec := Spec{AgentID: "child-callid", Task: "inspect", ParentCallID: "call_parent"}
	_, _, _, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	for _, event := range sink.events {
		if event.Type == output.EventTypeDelegationStarted {
			t.Error("SpawnDelegate emitted a DelegationStarted event")
		}
	}
}
