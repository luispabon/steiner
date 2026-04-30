package agent

import (
	"context"

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

// advance runs a single turn by delegating to the existing runner method.
//
// Stage 1: pure delegation with no behavioral changes.
func (p *turnProgressor) advance(ctx context.Context, in turnInput) turnOutcome {
	state, err := p.runner.runTurn(
		ctx,
		in.Request,
		in.State,
		in.BasePrompt,
		in.CompactionHistory,
		in.CompactionCount,
	)
	if err != nil {
		return turnOutcome{State: state, Error: err, Stop: true}
	}
	if state.StopReason != "" {
		return turnOutcome{State: state, Stop: true}
	}
	return turnOutcome{State: state, Retry: true}
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
