package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func completeEventFrom(events []output.Event) (output.DelegationCompleteEvent, bool) {
	for _, ev := range events {
		if ev.Type == output.EventTypeDelegationComplete {
			payload, ok := ev.Payload.(output.DelegationCompleteEvent)
			return payload, ok
		}
	}
	return output.DelegationCompleteEvent{}, false
}

func failedEventFrom(events []output.Event) (output.DelegationFailedEvent, bool) {
	for _, ev := range events {
		if ev.Type == output.EventTypeDelegationFailed {
			payload, ok := ev.Payload.(output.DelegationFailedEvent)
			return payload, ok
		}
	}
	return output.DelegationFailedEvent{}, false
}

// TestSpecializedHandlerCompleteEventCarriesAdvisorBudget is the B1 regression:
// the DelegationCompleteEvent emitted by SpawnDelegate must carry the
// configured per-child advisor budget, not zero.
func TestSpecializedHandlerCompleteEventCarriesAdvisorBudget(t *testing.T) {
	events := &recordingEventSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.Events = events
	deps.ExtraAllowedTools = map[AgentType][]string{AgentTypeReview: {advisor.ToolName}}
	deps.AdvisorForChild = func(string) (tool.ToolDef, bool) {
		return tool.ToolDef{Name: advisor.ToolName}, true
	}
	deps.AdvisorSubAgentBudget = 3

	handler := newSpecializedHandler(AgentTypeReview, deps)
	if _, err := handler(context.Background(), validStructuredTask("review a diff")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	payload, ok := completeEventFrom(events.Events())
	if !ok {
		t.Fatal("no DelegationCompleteEvent emitted")
	}
	if payload.AdvisorBudget != 3 {
		t.Errorf("AdvisorBudget = %d, want 3", payload.AdvisorBudget)
	}
}

// TestSpecializedHandlerCompleteEventZeroBudgetWhenAdvisorUnavailable is the
// mirror case: a child with no advisor access emits AdvisorBudget == 0.
func TestSpecializedHandlerCompleteEventZeroBudgetWhenAdvisorUnavailable(t *testing.T) {
	events := &recordingEventSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.Events = events
	deps.AdvisorSubAgentBudget = 3 // configured, but explore never gets advisor in its allowlist

	handler := newSpecializedHandler(AgentTypeExplore, deps)
	if _, err := handler(context.Background(), validStructuredTask("explore the repo")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	payload, ok := completeEventFrom(events.Events())
	if !ok {
		t.Fatal("no DelegationCompleteEvent emitted")
	}
	if payload.AdvisorBudget != 0 {
		t.Errorf("AdvisorBudget = %d, want 0", payload.AdvisorBudget)
	}
}

// TestFollowUpCompleteEventCarriesAdvisorBudget is the B1 follow-up case: a
// follow_up resumption's completion event must also carry the correct
// non-zero advisor budget.
func TestFollowUpCompleteEventCarriesAdvisorBudget(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{
			AgentID:   "child-1",
			AgentType: AgentTypeReview,
			Task:      "inspect code",
			Limits:    Limits{MaxTurns: 2, OutputLimitTokens: 9},
		},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 2, MaxTokens: 9},
			Tools: []provider.ToolSpec{
				{Type: "function", Function: provider.ToolFunctionSpec{Name: advisor.ToolName}},
			},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount:     2,
		TokenCount:    40,
		ToolCallCount: 1,
	})

	events := &recordingEventSink{}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		Events:                events,
		SessionStore:          store,
		AdvisorSubAgentBudget: 5,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			if _, ok := req.Executor.(summaryOnlyExecutor); ok {
				return agent.RunState{
					Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}},
					TurnCount:    1,
					TokenCount:   1,
					StopReason:   agent.StopReasonComplete,
				}, nil
			}
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "first answer"},
					{Role: agent.MessageRoleUser, Content: "continue"},
					{Role: agent.MessageRoleAssistant, Content: "second answer"},
				},
				TurnCount:  3,
				TokenCount: 15,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	if _, err := handler(context.Background(), map[string]any{
		"agent_id": "child-1",
		"message":  "continue",
	}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	payload, ok := completeEventFrom(events.Events())
	if !ok {
		t.Fatal("no DelegationCompleteEvent emitted")
	}
	if payload.AdvisorBudget != 5 {
		t.Errorf("AdvisorBudget = %d, want 5", payload.AdvisorBudget)
	}
}

// TestSpawnDelegateOutputCarriesAdvisorSummaryLine is the I2 test: the
// emitted DelegationCompleteEvent.Output must contain the advisor summary
// line when usage is non-zero, and must not contain it when both counters
// are zero. result.Summary must never contain the summary line.
func TestSpawnDelegateOutputCarriesAdvisorSummaryLine(t *testing.T) {
	conversationWithAdvisorUse := []agent.Message{
		{
			Role: agent.MessageRoleAssistant,
			ToolCalls: []agent.ToolCall{
				{ID: "c0", Name: advisor.ToolName, Arguments: map[string]any{"question": "q"}},
			},
		},
		{Role: agent.MessageRoleTool, Name: advisor.ToolName, ToolCallID: "c0", Content: "advisor answer"},
		{Role: agent.MessageRoleAssistant, Content: "final output"},
	}

	t.Run("non-zero usage appends summary line to Output only", func(t *testing.T) {
		events := &recordingEventSink{}
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			if _, ok := req.Executor.(summaryOnlyExecutor); ok {
				return agent.RunState{
					Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "retained summary"}},
					TurnCount:    1,
					StopReason:   agent.StopReasonComplete,
				}, nil
			}
			return agent.RunState{
				Conversation: conversationWithAdvisorUse,
				TurnCount:    1,
				StopReason:   agent.StopReasonComplete,
			}, nil
		}}

		spec := Spec{AgentID: "agent-advisor-summary", AdvisorBudget: 2}
		res, _, _, err := SpawnDelegate(context.Background(), spec, agent.RunRequest{}, runner, events, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		payload, ok := completeEventFrom(events.Events())
		if !ok {
			t.Fatal("no DelegationCompleteEvent emitted")
		}
		const wantLine = "advisor: 1 used, 0 denied"
		if !strings.Contains(payload.Output, wantLine) {
			t.Errorf("event Output %q does not contain %q", payload.Output, wantLine)
		}

		result, ok := res.Value.(Result)
		if !ok {
			t.Fatalf("result.Value type = %T, want Result", res.Value)
		}
		if !strings.Contains(result.Output, wantLine) {
			t.Errorf("result.Output %q does not contain %q", result.Output, wantLine)
		}
		if strings.Contains(result.Summary, wantLine) {
			t.Errorf("result.Summary %q must not contain the advisor summary line", result.Summary)
		}
	})

	t.Run("zero usage leaves Output unchanged", func(t *testing.T) {
		events := &recordingEventSink{}
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			if _, ok := req.Executor.(summaryOnlyExecutor); ok {
				return agent.RunState{
					Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "retained summary"}},
					TurnCount:    1,
					StopReason:   agent.StopReasonComplete,
				}, nil
			}
			return agent.RunState{
				Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "final output"}},
				TurnCount:    1,
				StopReason:   agent.StopReasonComplete,
			}, nil
		}}

		spec := Spec{AgentID: "agent-no-advisor", AdvisorBudget: 2}
		_, _, _, err := SpawnDelegate(context.Background(), spec, agent.RunRequest{}, runner, events, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		payload, ok := completeEventFrom(events.Events())
		if !ok {
			t.Fatal("no DelegationCompleteEvent emitted")
		}
		if strings.Contains(payload.Output, "advisor:") {
			t.Errorf("event Output %q should not contain an advisor summary line", payload.Output)
		}
	})
}

// TestSpawnDelegateFailedEventCarriesAdvisorCounters is the B2 test: a
// post-run failure (the child's initial runner.Run call errors) emits a
// DelegationFailedEvent carrying the advisor counters observed before the
// failure, rather than leaving them at zero for every caller.
func TestSpawnDelegateFailedEventCarriesAdvisorCounters(t *testing.T) {
	runErr := errors.New("boom")
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return agent.RunState{
			Conversation: []agent.Message{
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c0", Name: advisor.ToolName, Arguments: map[string]any{"question": "q"}},
					},
				},
				{Role: agent.MessageRoleTool, Name: advisor.ToolName, ToolCallID: "c0", Content: "advisor answer"},
			},
			TurnCount: 1,
		}, runErr
	}}

	events := &recordingEventSink{}
	spec := Spec{AgentID: "agent-failed-advisor", AdvisorBudget: 4}
	_, _, _, err := SpawnDelegate(context.Background(), spec, agent.RunRequest{}, runner, events, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate returned an outer error: %v", err)
	}

	payload, ok := failedEventFrom(events.Events())
	if !ok {
		t.Fatal("no DelegationFailedEvent emitted")
	}
	if payload.AdvisorBudget != 4 {
		t.Errorf("AdvisorBudget = %d, want 4", payload.AdvisorBudget)
	}
	if payload.AdvisorUses != 1 {
		t.Errorf("AdvisorUses = %d, want 1", payload.AdvisorUses)
	}
	if payload.AdvisorDenied != 0 {
		t.Errorf("AdvisorDenied = %d, want 0", payload.AdvisorDenied)
	}
}

// TestEmitDelegateFailedSetupSiteReportsZeroAdvisorCounters confirms
// setup-failure sites (before the child ever ran) correctly emit zero
// advisor counters rather than leaving them unpopulated by accident.
func TestEmitDelegateFailedSetupSiteReportsZeroAdvisorCounters(t *testing.T) {
	events := &recordingEventSink{}
	spec := Spec{AgentID: "agent-setup-fail", Task: "task", AdvisorBudget: 4}
	emitDelegateFailed(events, spec, AgentTypeExplore, "setup failed")

	payload, ok := failedEventFrom(events.Events())
	if !ok {
		t.Fatal("no DelegationFailedEvent emitted")
	}
	if payload.AdvisorBudget != 0 || payload.AdvisorUses != 0 || payload.AdvisorDenied != 0 {
		t.Errorf("advisor counters = %d/%d/%d, want all zero at a setup-failure site", payload.AdvisorBudget, payload.AdvisorUses, payload.AdvisorDenied)
	}
}
