package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// compactionStageRetention strips skill blocks and elides old tool output in the
// retained messages a stage stores, so the emitted RetainedMessages are the
// messages actually persisted and the elision markers are what the new
// generation freezes. recomputeFit refits the tail when it is not the one the
// stage already fit.
func compactionStageRetention(ctx context.Context, req RunRequest, state RunState, recomputeFit bool, fit prompt.RequestTokenBudget, retained []Message) ([]Message, prompt.RequestTokenBudget, error) {
	retained, elided := buildCompactedRetention(ctx, req, state, cloneMessages(retained))
	if !elided && !recomputeFit {
		return retained, fit, nil
	}
	// The stage's token attribution measures the tail the generation keeps.
	refit, err := fitConversationState(ctx, req, state.WithConversation(retained))
	if err != nil {
		return nil, prompt.RequestTokenBudget{}, err
	}
	return retained, refit, nil
}

// stageTruncationMarker returns the marker a truncated summary carries. It
// follows the history note and precedes the request block.
func stageTruncationMarker(truncated bool) string {
	if !truncated {
		return ""
	}
	return "\n\n" + summaryTruncatedMarker
}

func fitConversationState(ctx context.Context, req RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
	basePrompt := prepareBasePrompt(req)
	assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state))
	if err != nil {
		return prompt.RequestTokenBudget{}, err
	}

	// Reasoning is intentionally omitted here: this request is only used to
	// estimate token counts against the budget (FitRequest), never sent to a
	// provider, and reasoning effort does not affect token estimation.
	chatRequest := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    assembly.Messages,
		Tools:       provider.CloneTools(req.Tools),
		Params:      req.ResolvedModel.Params,
		ExtraParams: req.ResolvedModel.ExtraParams,
	}
	chatRequest = applyPromptSuffix(req.ResolvedModel.PromptSuffix, chatRequest)
	return req.ModelBudget.FitRequest(ctx, chatRequest)
}
