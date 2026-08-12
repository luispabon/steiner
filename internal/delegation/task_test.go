package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestFailedDelegateSummaryText_NoPreviousOutput(t *testing.T) {
	err := errors.New("deadline exceeded")
	state := agent.RunState{}
	summary := failedDelegateSummaryText(err, state)
	if !strings.Contains(summary, "delegation failed: deadline exceeded") {
		t.Fatalf("expected failure summary, got: %s", summary)
	}
}

func TestFailedDelegateSummaryText_OutputWins(t *testing.T) {
	err := errors.New("deadline exceeded")
	state := agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "found 3 issues in pkg A"},
		},
	}
	summary := failedDelegateSummaryText(err, state)
	if !strings.Contains(summary, "previous output:") {
		t.Errorf("expected previous output in summary, got: %s", summary)
	}
}

func TestFailedDelegateSummaryText_CancellationSaysSessionPreserved(t *testing.T) {
	err := context.Canceled
	summary := failedDelegateSummaryText(err, agent.RunState{})
	if !strings.Contains(summary, "session is preserved") {
		t.Fatalf("expected session-preserved note on cancellation, got: %s", summary)
	}
	if !strings.Contains(summary, "follow_up") {
		t.Fatalf("expected follow_up hint on cancellation, got: %s", summary)
	}
}

func TestCancelledActivitySummary_ZeroTurnsTellsParentSessionIsPreserved(t *testing.T) {
	summary := cancelledActivitySummary(agent.RunState{
		StopReason: agent.StopReasonCancelled,
	})
	if !strings.Contains(summary, "session is preserved") {
		t.Fatalf("expected session-preserved note for zero-turn cancellation, got: %s", summary)
	}
	if !strings.Contains(summary, "follow_up") {
		t.Fatalf("expected follow_up hint for zero-turn cancellation, got: %s", summary)
	}
}

func TestCancelledActivitySummary_WithToolCallsIncludesLastActivity(t *testing.T) {
	state := agent.RunState{
		TurnCount:  3,
		TokenCount: 1500,
		StopReason: agent.StopReasonCancelled,
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "glob", Arguments: map[string]any{"pattern": "**/*_test.go"}}}},
		},
	}
	summary := cancelledActivitySummary(state)
	if !strings.Contains(summary, "last activity: glob(pattern=**/*_test.go)") {
		t.Errorf("expected last activity in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "session is preserved") {
		t.Errorf("expected session-preserved note, got: %s", summary)
	}
}

// extensionStubRunner provides pre-configured responses for Delegate Extension tests.
type extensionStubRunner struct {
	calls     int
	responses []extensionStubResponse
	requests  []agent.RunRequest
}

type extensionStubResponse struct {
	state agent.RunState
	err   error
}

func (r *extensionStubRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.requests = append(r.requests, req)
	if r.calls >= len(r.responses) {
		return agent.RunState{}, fmt.Errorf("unexpected call #%d", r.calls)
	}
	resp := r.responses[r.calls]
	r.calls++
	return resp.state, resp.err
}

func TestRunChildToCompletion_NoExtensionNeeded(t *testing.T) {
	// State already done — loop exits immediately.
	runner := &extensionStubRunner{}
	state := agent.RunState{
		StopReason:        agent.StopReasonComplete,
		TurnCount:         1,
		TokenCount:        10,
		InputTokens:       7,
		CacheReadTokens:   8,
		CacheCreateTokens: 9,
	}
	req := agent.RunRequest{Limits: agent.Limits{MaxTurns: 5}}
	tc := newTraceCollector("test-agent", "test task")

	finalState, usage, granted, err := runChildToCompletion(
		context.Background(), req, runner, 5, nil, tc, state, "test-agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted != 0 {
		t.Errorf("extensionsGranted = %d, want 0", granted)
	}
	if runner.calls != 0 {
		t.Errorf("runner calls = %d, want 0", runner.calls)
	}
	if finalState.StopReason != agent.StopReasonComplete {
		t.Errorf("state stop reason = %s, want %s", finalState.StopReason, agent.StopReasonComplete)
	}
	if usage != cacheUsageOf(state) {
		t.Errorf("usage = %+v, want %+v (cacheUsageOf initial state)", usage, cacheUsageOf(state))
	}
}

func TestRunChildToCompletion_OneExtensionThenComplete(t *testing.T) {
	// One extension needed, then completes.
	// Initial state triggers extension; single runner response is the completion.
	runner := &extensionStubRunner{
		responses: []extensionStubResponse{
			{state: agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, Content: "done"},
				},
				StopReason: agent.StopReasonComplete,
				TurnCount:  2,
				TokenCount: 20,
			}},
		},
	}
	initialState := agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "t"}}},
		},
		StopReason: agent.StopReasonMaxTurns,
		TurnCount:  1,
		TokenCount: 10,
	}
	req := agent.RunRequest{Limits: agent.Limits{MaxTurns: 5}}
	tc := newTraceCollector("test-agent", "test task")

	finalState, _, granted, err := runChildToCompletion(
		context.Background(), req, runner, 5, nil, tc, initialState, "test-agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted != 1 {
		t.Errorf("extensionsGranted = %d, want 1", granted)
	}
	if runner.calls != 1 {
		t.Errorf("runner calls = %d, want 1", runner.calls)
	}
	if finalState.StopReason != agent.StopReasonComplete {
		t.Errorf("state stop reason = %s, want %s", finalState.StopReason, agent.StopReasonComplete)
	}
	if finalState.TurnCount != 2 {
		t.Errorf("final state TurnCount = %d, want 2", finalState.TurnCount)
	}
	// Verify MaxTurns growth: originalMaxTurns + state.TurnCount before extension
	if len(runner.requests) > 0 && runner.requests[0].Limits.MaxTurns != 1+5 {
		t.Errorf("extension MaxTurns = %d, want %d", runner.requests[0].Limits.MaxTurns, 1+5)
	}
}

func TestRunChildToCompletion_MultipleExtensionsThenComplete(t *testing.T) {
	// Needs 3 extensions before completing.
	// Initial state triggers first extension; 2 more return needing more, final completes.
	responses := []extensionStubResponse{
		{state: agent.RunState{
			Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c1", Name: "t"}}},
			},
			StopReason:        agent.StopReasonMaxTurns,
			TurnCount:         2,
			TokenCount:        20,
			InputTokens:       20,
			CacheReadTokens:   200,
			CacheCreateTokens: 2,
		}},
		{state: agent.RunState{
			Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c2", Name: "t"}}},
			},
			StopReason:        agent.StopReasonMaxTurns,
			TurnCount:         3,
			TokenCount:        30,
			InputTokens:       30,
			CacheReadTokens:   300,
			CacheCreateTokens: 3,
		}},
		{state: agent.RunState{
			Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "done"},
			},
			StopReason:        agent.StopReasonComplete,
			TurnCount:         4,
			TokenCount:        40,
			InputTokens:       40,
			CacheReadTokens:   400,
			CacheCreateTokens: 4,
		}},
	}
	runner := &extensionStubRunner{responses: responses}
	initialState := agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "t"}}},
		},
		StopReason:        agent.StopReasonMaxTurns,
		TurnCount:         1,
		TokenCount:        10,
		InputTokens:       10,
		CacheReadTokens:   100,
		CacheCreateTokens: 1,
	}
	req := agent.RunRequest{Limits: agent.Limits{MaxTurns: 5}}
	tc := newTraceCollector("test-agent", "test task")

	finalState, usage, granted, err := runChildToCompletion(
		context.Background(), req, runner, 5, nil, tc, initialState, "test-agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted != 3 {
		t.Errorf("extensionsGranted = %d, want 3", granted)
	}
	if runner.calls != 3 {
		t.Errorf("runner calls = %d, want 3", runner.calls)
	}
	if finalState.StopReason != agent.StopReasonComplete {
		t.Errorf("state stop reason = %s, want %s", finalState.StopReason, agent.StopReasonComplete)
	}
	if finalState.TurnCount != 4 {
		t.Errorf("final state TurnCount = %d, want 4", finalState.TurnCount)
	}
	wantUsage := cacheUsageOf(initialState).
		Add(cacheUsageOf(responses[0].state)).
		Add(cacheUsageOf(responses[1].state)).
		Add(cacheUsageOf(responses[2].state))
	if usage != wantUsage {
		t.Errorf("usage = %+v, want %+v (sum across initial run and all extensions)", usage, wantUsage)
	}
}

func TestRunChildToCompletion_CapsAtMaxExtensions(t *testing.T) {
	// Always needs extension — caps at maxDelegateExtensions.
	var responses []extensionStubResponse
	for i := 0; i < maxDelegateExtensions; i++ {
		responses = append(responses, extensionStubResponse{
			state: agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c", Name: "t"}}},
				},
				StopReason: agent.StopReasonMaxTurns,
				TurnCount:  i + 2,
				TokenCount: (i + 1) * 10,
			},
		})
	}
	runner := &extensionStubRunner{responses: responses}
	initialState := agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c", Name: "t"}}},
		},
		StopReason: agent.StopReasonMaxTurns,
		TurnCount:  1,
		TokenCount: 10,
	}
	req := agent.RunRequest{Limits: agent.Limits{MaxTurns: 5}}
	tc := newTraceCollector("test-agent", "test task")

	finalState, _, granted, err := runChildToCompletion(
		context.Background(), req, runner, 5, nil, tc, initialState, "test-agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted != maxDelegateExtensions {
		t.Errorf("extensionsGranted = %d, want %d", granted, maxDelegateExtensions)
	}
	if runner.calls != maxDelegateExtensions {
		t.Errorf("runner calls = %d, want %d", runner.calls, maxDelegateExtensions)
	}
	// Final state should still have tools (no completion occurred).
	if finalState.StopReason != agent.StopReasonMaxTurns {
		t.Errorf("state stop reason = %s, want %s", finalState.StopReason, agent.StopReasonMaxTurns)
	}
}

func TestRunChildToCompletion_ErrorDuringExtension(t *testing.T) {
	// First extension succeeds (returns state that still needs extension),
	// second extension fails during execution.
	runner := &extensionStubRunner{
		responses: []extensionStubResponse{
			{state: agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c1", Name: "t"}}},
				},
				StopReason:        agent.StopReasonMaxTurns,
				TurnCount:         2,
				TokenCount:        20,
				InputTokens:       20,
				CacheReadTokens:   200,
				CacheCreateTokens: 2,
			}},
			{err: fmt.Errorf("provider error")},
		},
	}
	initialState := agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "t"}}},
		},
		StopReason:        agent.StopReasonMaxTurns,
		TurnCount:         1,
		TokenCount:        10,
		InputTokens:       10,
		CacheReadTokens:   100,
		CacheCreateTokens: 1,
	}
	req := agent.RunRequest{Limits: agent.Limits{MaxTurns: 5}}
	tc := newTraceCollector("test-agent", "test task")

	finalState, usage, granted, err := runChildToCompletion(
		context.Background(), req, runner, 5, nil, tc, initialState, "test-agent",
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider error" {
		t.Errorf("error = %q, want %q", err.Error(), "provider error")
	}
	// Two extensions were granted: first succeeded (state updated to TurnCount=2),
	// second failed during execution (state before it was TurnCount=2).
	if granted != 2 {
		t.Errorf("extensionsGranted = %d, want 2", granted)
	}
	if runner.calls != 2 {
		t.Errorf("runner calls = %d, want 2", runner.calls)
	}
	// State returned is the state before the failed run: TurnCount=2
	// (result of the first successful extension).
	if finalState.TurnCount != 2 {
		t.Errorf("final state TurnCount = %d, want 2 (state before failed run)", finalState.TurnCount)
	}
	wantUsage := cacheUsageOf(initialState).Add(cacheUsageOf(runner.responses[0].state))
	if usage != wantUsage {
		t.Errorf("usage = %+v, want %+v (usage through last successful state, excluding failed run)", usage, wantUsage)
	}
}
