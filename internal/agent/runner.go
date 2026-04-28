package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any) (any, error)
}

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
	turn := state.TurnCount + 1
	assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state))
	if err != nil {
		return handleRunError(ctx, req.Events, state, err)
	}
	emitAssemblyDiagnostics(req.Events, req.Prompt, turn, assembly)

	chatRequest := provider.ChatRequest{
		Model:       req.Model,
		Messages:    assembly.Messages,
		Tools:       cloneProviderTools(req.Tools),
		ExtraParams: req.ExtraParams,
		MaxTokens:   req.MaxTokens,
	}

	fit, err := req.ModelBudget.FitRequest(ctx, chatRequest)
	if err != nil {
		return handleRunError(ctx, req.Events, state, err)
	}
	emitRequestTokenDiagnostic(req.Events, turn, fit, !fit.Fits)
	if !fit.Fits {
		emitCompactionStartedEvent(req.Events, turn)
		compacted, err := r.compactConversationForBudget(ctx, req, &state, turn, compactionHistory, compactionCount)
		if err != nil {
			return handleRunError(ctx, req.Events, state, err)
		}
		if compacted {
			return state, nil
		}
		return handleRunError(ctx, req.Events, state, fmt.Errorf("request exceeds context window: %s", fit.String()))
	}

	emitEvent(req.Events, output.NewTurnStartedEvent(turn, req.Model, len(assembly.Messages)))
	emitEvent(req.Events, output.NewModelCallStartedEvent(turn, req.Model, len(assembly.Messages)))
	response, err := completeModelCall(ctx, req, turn, chatRequest, assembly.Blocks, req.ModelBudget)
	if err != nil {
		if cancelled, ok := contextCancellationState(ctx, state); ok {
			emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, "", 0, 0, nil))
			emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, "", "", nil))
			emitStop(req.Events, cancelled, nil)
			return cancelled, nil
		}
		state.StopReason = StopReasonError
		emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, "", 0, 0, err))
		emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, "", "", err))
		emitStop(req.Events, state, err)
		return state, err
	}

	return r.handleModelResponse(ctx, req, state, turn, chatRequest, response)
}

func (r *Runner) handleModelResponse(ctx context.Context, req RunRequest, state RunState, turn int, chatRequest provider.ChatRequest, response provider.ChatResponse) (RunState, error) {
	state.TurnCount = turn
	turnTokens, err := tokenCount(ctx, chatRequest, response.Usage)
	if err != nil {
		emitEvent(req.Events, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{err.Error()},
		}))
		// Continue with 0 tokens for this turn; the error is logged but not fatal
	}
	state.TokenCount += turnTokens
	emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(req.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}

	assistant := fromProviderMessage(response.Message)
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

	if len(response.Message.ToolCalls) == 0 {
		emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, response.FinishReason, response.Message.Content, nil))
		state.StopReason = StopReasonComplete
		emitStop(req.Events, state, nil)
		return state, nil
	}

	return r.executeToolCalls(ctx, req, state, turn, response)
}

func (r *Runner) executeToolCalls(ctx context.Context, req RunRequest, state RunState, turn int, response provider.ChatResponse) (RunState, error) {
	for _, call := range response.Message.ToolCalls {
		writeTargetExistedBefore := writeTargetExistedBefore(call.Name, call.Arguments)
		emitEvent(req.Events, output.NewToolCallStartedEventWithPreviewState(turn, call.Name, call.ID, cloneInput(call.Arguments), writeTargetExistedBefore))

		result, err := req.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
		if cancelled, ok := contextCancellationState(ctx, state); ok {
			emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
			emitStop(req.Events, cancelled, nil)
			return cancelled, nil
		}

		var toolContent string
		var preview output.ToolPreview
		if err != nil {
			toolContent = formatToolError(err)
			preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent, writeTargetExistedBefore)
			emitEvent(req.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, err, preview))
		} else {
			normalizedResult := normalizeToolResult(result)
			toolContent = normalizedResult.Content
			preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent, writeTargetExistedBefore)
			emitEvent(req.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, nil, preview))
		}
		state.Conversation = append(state.Conversation, Message{
			Role:       MessageRoleTool,
			Content:    toolContent,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{{
			Role:       MessageRoleTool,
			Content:    toolContent,
			ToolCallID: call.ID,
			Name:       call.Name,
		}})
	}

	emitEvent(req.Events, output.NewTurnFinishedEvent(turn, len(response.Message.ToolCalls), response.FinishReason, response.Message.Content, nil))
	state.Conversation = state.Lineage.FullMessages()
	return state, nil
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
