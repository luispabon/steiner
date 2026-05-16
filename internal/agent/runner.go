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

// ToolExecutor runs a named tool invocation for the agent loop.
type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any) (any, error)
}

// RunRequest carries all parameters needed for a single agent run.
type RunRequest struct {
	Provider                  provider.Provider
	Executor                  ToolExecutor
	Tools                     []provider.ToolSpec
	Prompt                    prompt.AssemblyOptions
	ModelBudget               prompt.ModelTokenBudget
	Model                     string
	ExtraParams               map[string]any
	MaxTokens                 *int
	Limits                    Limits
	Events                    output.EventSink
	ContextManager            ContextManager
	ThinkingEnabled           bool
	ThinkingDisableMarker     string
	ThinkingScaffoldInference bool
	ThinkingParams            map[string]any

	// StreamingPreferred signals whether the caller wants streaming responses.
	// When false, ChatCompletion is tried first and streaming is used only as a
	// fallback. Interactive mode sets this to true; --exec defaults to false.
	StreamingPreferred bool
}

// Runner executes the main turn loop for an agent run.
type Runner struct{}

// NewRunner creates a new agent runner with default limits and state.
func NewRunner() *Runner {
	return &Runner{}
}

// Run executes req until the loop completes, stops, or fails.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunState, error) {
	req = normalizeRunRequest(req)
	state := initializeRunState(req)
	if validated, done, err := validateRunRequest(ctx, req, state); err != nil {
		return validated, err
	} else if done {
		return validated, nil
	}

	var err error
	state, err = postIngestionState(ctx, req, state)
	if err != nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("post ingestion: %w", err)
	}

	basePrompt := prepareBasePrompt(req)
	compactionHistory := map[string]bool{}
	compactionCount := 0
	p := newTurnProgressor(r)

	for {
		if stopped, done := stopRunBeforeTurn(ctx, req, state); done {
			return stopped, nil
		}

		turnCtx := ctx
		var cancel context.CancelFunc
		if req.Limits.TurnTimeout > 0 {
			turnCtx, cancel = context.WithTimeout(ctx, req.Limits.TurnTimeout)
		}
		in := turnInput{
			Request:           req,
			State:             state,
			BasePrompt:        basePrompt,
			CompactionHistory: compactionHistory,
			CompactionCount:   &compactionCount,
		}
		outcome := p.advance(turnCtx, in)
		if cancel != nil {
			cancel()
		}
		state = outcome.State
		if outcome.Error != nil {
			return state, outcome.Error
		}
		if outcome.Stop {
			return state, nil
		}
	}
}

func normalizeRunRequest(req RunRequest) RunRequest {
	if req.ContextManager == nil {
		req.ContextManager = &NaiveContextManager{}
	}
	if setter, ok := req.ContextManager.(EventSinkSetter); ok {
		setter.SetEventSink(req.Events)
	}
	return req
}

func initializeRunState(req RunRequest) RunState {
	conversation := fromProviderMessages(req.Prompt.Conversation)
	state := RunState{
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
		Context:      fromPromptContext(req.Prompt.ContextState),
	}
	state.TurnCount = initialConversationTurnCount(conversation)
	return state
}

func validateRunRequest(ctx context.Context, req RunRequest, state RunState) (RunState, bool, error) {
	if req.Provider == nil {
		state.StopReason = StopReasonError
		return state, false, fmt.Errorf("provider is required")
	}
	if req.Executor == nil {
		state.StopReason = StopReasonError
		return state, false, fmt.Errorf("tool executor is required")
	}
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, true, nil
	}
	return state, false, nil
}

func postIngestionState(ctx context.Context, req RunRequest, state RunState) (RunState, error) {
	return req.ContextManager.PostIngestion(ctx, state)
}

func prepareBasePrompt(req RunRequest) prompt.AssemblyOptions {
	basePrompt := req.Prompt
	basePrompt.Conversation = nil
	// Cache the system preamble once per session so every turn sends the
	// byte-identical string, preventing KV cache busting on local servers.
	if preambler, ok := req.ContextManager.(PreambleProvider); ok {
		basePrompt.CachedPreamble = preambler.CachedSystemPreamble(
			basePrompt.PromptOverrides.System,
			basePrompt.ScratchpadEnabled,
			basePrompt.DelegationEnabled,
		)
	}
	return basePrompt
}

func stopRunBeforeTurn(ctx context.Context, req RunRequest, state RunState) (RunState, bool) {
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, true
	}
	if req.Limits.MaxTurns > 0 && state.TurnCount >= req.Limits.MaxTurns {
		state.StopReason = StopReasonMaxTurns
		emitStop(req.Events, state, nil)
		return state, true
	}
	if req.Limits.MaxTokens > 0 && state.TokenCount >= req.Limits.MaxTokens {
		state.StopReason = StopReasonMaxTokens
		emitStop(req.Events, state, nil)
		return state, true
	}
	return state, false
}

func initialConversationTurnCount(messages []Message) int {
	maxTurn := 0
	for _, message := range messages {
		if message.Turn > maxTurn {
			maxTurn = message.Turn
		}
	}
	return maxTurn
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
