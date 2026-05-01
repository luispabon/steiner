package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestPrepareTurn_SuccessfulFit(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	req := RunRequest{
		Model: "test-model",
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Limits: Limits{MaxTurns: 2},
		Events: output.NoopSink{},
	}
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	assembly, chatRequest, fit, err := prepareTurn(context.Background(), in)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	if !fit.Fits {
		t.Fatalf("fit.Fits = false, want true")
	}
	if len(assembly.Messages) == 0 {
		t.Fatalf("assembly has no messages")
	}
	if chatRequest.Model != "test-model" {
		t.Fatalf("chatRequest.Model = %q, want %q", chatRequest.Model, "test-model")
	}
}

func TestPrepareTurn_FitFailure(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello world this message is too long to fit"}},
	}
	req := RunRequest{
		Model: "test-model",
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello world this message is too long to fit"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         1,
			MaxCompletionTokens: 1,
		},
		Limits: Limits{MaxTurns: 2},
		Events: output.NoopSink{},
	}
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	_, _, fit, err := prepareTurn(context.Background(), in)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	if fit.Fits {
		t.Fatalf("fit.Fits = true, want false")
	}
}

func TestHandleCompaction_CompactsAndRetries(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "compacted summary of the conversation",
				},
				FinishReason: "stop",
			},
		},
	}

	state := RunState{
		TurnCount: 0,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		}),
	}

	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{}, nil
		}},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         10,
			MaxCompletionTokens: 5,
			SummaryMaxTokens:    5,
		},
		Events: output.NoopSink{},
	}

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	fit := prompt.RequestTokenBudget{
		ContextSize: 10,
		TotalTokens: 100,
		Fits:        false,
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.handleCompaction(context.Background(), in, fit)

	if outcome.Error != nil {
		t.Fatalf("handleCompaction() error = %v", outcome.Error)
	}
	if !outcome.Retry {
		t.Fatalf("outcome.Retry = false, want true")
	}
	if outcome.Stop {
		t.Fatalf("outcome.Stop = true, want false")
	}
}

func TestHandleCompaction_ProviderError(t *testing.T) {
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, fmt.Errorf("compaction provider unavailable")
		},
	}

	state := RunState{
		TurnCount: 0,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		}),
	}

	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{}, nil
		}},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
			SummaryMaxTokens:    256,
		},
		Events: output.NoopSink{},
	}

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	fit := prompt.RequestTokenBudget{
		ContextSize: 4096,
		TotalTokens: 100,
		Fits:        false,
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.handleCompaction(context.Background(), in, fit)

	if outcome.Error == nil {
		t.Fatal("handleCompaction() error = nil, want error")
	}
	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
}

func TestHandleCompaction_NoCandidate(t *testing.T) {
	state := RunState{
		TurnCount: 0,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "hello"},
		}),
	}

	req := RunRequest{
		Provider: &fakeProvider{},
		Executor: &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{}, nil
		}},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Events: output.NoopSink{},
	}

	key := compactionCandidateKey(ConversationCandidate{
		GenerationID: 1,
		View:         ConversationViewFull,
	})
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{key: true},
		CompactionCount:   new(int),
	}

	fit := prompt.RequestTokenBudget{
		ContextSize: 4096,
		TotalTokens: 100,
		Fits:        false,
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.handleCompaction(context.Background(), in, fit)

	if outcome.Error == nil {
		t.Fatal("handleCompaction() error = nil, want error")
	}
	if !strings.Contains(outcome.Error.Error(), "exceeds context window") {
		t.Fatalf("outcome.Error = %q, want 'exceeds context window'", outcome.Error)
	}
	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
}

func TestPrepareTurn_AssemblyErrorPropagates(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	// Set a project context budget of 0 bytes to avoid assembly errors.
	req := RunRequest{
		Model: "test-model",
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Limits: Limits{MaxTurns: 2},
		Events: output.NoopSink{},
	}
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	// A canceled context should cause prompt.Assemble to fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := prepareTurn(ctx, in)
	if err == nil {
		t.Fatal("prepareTurn() error = nil, want error from canceled context")
	}
}

func TestAdvance_AssistantOnlyStops(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "hello",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2},
			},
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hi"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(context.Background(), in)

	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonComplete {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonComplete)
	}
	if outcome.State.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", outcome.State.TurnCount)
	}

	got := eventTypes(events)
	wantSequence := []string{
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeTurnFinished,
		output.EventTypeStopReason,
	}
	if !containsSequence(got, wantSequence) {
		t.Fatalf("event types = %v, want sequence %v", got, wantSequence)
	}
}

func TestAdvance_ToolCallsThenContinue(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "test.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return map[string]any{"contents": "hello"}, nil
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hi"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(context.Background(), in)

	if outcome.Stop {
		t.Fatalf("outcome.Stop = true, want false")
	}
	if outcome.Retry {
		t.Fatalf("outcome.Retry = true, want false")
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}
	if outcome.State.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", outcome.State.TurnCount)
	}
	if len(outcome.State.Conversation) < 3 {
		t.Fatalf("conversation has %d messages, want at least 3 (user + assistant + tool)", len(outcome.State.Conversation))
	}
	lastMsg := outcome.State.Conversation[len(outcome.State.Conversation)-1]
	if lastMsg.Role != MessageRoleTool {
		t.Fatalf("last message role = %q, want %q", lastMsg.Role, MessageRoleTool)
	}
	if lastMsg.ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want %q", lastMsg.ToolCallID, "call_1")
	}
	if lastMsg.Content != `{"contents":"hello"}` {
		t.Fatalf("tool result content = %q, want %q", lastMsg.Content, `{"contents":"hello"}`)
	}

	wantSequence := []string{
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished,
	}
	if !containsSequence(eventTypes(events), wantSequence) {
		t.Fatalf("event types = %v, want sequence %v", eventTypes(events), wantSequence)
	}
}

func TestAdvance_ModelCallCancellation(t *testing.T) {
	providerStub := &fakeProvider{
		chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			<-ctx.Done()
			return provider.ChatResponse{}, ctx.Err()
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hi"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(ctx, in)

	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonCancelled)
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}

	got := eventTypes(events)
	if !containsString(got, output.EventTypeStopReason) {
		t.Fatalf("event types = %v, want stop_reason", got)
	}
}

func TestAdvance_ToolCallCancellation(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "test.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			cancelFunc := ctx.Value(cancelContextKey{}).(context.CancelFunc)
			cancelFunc()
			return nil, ctx.Err()
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hi"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, cancelContextKey{}, context.CancelFunc(cancel))

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(ctx, in)

	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonCancelled)
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}

	got := eventTypes(events)
	wantSequence := []string{
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeStopReason,
	}
	if !containsSequence(got, wantSequence) {
		t.Fatalf("event types = %v, want sequence %v", got, wantSequence)
	}
}

func TestAdvance_ToolCallFailure(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "test.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return nil, fmt.Errorf("execution failed")
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hi"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(context.Background(), in)

	if outcome.Stop {
		t.Fatalf("outcome.Stop = true, want false")
	}
	if outcome.Retry {
		t.Fatalf("outcome.Retry = true, want false")
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}
	if outcome.State.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", outcome.State.TurnCount)
	}

	// The tool error should be reflected in the conversation as a tool message.
	if len(outcome.State.Conversation) < 3 {
		t.Fatalf("conversation has %d messages, want at least 3", len(outcome.State.Conversation))
	}
	lastMsg := outcome.State.Conversation[len(outcome.State.Conversation)-1]
	if lastMsg.Role != MessageRoleTool {
		t.Fatalf("last message role = %q, want %q", lastMsg.Role, MessageRoleTool)
	}
	if lastMsg.ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want %q", lastMsg.ToolCallID, "call_1")
	}
	if !strings.Contains(lastMsg.Content, "execution failed") {
		t.Fatalf("tool message content = %q, want it to contain %q", lastMsg.Content, "execution failed")
	}

	wantSequence := []string{
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished,
	}
	if !containsSequence(eventTypes(events), wantSequence) {
		t.Fatalf("event types = %v, want sequence %v", eventTypes(events), wantSequence)
	}
}

func TestAdvance_FitFailureThenCompaction(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "compacted summary of the conversation",
				},
				FinishReason: "stop",
			},
		},
	}

	state := RunState{
		TurnCount: 0,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "world"},
		}),
	}

	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{}, nil
		}},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         1,
			MaxCompletionTokens: 1,
			SummaryMaxTokens:    1,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hello"},
				{Role: provider.MessageRoleAssistant, Content: "world"},
			},
		},
		Limits: Limits{MaxTurns: 2},
	}

	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{},
		CompactionCount:   new(int),
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(context.Background(), in)

	// Compaction should signal retry: the outer loop should retry the turn
	// with the (skipped candidate) state.
	if !outcome.Retry {
		t.Fatalf("outcome.Retry = false, want true")
	}
	if outcome.Stop {
		t.Fatalf("outcome.Stop = true, want false")
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}
}

func TestAdvance_FitFailureNoCompactionCandidate(t *testing.T) {
	state := RunState{
		TurnCount: 0,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "hello"},
		}),
	}

	req := RunRequest{
		Provider: &fakeProvider{},
		Executor: &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{}, nil
		}},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         1,
			MaxCompletionTokens: 1,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hello"},
			},
		},
		Limits: Limits{MaxTurns: 2},
	}

	key := compactionCandidateKey(ConversationCandidate{
		GenerationID: 1,
		View:         ConversationViewFull,
	})
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        prompt.AssemblyOptions{},
		CompactionHistory: map[string]bool{key: true},
		CompactionCount:   new(int),
	}

	runner := NewRunner()
	p := newTurnProgressor(runner)
	outcome := p.advance(context.Background(), in)

	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.Error == nil {
		t.Fatal("outcome.Error = nil, want error")
	}
	if !strings.Contains(outcome.Error.Error(), "exceeds context window") {
		t.Fatalf("outcome.Error = %q, want 'exceeds context window'", outcome.Error)
	}
	if outcome.State.StopReason != StopReasonError {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonError)
	}
}
