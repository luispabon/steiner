package agent

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// turnProgressor owns the per-turn progression lifecycle.
type turnProgressor struct {
	runner *Runner
}

func newTurnProgressor(runner *Runner) *turnProgressor {
	return &turnProgressor{runner: runner}
}

// advance handles the compaction path of the turn lifecycle.
// It emits the compaction event, runs compaction, and returns the outcome.
func (p *turnProgressor) advance(ctx context.Context, in turnInput, fit prompt.RequestTokenBudget) turnOutcome {
	return p.handleCompaction(ctx, in, fit)
}

// handleCompaction coordinates compaction when the request does not fit
// the model token budget. It returns a retry outcome on success (the caller
// should re-run the turn with the compacted state) or an error outcome on
// failure.
func (p *turnProgressor) handleCompaction(ctx context.Context, in turnInput, fit prompt.RequestTokenBudget) turnOutcome {
	turn := in.State.TurnCount + 1
	emitCompactionStartedEvent(in.Request.Events, turn)
	state := in.State
	compacted, err := p.runner.compactConversationForBudget(ctx, in.Request, &state, turn, in.CompactionHistory, in.CompactionCount)
	if err != nil {
		return turnOutcome{State: state, Error: err, Stop: true}
	}
	if compacted {
		return turnOutcome{State: state, Retry: true}
	}
	return turnOutcome{
		State: state,
		Error: fmt.Errorf("request exceeds context window: %s", fit.String()),
		Stop:  true,
	}
}

// prepareTurn assembles the prompt, constructs the chat request, and fits it
// against the model token budget. Diagnostics are emitted through the request
// event sink.
func prepareTurn(ctx context.Context, in turnInput) (prompt.Assembly, provider.ChatRequest, prompt.RequestTokenBudget, error) {
	turn := in.State.TurnCount + 1
	assembly, err := prompt.Assemble(ctx, assemblyOptions(in.BasePrompt, in.State))
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitAssemblyDiagnostics(in.Request.Events, in.Request.Prompt, turn, assembly)

	chatRequest := provider.ChatRequest{
		Model:       in.Request.Model,
		Messages:    assembly.Messages,
		Tools:       cloneProviderTools(in.Request.Tools),
		ExtraParams: in.Request.ExtraParams,
		MaxTokens:   in.Request.MaxTokens,
	}

	fit, err := in.Request.ModelBudget.FitRequest(ctx, chatRequest)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitRequestTokenDiagnostic(in.Request.Events, turn, fit, !fit.Fits)
	return assembly, chatRequest, fit, nil
}
