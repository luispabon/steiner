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
