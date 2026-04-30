package agent

import (
	"context"
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
