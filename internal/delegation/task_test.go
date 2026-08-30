package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
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

const testTurnBudgetMarker = "[turn-budget-checkpoint]"

func (r *extensionStubRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.requests = append(r.requests, req)
	if r.calls >= len(r.responses) {
		return agent.RunState{}, fmt.Errorf("unexpected call #%d", r.calls)
	}
	resp := r.responses[r.calls]
	r.calls++
	if req.TurnBudgetNotice != nil {
		notice := testTurnBudgetMarker + " " + req.TurnBudgetNotice(resp.state.TurnCount, req.Limits.MaxTurns)
		resp.state.Conversation = supersedeOrAppendNoticeForTest(resp.state.Conversation, notice)
	}
	return resp.state, resp.err
}

// supersedeOrAppendNoticeForTest mirrors (without importing) the marker-based
// supersede-in-place semantics of agent.injectTurnBudgetNoticeIfDue, so tests
// can verify task.go's per-extension closures compose correctly with that
// mechanism's contract.
func supersedeOrAppendNoticeForTest(conversation []agent.Message, notice string) []agent.Message {
	for i, m := range conversation {
		if strings.HasPrefix(m.Content, testTurnBudgetMarker) {
			out := append([]agent.Message(nil), conversation...)
			out[i] = agent.Message{Role: agent.MessageRoleUser, Content: notice}
			return out
		}
	}
	return append(append([]agent.Message(nil), conversation...), agent.Message{Role: agent.MessageRoleUser, Content: notice})
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
	if usage != tokenUsageOf(state) {
		t.Errorf("usage = %+v, want %+v (tokenUsageOf initial state)", usage, tokenUsageOf(state))
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
	wantUsage := tokenUsageOf(initialState).
		Add(tokenUsageOf(responses[0].state)).
		Add(tokenUsageOf(responses[1].state)).
		Add(tokenUsageOf(responses[2].state))
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

	// Exactly one budget-notice-marked message should survive across all
	// extensions (superseded in place, not accumulated).
	var marked []agent.Message
	for _, m := range finalState.Conversation {
		if strings.HasPrefix(m.Content, testTurnBudgetMarker) {
			marked = append(marked, m)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("marked notice messages = %d, want 1; conversation = %+v", len(marked), finalState.Conversation)
	}
	// The surviving message must carry the last extension's numbers: the
	// final call is ext = maxDelegateExtensions-1, so extensionsLeft = 0.
	lastReq := runner.requests[len(runner.requests)-1]
	lastResp := runner.responses[len(runner.responses)-1]
	const wantSuffix = "0 extension(s) remaining"
	if !strings.Contains(marked[0].Content, wantSuffix) {
		t.Errorf("surviving notice = %q, want it to mention %q", marked[0].Content, wantSuffix)
	}
	wantTurns := fmt.Sprintf("used %d of %d turns", lastResp.state.TurnCount, lastReq.Limits.MaxTurns)
	if !strings.Contains(marked[0].Content, wantTurns) {
		t.Errorf("surviving notice = %q, want it to mention %q (last extension's turn counts)", marked[0].Content, wantTurns)
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
			{state: agent.RunState{TokenCount: 30, InputTokens: 30, CacheReadTokens: 300, CacheCreateTokens: 3}, err: fmt.Errorf("provider error")},
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
	wantUsage := tokenUsageOf(initialState).Add(tokenUsageOf(runner.responses[0].state)).Add(tokenUsageOf(runner.responses[1].state))
	if usage != wantUsage {
		t.Errorf("usage = %+v, want %+v (usage through the failed run)", usage, wantUsage)
	}
}

func TestSpawnDelegateAccumulatesExtensionUsage(t *testing.T) {
	calls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		calls++
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, StopReason: agent.StopReasonComplete, TokenCount: 1}, nil
		}
		if calls == 1 {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "read"}}}}, StopReason: agent.StopReasonMaxTurns, TokenCount: 10}, nil
		}
		return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "done"}}, StopReason: agent.StopReasonComplete, TokenCount: 20}, nil
	}}

	result, _, usage, err := SpawnDelegate(context.Background(), Spec{AgentID: "extension-usage", Limits: Limits{MaxTurns: 1}}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	got := result.Value.(Result)
	if got.TokenCount != 31 || usage.OutputTokens != 31 {
		t.Fatalf("output usage = (%d, %d), want (31, 31)", got.TokenCount, usage.OutputTokens)
	}
}

func TestSpawnDelegateAccumulatesErroredExtensionUsage(t *testing.T) {
	calls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		calls++
		if calls == 1 {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "read"}}}}, StopReason: agent.StopReasonMaxTurns, TokenCount: 10}, nil
		}
		return agent.RunState{TokenCount: 20, InputTokens: 2, CacheReadTokens: 3, CacheCreateTokens: 4}, errors.New("extension failed")
	}}

	result, _, usage, err := SpawnDelegate(context.Background(), Spec{AgentID: "errored-extension", Limits: Limits{MaxTurns: 1}}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	got := result.Value.(Result)
	if got.TokenCount != 30 || usage.OutputTokens != 30 || got.InputTokens != 2 || got.CacheReadTokens != 3 || got.CacheCreateTokens != 4 {
		t.Fatalf("errored extension usage = result(%d,%d,%d,%d), usage output=%d; want (30,2,3,4), 30", got.TokenCount, got.InputTokens, got.CacheReadTokens, got.CacheCreateTokens, usage.OutputTokens)
	}
}

func TestSpawnDelegateAccumulatesSummaryUsage(t *testing.T) {
	calls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		calls++
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, StopReason: agent.StopReasonComplete, TokenCount: 7, InputTokens: 11, CacheReadTokens: 13, CacheCreateTokens: 17}, nil
		}
		return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "done"}}, StopReason: agent.StopReasonComplete, TokenCount: 5, InputTokens: 2, CacheReadTokens: 3, CacheCreateTokens: 4}, nil
	}}

	result, _, usage, err := SpawnDelegate(context.Background(), Spec{AgentID: "summary-usage"}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	got := result.Value.(Result)
	if got.TokenCount != 12 || got.InputTokens != 13 || got.CacheReadTokens != 16 || got.CacheCreateTokens != 21 {
		t.Fatalf("summary usage result = (%d,%d,%d,%d), want (12,13,16,21)", got.TokenCount, got.InputTokens, got.CacheReadTokens, got.CacheCreateTokens)
	}
	if usage != (TokenUsage{OutputTokens: 12, InputTokens: 13, CacheReadTokens: 16, CacheCreateTokens: 21}) {
		t.Fatalf("summary usage = %+v, want cumulative usage", usage)
	}
}

func TestSpawnDelegate_SummaryTurnGetsNoTurnBudgetNotice(t *testing.T) {
	var capturedSummaryReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			capturedSummaryReq = req
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, StopReason: agent.StopReasonComplete}, nil
		}
		return successRunState(), nil
	}}

	_, _, _, err := SpawnDelegate(context.Background(), Spec{AgentID: "summary-no-notice"}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if capturedSummaryReq.TurnBudgetNotice != nil {
		t.Fatal("expected the summarisation turn's request to carry a nil TurnBudgetNotice")
	}
}

func TestTurnBudgetNoticeFunc(t *testing.T) {
	tests := []struct {
		extensionsLeft int
	}{
		{extensionsLeft: 3},
		{extensionsLeft: 1},
		{extensionsLeft: 0},
	}
	for _, tt := range tests {
		notice := turnBudgetNoticeFunc(tt.extensionsLeft)
		text := notice(21, 30)
		wantExt := fmt.Sprintf("%d extension(s) remaining", tt.extensionsLeft)
		if !strings.Contains(text, wantExt) {
			t.Errorf("extensionsLeft=%d: text = %q, want to contain %q", tt.extensionsLeft, text, wantExt)
		}
		if !strings.Contains(text, "used 21 of 30 turns (9 remaining)") {
			t.Errorf("extensionsLeft=%d: text = %q, want turn counts", tt.extensionsLeft, text)
		}
	}
}

func TestSpawnDelegate_SetsInitialTurnBudgetNotice(t *testing.T) {
	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, StopReason: agent.StopReasonComplete}, nil
		}
		capturedReq = req
		return successRunState(), nil
	}}

	_, _, _, err := SpawnDelegate(context.Background(), Spec{AgentID: "initial-notice"}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if capturedReq.TurnBudgetNotice == nil {
		t.Fatal("expected TurnBudgetNotice to be set on the initial run")
	}
	text := capturedReq.TurnBudgetNotice(21, 30)
	wantExt := fmt.Sprintf("%d extension(s) remaining", maxDelegateExtensions)
	if !strings.Contains(text, wantExt) {
		t.Errorf("initial notice = %q, want to contain %q", text, wantExt)
	}
}

func TestSpawnDelegate_DoesNotEmitStartedEvent(t *testing.T) {
	sink := &collectingSink{}
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}
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

func TestTruncateUTF8_TruncatesAt4000Chars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{
			name:     "string shorter than limit passes through",
			input:    "short text",
			maxRunes: delegateRetentionSummaryMaxRunes,
			want:     "short text",
		},
		{
			name:     "string exactly at limit passes through",
			input:    strings.Repeat("x", delegateRetentionSummaryMaxRunes),
			maxRunes: delegateRetentionSummaryMaxRunes,
			want:     strings.Repeat("x", delegateRetentionSummaryMaxRunes),
		},
		{
			name:     "string exceeding limit is truncated with ellipsis",
			input:    strings.Repeat("x", delegateRetentionSummaryMaxRunes+100),
			maxRunes: delegateRetentionSummaryMaxRunes,
			want:     strings.Repeat("x", delegateRetentionSummaryMaxRunes-3) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input)
			if got != tt.want {
				t.Errorf("truncateUTF8() = %d chars, want %d chars", len(got), len(tt.want))
				if len(got) > 100 {
					t.Logf("got truncated to: %s...", got[:100])
					t.Logf("want: %s...", tt.want[:min(100, len(tt.want))])
				}
			}
		})
	}
}
