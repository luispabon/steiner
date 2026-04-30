package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any) (any, error)
}

// RunRequest carries all parameters needed for a single agent run.
type RunRequest struct {
	Provider    provider.Provider
	Executor    ToolExecutor
	Tools       []provider.ToolSpec
	Prompt      prompt.AssemblyOptions
	ModelBudget prompt.ModelTokenBudget
	Model       string
	ExtraParams map[string]any
	MaxTokens   *int
	Limits      Limits
	Events      output.EventSink

	// StreamingPreferred signals whether the caller wants streaming responses.
	// When false, ChatCompletion is tried first and streaming is used only as a
	// fallback. Interactive mode sets this to true; --exec defaults to false.
	StreamingPreferred bool
}

type Runner struct{}

// NewRunner creates a new agent runner with default limits and state.
func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (RunState, error) {
	conversation := fromProviderMessages(req.Prompt.Conversation)
	state := RunState{
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
		Context:      fromPromptContext(req.Prompt.ContextState),
	}
	if req.Provider == nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("provider is required")
	}
	if req.Executor == nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("tool executor is required")
	}
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, nil
	}

	basePrompt := req.Prompt
	basePrompt.Conversation = nil
	compactionHistory := map[string]bool{}
	compactionCount := 0

	for {
		if err := ctx.Err(); err != nil {
			state.StopReason = StopReasonCancelled
			emitStop(req.Events, state, nil)
			return state, nil
		}

		if req.Limits.MaxTurns > 0 && state.TurnCount >= req.Limits.MaxTurns {
			state.StopReason = StopReasonMaxTurns
			emitStop(req.Events, state, nil)
			return state, nil
		}
		if req.Limits.MaxTokens > 0 && state.TokenCount >= req.Limits.MaxTokens {
			state.StopReason = StopReasonMaxTokens
			emitStop(req.Events, state, nil)
			return state, nil
		}

		var err error
		state, err = r.runTurn(ctx, req, state, basePrompt, compactionHistory, &compactionCount)
		if err != nil {
			return state, err
		}
		if state.StopReason != "" {
			return state, nil
		}
	}
}

func (r *Runner) runTurn(ctx context.Context, req RunRequest, state RunState, basePrompt prompt.AssemblyOptions, compactionHistory map[string]bool, compactionCount *int) (RunState, error) {
	in := turnInput{
		Request:           req,
		State:             state,
		BasePrompt:        basePrompt,
		CompactionHistory: compactionHistory,
		CompactionCount:   compactionCount,
	}
	assembly, chatRequest, fit, err := prepareTurn(ctx, in)
	if err != nil {
		return handleRunError(ctx, req.Events, state, err)
	}
	if !fit.Fits {
		p := newTurnProgressor(r)
		outcome := p.advance(ctx, in, fit)
		if outcome.Error != nil {
			return handleRunError(ctx, req.Events, outcome.State, outcome.Error)
		}
		return outcome.State, nil
	}

	p := newTurnProgressor(r)
	outcome := p.executeModelCall(ctx, in, assembly, chatRequest)
	if outcome.Error != nil {
		return outcome.State, outcome.Error
	}
	if outcome.Stop {
		return outcome.State, nil
	}
	// Tool calls present — delegate to turnProgressor
	in.State = outcome.State
	toolOutcome := p.executeToolCalls(ctx, in, *outcome.Response)
	if toolOutcome.Error != nil {
		return toolOutcome.State, toolOutcome.Error
	}
	if toolOutcome.Stop {
		return toolOutcome.State, nil
	}
	return toolOutcome.State, nil
}

func handleRunError(ctx context.Context, events output.EventSink, state RunState, err error) (RunState, error) {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitStop(events, cancelled, nil)
		return cancelled, nil
	}
	state.StopReason = StopReasonError
	emitStop(events, state, err)
	return state, err
}

func formatToolError(err error) string {
	var tee *tool.ToolExecutionError
	if ok := errors.As(err, &tee); ok {
		details := map[string]any{
			"exit_code": tee.ExitCode,
			"stdout":    tee.Output.Stdout.Preview,
			"stderr":    tee.Output.Stderr.Preview,
		}
		if tee.Output.Stdout.Summary() != "" || tee.Output.Stderr.Summary() != "" {
			details["stdout"] = tee.Output.Stdout.Summary()
			details["stderr"] = tee.Output.Stderr.Summary()
		}
		envelope := tool.JSONEnvelope{
			OK: false,
			Error: &tool.JSONEnvelopeError{
				Kind:    tee.Kind,
				Message: tee.Message,
				Details: details,
			},
		}
		data, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Sprintf(`{"ok":false,"error":{"kind":"%s","message":"%s"}}`, tee.Kind, tee.Message)
		}
		return string(data)
	}
	envelope := tool.JSONEnvelope{
		OK: false,
		Error: &tool.JSONEnvelopeError{
			Kind:    "tool_error",
			Message: err.Error(),
		},
	}
	data, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return fmt.Sprintf(`{"ok":false,"error":{"kind":"tool_error","message":"%s"}}`, err.Error())
	}
	return string(data)
}
