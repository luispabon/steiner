package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestPrepareTurn_SuccessfulFit(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Limits:         Limits{MaxTurns: 2},
		Events:         output.NoopSink{},
		PromptCacheKey: "test-session-key",
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	assembly, chatRequest, fit, err := p.prepareTurn(context.Background(), state)
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
	if chatRequest.PromptCacheKey == "" {
		t.Fatal("chatRequest.PromptCacheKey = empty, want stable session key")
	}
	if chatRequest.MaxTokens != nil {
		t.Fatalf("chatRequest.MaxTokens = %v, want nil for normal turns", *chatRequest.MaxTokens)
	}
	if fit.ShouldCompact {
		t.Fatal("fit.ShouldCompact = true, want false for a small prompt")
	}
}

func TestPrepareTurn_PromptCacheKeyStableAcrossCalls(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Limits:         Limits{MaxTurns: 2},
		Events:         output.NoopSink{},
		PromptCacheKey: "test-session-key",
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	_, firstRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() first call error = %v", err)
	}
	_, secondRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() second call error = %v", err)
	}
	if firstRequest.PromptCacheKey == "" {
		t.Fatal("firstRequest.PromptCacheKey = empty, want stable session key")
	}
	if secondRequest.PromptCacheKey == "" {
		t.Fatal("secondRequest.PromptCacheKey = empty, want stable session key")
	}
	if firstRequest.PromptCacheKey != secondRequest.PromptCacheKey {
		t.Fatalf("prompt cache key changed between calls: %q != %q", firstRequest.PromptCacheKey, secondRequest.PromptCacheKey)
	}
}

func TestPrepareTurn_FitFailure(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello world this message is too long to fit"}},
	}
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
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
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	_, _, fit, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}

	if fit.Fits {
		t.Fatalf("fit.Fits = true, want false")
	}
	if !fit.ShouldCompact {
		t.Fatal("fit.ShouldCompact = false, want true when the hard prompt limit is exceeded")
	}
}

func TestPrepareTurn_RequestsCompactionAtSeventyPercentUsage(t *testing.T) {
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize: 4096,
		},
		Limits: Limits{MaxTurns: 2},
		Events: output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	_, _, fit, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() baseline error = %v", err)
	}

	threshReq := req
	threshReq.ModelBudget.ContextSize = int(float64(fit.EstimatedPromptTokens) / 0.70)
	p2 := newTurnProgressor(threshReq, prompt.AssemblyOptions{}, nil)

	_, _, thresholdFit, err := p2.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() threshold error = %v", err)
	}
	if !thresholdFit.ShouldCompact {
		t.Fatalf("fit.ShouldCompact = false, want true at >=70%% prompt usage")
	}
	if !thresholdFit.Fits {
		t.Fatalf("fit.Fits = false, want true when the prompt still hard-fits")
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

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, func(_ context.Context, _ RunRequest, _ *RunState, _ int, _ *prompt.RequestTokenBudget, _ map[string]bool, _ *int) (bool, error) {
		return false, fmt.Errorf("compaction cannot solve this request: no valid candidates")
	})

	fit := prompt.RequestTokenBudget{
		ContextSize: 10,
		TotalTokens: 100,
		Fits:        false,
	}

	outcome := p.handleCompaction(context.Background(), state, fit)

	if outcome.Error == nil {
		t.Fatal("handleCompaction() error = nil, want non-nil")
	}
	if !strings.Contains(outcome.Error.Error(), "compaction cannot solve this request") {
		t.Fatalf("handleCompaction() error = %q, want irreducible compaction failure", outcome.Error)
	}
	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
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

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, func(_ context.Context, _ RunRequest, _ *RunState, _ int, _ *prompt.RequestTokenBudget, _ map[string]bool, _ *int) (bool, error) {
		return false, fmt.Errorf("compaction provider unavailable")
	})

	fit := prompt.RequestTokenBudget{
		ContextSize: 4096,
		TotalTokens: 100,
		Fits:        false,
	}

	outcome := p.handleCompaction(context.Background(), state, fit)

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
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, func(_ context.Context, _ RunRequest, _ *RunState, _ int, _ *prompt.RequestTokenBudget, _ map[string]bool, _ *int) (bool, error) {
		return false, nil
	})
	p.compactionHistory[key] = true

	fit := prompt.RequestTokenBudget{
		ContextSize: 4096,
		TotalTokens: 100,
		Fits:        false,
	}

	outcome := p.handleCompaction(context.Background(), state, fit)

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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
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
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	// A canceled context should cause prompt.Assemble to fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := p.prepareTurn(ctx, state)
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
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)

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
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)

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
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := p.advance(ctx, state)

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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, _ string, _ map[string]any) (any, error) {
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)

	outcome := p.advance(ctx, state)

	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonCancelled)
	}
	if outcome.Error != nil {
		t.Fatalf("outcome.Error = %v, want nil", outcome.Error)
	}
	if len(outcome.State.Conversation) != 1 {
		t.Fatalf("len(outcome.State.Conversation) = %d, want 1", len(outcome.State.Conversation))
	}
	if got := outcome.State.Conversation[0]; got.Role != MessageRoleUser || got.Content != "hi" {
		t.Fatalf("cancelled conversation[0] = %#v, want original user message only", got)
	}
	if got := outcome.State.Lineage.FullMessages(); len(got) != 1 || got[0].Role != MessageRoleUser {
		t.Fatalf("cancelled lineage = %#v, want replay-safe lineage without empty assistant", got)
	}

	got := eventTypes(events)
	wantSequence := []string{
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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)

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
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
	}
	if !containsSequence(eventTypes(events), wantSequence) {
		t.Fatalf("event types = %v, want sequence %v", eventTypes(events), wantSequence)
	}
}

func TestAdvance_WorkflowHandoffAcceptedStopsWithoutToolResult(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "workflow_handoff", Arguments: map[string]any{"next": "implement"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.WorkflowHandoffAccepted{
				Transition: tool.WorkflowHandoffTransition{
					Next:   "implement",
					Target: ".steiner/plans/step-2",
				},
			}, nil
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
	}
	var events []output.Event
	req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)

	if !outcome.Stop {
		t.Fatal("outcome.Stop = false, want true")
	}
	if got, want := outcome.State.StopReason, StopReasonWorkflowHandoff; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if outcome.State.WorkflowHandoff == nil {
		t.Fatal("WorkflowHandoff = nil, want transition")
	}
	if got := outcome.State.WorkflowHandoff.Target; got != ".steiner/plans/step-2" {
		t.Fatalf("WorkflowHandoff.Target = %q, want .steiner/plans/step-2", got)
	}
	if len(outcome.State.Conversation) != 2 {
		t.Fatalf("conversation length = %d, want 2 (user + assistant tool call only)", len(outcome.State.Conversation))
	}
	if got := outcome.State.Conversation[len(outcome.State.Conversation)-1].Role; got != MessageRoleAssistant {
		t.Fatalf("last role = %q, want assistant", got)
	}
	wantSequence := []string{
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeStopReason,
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

	compactFn := func(_ context.Context, _ RunRequest, _ *RunState, _ int, _ *prompt.RequestTokenBudget, _ map[string]bool, _ *int) (bool, error) {
		return false, fmt.Errorf("compaction cannot solve this request: no valid candidates")
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, compactFn)
	outcome := p.advance(context.Background(), state)

	// Compaction should signal retry: the outer loop should retry the turn
	// with the (skipped candidate) state.
	if outcome.Retry {
		t.Fatalf("outcome.Retry = true, want false")
	}
	if !outcome.Stop {
		t.Fatalf("outcome.Stop = false, want true")
	}
	if outcome.Error == nil {
		t.Fatal("outcome.Error = nil, want error")
	}
	if !strings.Contains(outcome.Error.Error(), "compaction cannot solve this request") {
		t.Fatalf("outcome.Error = %q, want irreducible compaction failure", outcome.Error)
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
	compactFn := func(_ context.Context, _ RunRequest, _ *RunState, _ int, _ *prompt.RequestTokenBudget, _ map[string]bool, _ *int) (bool, error) {
		return false, nil
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, compactFn)
	p.compactionHistory[key] = true
	outcome := p.advance(context.Background(), state)

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

func TestAdvance_DetectedReasoningEchoBack_AssistantOnlyTurn(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:             provider.MessageRoleAssistant,
					Content:          "final answer",
					ReasoningContent: "step by step thinking",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
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
			Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model", ReasoningEchoBack: false},
		Limits:        Limits{MaxTurns: 2},
		Events:        output.NoopSink{},
	}

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)

	if !p.request.ResolvedModel.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack = false, want true when model returns reasoning_content")
	}
	if !outcome.Stop {
		t.Fatal("outcome.Stop = false, want true for assistant-only turn")
	}
}

func TestAdvance_DetectedReasoningEchoBack_NotSetWhenAlreadyEnabled(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:             provider.MessageRoleAssistant,
					Content:          "final answer",
					ReasoningContent: "step by step thinking",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
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
			Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		// ReasoningEchoBack already enabled — flag must NOT be set.
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model", ReasoningEchoBack: true},
		Limits:        Limits{MaxTurns: 2},
		Events:        output.NoopSink{},
	}

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	_ = p.advance(context.Background(), state)

	if !p.request.ResolvedModel.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack was toggled off, want it to remain true")
	}
}

func TestPrepareTurn_SetsIncludeEmptyReasoningFromResolvedModel(t *testing.T) {
	tests := []struct {
		name              string
		reasoningEchoBack bool
		wantIncludeEmpty  bool
	}{
		{
			name:              "sets IncludeEmptyReasoning when ReasoningEchoBack is true",
			reasoningEchoBack: true,
			wantIncludeEmpty:  true,
		},
		{
			name:              "clears IncludeEmptyReasoning when ReasoningEchoBack is false",
			reasoningEchoBack: false,
			wantIncludeEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := RunState{
				TurnCount:    0,
				Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
			}
			req := RunRequest{
				ResolvedModel: provider.ResolvedModel{
					BackendModelID:    "test-model",
					ReasoningEchoBack: tt.reasoningEchoBack,
				},
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
			p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

			_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
			if err != nil {
				t.Fatalf("prepareTurn() error = %v", err)
			}
			if chatRequest.IncludeEmptyReasoning != tt.wantIncludeEmpty {
				t.Fatalf("IncludeEmptyReasoning = %v, want %v", chatRequest.IncludeEmptyReasoning, tt.wantIncludeEmpty)
			}
		})
	}
}

func TestPrepareTurn_SetsReasoningFromResolvedModel(t *testing.T) {
	tests := []struct {
		name            string
		effectiveEffort string
		wantReasoning   *provider.ReasoningRequest
	}{
		{
			name:            "nil reasoning when effective effort empty",
			effectiveEffort: "",
			wantReasoning:   nil,
		},
		{
			name:            "reasoning set when effective effort explicit",
			effectiveEffort: "low",
			wantReasoning:   &provider.ReasoningRequest{Effort: "low"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := RunState{
				TurnCount:    0,
				Conversation: []Message{{Role: MessageRoleUser, Content: "hello"}},
			}
			req := RunRequest{
				ResolvedModel: provider.ResolvedModel{
					BackendModelID:           "test-model",
					ReasoningEffectiveEffort: tt.effectiveEffort,
				},
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
			p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

			_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
			if err != nil {
				t.Fatalf("prepareTurn() error = %v", err)
			}
			if tt.wantReasoning == nil {
				if chatRequest.Reasoning != nil {
					t.Fatalf("Reasoning = %+v, want nil", chatRequest.Reasoning)
				}
				return
			}
			if chatRequest.Reasoning == nil || *chatRequest.Reasoning != *tt.wantReasoning {
				t.Fatalf("Reasoning = %+v, want %+v", chatRequest.Reasoning, tt.wantReasoning)
			}
		})
	}
}

func TestConversationSnapshotFromContextReturnsClonedSnapshot(t *testing.T) {
	original := []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}}

	ctx := WithConversationSnapshot(context.Background(), original)
	got, ok := ConversationSnapshotFromContext(ctx)
	if !ok {
		t.Fatal("ConversationSnapshotFromContext() ok = false, want true")
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("ConversationSnapshotFromContext() = %#v, want original content", got)
	}

	got[0].Content = "mutated"
	again, ok := ConversationSnapshotFromContext(ctx)
	if !ok {
		t.Fatal("ConversationSnapshotFromContext() second ok = false, want true")
	}
	if again[0].Content != "hello" {
		t.Fatalf("ConversationSnapshotFromContext() returned shared data %q, want %q", again[0].Content, "hello")
	}
}

func TestExecuteSingleToolCallSetsConversationSnapshot(t *testing.T) {
	var snapshot []provider.Message
	executor := &fakeExecutor{
		execute: func(ctx context.Context, _ string, _ map[string]any) (any, error) {
			var ok bool
			snapshot, ok = ConversationSnapshotFromContext(ctx)
			if !ok {
				t.Fatal("ConversationSnapshotFromContext() ok = false, want true during tool execution")
			}
			return map[string]any{"ok": true}, nil
		},
	}

	state := RunState{
		TurnCount: 1,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "hi"},
			{
				Role:    MessageRoleAssistant,
				Content: "checking the file first",
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Name: "bash",
					Arguments: map[string]any{
						"cmd": "pwd",
					},
				}},
			},
		},
	}
	state.Lineage = newConversationLineage(state.Conversation)

	req := RunRequest{
		Executor:       executor,
		ContextManager: NewContextStateManager(),
		Events:         output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	call := provider.ToolCall{ID: "call-1", Name: "bash", Arguments: map[string]any{"cmd": "pwd"}}
	_, outcome := p.executeSingleToolCall(context.Background(), state, 1, call)
	if outcome.Stop {
		t.Fatalf("executeSingleToolCall() stop = true, want false")
	}

	want := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hi"},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "checking the file first",
		},
	}
	if fmt.Sprintf("%#v", snapshot) != fmt.Sprintf("%#v", want) {
		t.Fatalf("tool snapshot = %#v, want %#v", snapshot, want)
	}
}

func TestAdvance_DetectedReasoningEchoBack_NotSetWhenNoReasoningContent(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "final answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
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
			Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 2},
		Events:        output.NoopSink{},
	}

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	_ = p.advance(context.Background(), state)

	if p.request.ResolvedModel.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack = true, want false when no reasoning_content in response")
	}
}

// TestAdvance_DetectedReasoningEchoBack_PersistsAcrossAdvanceCalls verifies
// that when the model returns reasoning_content on the first advance call
// (which also returns tool calls), the progressor enables ReasoningEchoBack
// and preserves it on the second advance call. The second call's chat request
// must have IncludeEmptyReasoning=true, proving the flag propagates.
func TestAdvance_DetectedReasoningEchoBack_PersistsAcrossAdvanceCalls(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:             provider.MessageRoleAssistant,
					ReasoningContent: "step by step thinking",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "echo hi"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{"output": "hi"}, nil
		},
	}
	state := RunState{
		TurnCount:    0,
		Conversation: []Message{{Role: MessageRoleUser, Content: "run something"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleUser, Content: "run something"}}),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "run something"},
			},
			ProjectContextBudgetBytes: 128,
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model", ReasoningEchoBack: false},
		Limits:        Limits{MaxTurns: 4},
		Tools: []provider.ToolSpec{
			{Type: "function", Function: provider.ToolFunctionSpec{Name: "bash", Description: "run shell", Parameters: map[string]any{"type": "object"}}},
		},
		Events: output.NoopSink{},
	}

	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	// First advance: model returns reasoning_content + tool call.
	// Echo-back should be auto-detected and enabled.
	outcome1 := p.advance(context.Background(), state)

	if !p.request.ResolvedModel.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack = false after first advance, want true")
	}
	if outcome1.Stop {
		t.Fatal("outcome1.Stop = true, want false because turn has tool calls")
	}
	if outcome1.Error != nil {
		t.Fatalf("outcome1.Error = %v, want nil", outcome1.Error)
	}

	state2 := outcome1.State

	// Second advance: assistant-only, stops.
	outcome2 := p.advance(context.Background(), state2)

	if !p.request.ResolvedModel.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack = false after second advance, want true (preserved)")
	}
	if !outcome2.Stop {
		t.Fatal("outcome2.Stop = false, want true for assistant-only turn")
	}
	if outcome2.Error != nil {
		t.Fatalf("outcome2.Error = %v, want nil", outcome2.Error)
	}

	// Verify the second chat request had IncludeEmptyReasoning=true,
	// proving echo-back was enabled for the subsequent turn.
	if len(providerStub.requests) < 2 {
		t.Fatalf("provider got %d requests, want at least 2", len(providerStub.requests))
	}
	if !providerStub.requests[1].IncludeEmptyReasoning {
		t.Fatal("second request IncludeEmptyReasoning = false, want true")
	}
}
func TestStripImagesFromMessages_basic(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "abc123", Width: 100, Height: 200, SizeBytes: 2048},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Images != nil {
		t.Fatalf("Images = %v, want nil", got[0].Images)
	}
	wantContent := "look at this\n[image: 100x200 png 2KB]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
	// Original must not be mutated.
	if msgs[0].Images[0].Data == "" {
		t.Fatal("original message Data was mutated")
	}
}

func TestStripImagesFromMessages_multiple(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "two images",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "aaa", Width: 640, Height: 480, SizeBytes: 50000},
				{MediaType: "image/jpeg", Data: "bbb", Width: 1920, Height: 1080, SizeBytes: 2097152},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if got[0].Images != nil {
		t.Fatal("expected Images to be nil after stripping")
	}
	wantContent := "two images\n[image: 640x480 png 48KB]\n[image: 1920x1080 jpeg 2.0MB]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_noImages(t *testing.T) {
	msgs := []Message{
		{Role: MessageRoleUser, Content: "plain text"},
		{Role: MessageRoleAssistant, Content: "response"},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	for i, m := range got {
		if m.Content != msgs[i].Content {
			t.Fatalf("msg[%d].Content changed: got %q, want %q", i, m.Content, msgs[i].Content)
		}
	}
}

func TestStripImagesFromMessages_preservesContent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleTool,
			Content: "existing tool output",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "data", Width: 0, Height: 0, SizeBytes: 0},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	wantContent := "existing tool output\n[image: ? png]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_alreadyStripped(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "already stripped\n[image: 100x100 png 1KB]",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "", Width: 100, Height: 100, SizeBytes: 1024},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	// No Data to strip, Content must not grow.
	if got[0].Content != msgs[0].Content {
		t.Fatalf("Content changed for already-stripped image: got %q, want %q", got[0].Content, msgs[0].Content)
	}
}

func TestImageBlockPlaceholder_LegacyFormat(t *testing.T) {
	tests := []struct {
		name string
		img  ImageBlock
		want string
	}{
		{
			name: "basic image without size",
			img: ImageBlock{
				MediaType: "image/png",
				Width:     100,
				Height:    200,
				SizeBytes: 0,
			},
			want: "[image: 100x200 png]",
		},
		{
			name: "image with KB size",
			img: ImageBlock{
				MediaType: "image/png",
				Width:     100,
				Height:    200,
				SizeBytes: 2048,
			},
			want: "[image: 100x200 png 2KB]",
		},
		{
			name: "image with MB size",
			img: ImageBlock{
				MediaType: "image/jpeg",
				Width:     1920,
				Height:    1080,
				SizeBytes: 2097152,
			},
			want: "[image: 1920x1080 jpeg 2.0MB]",
		},
		{
			name: "image without dimensions",
			img: ImageBlock{
				MediaType: "image/png",
				SizeBytes: 1024,
			},
			want: "[image: ? png 1KB]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageBlockPlaceholder(tt.img, VisionUnknown, false)
			if got != tt.want {
				t.Fatalf("imageBlockPlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImageBlockPlaceholder_NewFormatWithIDAndFilePath(t *testing.T) {
	tests := []struct {
		name string
		img  ImageBlock
		want string
	}{
		{
			name: "image with ID and file path",
			img: ImageBlock{
				ID:        "img-1",
				FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
				MediaType: "image/png",
				Width:     1920,
				Height:    1080,
				SizeBytes: 456789,
			},
			want: `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use vision tool with image_id "img-1" or read tool to re-examine]`,
		},
		{
			name: "image with ID but no size",
			img: ImageBlock{
				ID:        "img-2",
				FilePath:  "/tmp/test.jpg",
				MediaType: "image/jpeg",
				Width:     640,
				Height:    480,
				SizeBytes: 0,
			},
			want: `[image img-2: /tmp/test.jpg 640x480 jpeg — use vision tool with image_id "img-2" or read tool to re-examine]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageBlockPlaceholder(tt.img, VisionUnknown, false)
			if got != tt.want {
				t.Fatalf("imageBlockPlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripImagesFromMessages_withIDAndFilePath(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Images != nil {
		t.Fatalf("Images = %v, want nil", got[0].Images)
	}
	// Should contain the new format placeholder with the vision tool hint.
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB — use vision tool with image_id "img-1" or read tool to re-examine]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
	// Original must not be mutated.
	if msgs[0].Images[0].Data == "" {
		t.Fatal("original message Data was mutated")
	}
}

func TestImageBlockPlaceholder_VisionCapable(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionCapable, false)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use vision tool with image_id "img-1" or read tool to re-examine]`
	if got != want {
		t.Fatalf("VisionCapable: got %q, want %q", got, want)
	}
}

func TestImageBlockPlaceholder_VisionIncapableWithSubAgent(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionIncapable, true)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use follow_up with the agent_id from the image analysis]`
	if got != want {
		t.Fatalf("VisionIncapable with sub-agent: got %q, want %q", got, want)
	}
}

func TestImageBlockPlaceholder_VisionIncapableWithoutSubAgent(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionIncapable, false)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB]`
	if got != want {
		t.Fatalf("VisionIncapable without sub-agent: got %q, want %q", got, want)
	}
}

func TestStripImagesFromMessages_VisionIncapableWithSubAgent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionIncapable, true)
	if got[0].Images != nil {
		t.Fatalf("Images should be nil")
	}
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB — use follow_up with the agent_id from the image analysis]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("got %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_VisionIncapableWithoutSubAgent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionIncapable, false)
	if got[0].Images != nil {
		t.Fatalf("Images should be nil")
	}
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("got %q, want %q", got[0].Content, wantContent)
	}
}

// TestAdvance_VisionLatchRetryDoesNotPanic drives advance() through the
// unknown-model-400 vision discovery path end to end. Before the fix,
// executeModelCall's {Retry: true, response: nil} outcome fell through
// advance()'s Error/Stop check and dereferenced the nil response, panicking.
// The model here has no prior vision capability derived (as if unlisted in
// models.dev), so the first turn's image-bearing request 400s, latches the
// model incapable, and must retry instead of crashing; the second turn must
// then strip/route the images and complete normally.
func TestAdvance_VisionLatchRetryDoesNotPanic(t *testing.T) {
	vc := NewVisionCapabilities(false)
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			if requestHasImages(req.Messages) {
				return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"}
			}
			return provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			}, nil
		},
	}

	convo := []Message{{
		Role:    MessageRoleUser,
		Content: "look at this",
		Images:  []ImageBlock{{ID: "img-1", MediaType: "image/png", Data: "abc"}},
	}}
	state := RunState{
		TurnCount:    0,
		Conversation: convo,
		Lineage:      newConversationLineage(convo),
	}
	req := RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Limits:             Limits{MaxTurns: 2},
		VisionCapabilities: vc,
		Events:             output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	outcome := p.advance(context.Background(), state)
	if outcome.Error != nil {
		t.Fatalf("first advance() error = %v, want nil", outcome.Error)
	}
	if !outcome.Retry {
		t.Fatalf("first advance() Retry = false, want true")
	}
	if outcome.Stop {
		t.Fatalf("first advance() Stop = true, want false")
	}
	if vc.Get("test-model") != VisionIncapable {
		t.Fatalf("model not latched incapable after first advance()")
	}

	outcome = p.advance(context.Background(), outcome.State)
	if outcome.Error != nil {
		t.Fatalf("second advance() error = %v, want nil", outcome.Error)
	}
	if !outcome.Stop {
		t.Fatalf("second advance() Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonComplete {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonComplete)
	}
}
