package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
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
	Temperature *float64
	MaxTokens   *int
	Limits      Limits
	Events      output.EventSink
}

type Runner struct{}

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

		turn := state.TurnCount + 1
		assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state))
		if err != nil {
			if cancelled, ok := contextCancellationState(ctx, state); ok {
				emitStop(req.Events, cancelled, nil)
				return cancelled, nil
			}
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}
		emitAssemblyDiagnostics(req.Events, req.Prompt, turn, assembly)

		chatRequest := provider.ChatRequest{
			Model:       req.Model,
			Messages:    assembly.Messages,
			Tools:       cloneProviderTools(req.Tools),
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		}

		fit, err := req.ModelBudget.FitRequest(ctx, chatRequest)
		if err != nil {
			if cancelled, ok := contextCancellationState(ctx, state); ok {
				emitStop(req.Events, cancelled, nil)
				return cancelled, nil
			}
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}
		emitRequestTokenDiagnostic(req.Events, turn, fit, !fit.Fits)
		if !fit.Fits {
			emitCompactionStartedEvent(req.Events, turn)
			compacted, err := r.compactConversationForBudget(ctx, req, &state, turn, compactionHistory, &compactionCount)
			if err != nil {
				if cancelled, ok := contextCancellationState(ctx, state); ok {
					emitStop(req.Events, cancelled, nil)
					return cancelled, nil
				}
				state.StopReason = StopReasonError
				emitStop(req.Events, state, err)
				return state, err
			}
			if compacted {
				continue
			}
			state.StopReason = StopReasonError
			err = fmt.Errorf("request exceeds context window: %s", fit.String())
			emitStop(req.Events, state, err)
			return state, err
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

		state.TurnCount = turn
		turnTokens := tokenCount(ctx, chatRequest, response.Usage)
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
		for _, call := range response.Message.ToolCalls {
			writeTargetExistedBefore := writeTargetExistedBefore(call.Name, call.Arguments)
			emitEvent(req.Events, output.NewToolCallStartedEventWithPreviewState(turn, call.Name, call.ID, cloneInput(call.Arguments), writeTargetExistedBefore))

			result, err := req.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
			if err != nil {
				if cancelled, ok := contextCancellationState(ctx, state); ok {
					emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
					emitStop(req.Events, cancelled, nil)
					return cancelled, nil
				}
				emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", err))
				state.StopReason = StopReasonError
				emitStop(req.Events, state, err)
				return state, err
			}

			normalizedResult := normalizeToolResult(result)
			preview := output.BuildToolPreview(call.Name, cloneInput(call.Arguments), normalizedResult.Content, writeTargetExistedBefore)
			emitEvent(req.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, normalizedResult.Content, nil, preview))
			state.Conversation = append(state.Conversation, Message{
				Role:       MessageRoleTool,
				Content:    normalizedResult.Content,
				ToolCallID: call.ID,
				Name:       call.Name,
			})
			state.Lineage = state.Lineage.WithAppendedMessages([]Message{{
				Role:       MessageRoleTool,
				Content:    normalizedResult.Content,
				ToolCallID: call.ID,
				Name:       call.Name,
			}})
		}

		emitEvent(req.Events, output.NewTurnFinishedEvent(turn, len(response.Message.ToolCalls), response.FinishReason, response.Message.Content, nil))
		state.Conversation = state.Lineage.FullMessages()
	}
}
