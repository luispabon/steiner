package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// executeModelCall runs the model-call phase of the turn lifecycle and applies
// the assistant response to the conversation state. It owns:
//   - TurnStarted / ModelCallStarted event emission
//   - completeModelCall invocation
//   - cancellation and error handling
//   - token accounting and ModelCallFinished event
//   - AssistantMessage event
//   - assistant transcript and state mutation
//   - TurnFinished / StopReason event emission for assistant-only turns
//
// When the response contains tool calls, it returns them via outcome.Response
// so turnProgressor.advance can pass it to executeToolCalls.
func (p *turnProgressor) executeModelCall(ctx context.Context, in turnInput, assembly prompt.Assembly, chatRequest provider.ChatRequest) turnOutcome {
	turn := in.State.TurnCount + 1

	emitEvent(in.Request.Events, output.NewTurnStartedEvent(turn, in.Request.Model, len(assembly.Messages)))
	emitEvent(in.Request.Events, output.NewModelCallStartedEvent(turn, in.Request.Model, len(assembly.Messages)))

	response, err := completeModelCall(ctx, in.Request, turn, chatRequest, assembly.Blocks, in.Request.ModelBudget)
	if err != nil {
		if cancelled, ok := contextCancellationState(ctx, in.State); ok {
			emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, "", 0, 0, nil))
			emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", nil))
			emitStop(in.Request.Events, cancelled, nil)
			return turnOutcome{State: cancelled, Stop: true}
		}
		state := in.State
		state.StopReason = StopReasonError
		emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, "", 0, 0, err))
		emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", err))
		emitStop(in.Request.Events, state, err)
		return turnOutcome{State: state, Stop: true, Error: err}
	}

	if response.Message.Role == "" {
		response.Message.Role = provider.MessageRoleAssistant
	}
	if response.Message.Content != "" {
		sanitized, note := processAssistantResponseForContextManager(in.Request.ContextManager, turn, response.Message.Content)
		response.Message.Content = sanitized
		if note != "" {
			emitEvent(in.Request.Events, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Turn:     turn,
				Notes:    []string{note},
			}))
		}
	}
	state := in.State
	state.TurnCount = turn
	turnTokens, err := tokenCount(ctx, chatRequest, response.Usage)
	if err != nil {
		emitEvent(in.Request.Events, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{err.Error()},
		}))
	}
	state.TokenCount += turnTokens
	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(in.Request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}

	assistant := fromProviderMessage(response.Message)
	assistant.Turn = turn
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

	if len(response.Message.ToolCalls) == 0 {
		if in.Request.ContextManager != nil {
			in.Request.ContextManager.OnTurnComplete(turn, false)
		}
		emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, response.FinishReason, response.Message.Content, nil))
		state.StopReason = StopReasonComplete
		emitStop(in.Request.Events, state, nil)
		return turnOutcome{State: state, Stop: true}
	}

	return turnOutcome{State: state, Response: &response}
}

// executeToolCalls runs the tool-execution phase of the turn lifecycle and
// applies tool results to the conversation state. It owns:
//   - ToolCallStarted event emission
//   - executor invocation
//   - cancellation handling
//   - tool error formatting
//   - tool preview construction
//   - ToolCallFinished event emission
//   - tool message append to conversation/lineage
//   - TurnFinished event emission after all tools
//   - OnTurnComplete notification for scratchpad tracking
func (p *turnProgressor) executeToolCalls(ctx context.Context, in turnInput, response provider.ChatResponse) turnOutcome {
	state := in.State
	turn := state.TurnCount
	scratchpadCalled := false

	for _, call := range response.Message.ToolCalls {
		if call.Name == "scratchpad" {
			scratchpadCalled = true
		}
		writeTargetExistedBefore := writeTargetExistedBefore(call.Name, call.Arguments)
		emitEvent(in.Request.Events, output.NewToolCallStartedEventWithPreviewState(turn, call.Name, call.ID, cloneInput(call.Arguments), writeTargetExistedBefore))

		result, err := in.Request.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
		if cancelled, ok := contextCancellationState(ctx, state); ok {
			emitEvent(in.Request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
			emitStop(in.Request.Events, cancelled, nil)
			return turnOutcome{State: cancelled, Stop: true}
		}

		var toolContent string
		var preview output.ToolPreview
		if err != nil {
			toolContent = formatToolError(err)
			preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent, writeTargetExistedBefore)
			emitEvent(in.Request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, err, preview))
		} else {
			normalizedResult := normalizeToolResult(result)
			toolContent = shapeIngestedToolResultForContextManager(in.Request.ContextManager, turn, call.Name, normalizedResult.Content)
			preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent, writeTargetExistedBefore)
			emitEvent(in.Request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, nil, preview))
		}
		state.Conversation = append(state.Conversation, Message{
			Role:       MessageRoleTool,
			Content:    toolContent,
			ToolCallID: call.ID,
			Name:       call.Name,
			Turn:       turn,
		})
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{{
			Role:       MessageRoleTool,
			Content:    toolContent,
			ToolCallID: call.ID,
			Name:       call.Name,
			Turn:       turn,
		}})
	}

	if in.Request.ContextManager != nil {
		in.Request.ContextManager.OnTurnComplete(turn, scratchpadCalled)
	}
	emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, len(response.Message.ToolCalls), response.FinishReason, response.Message.Content, nil))
	state.Conversation = state.Lineage.FullMessages()
	return turnOutcome{State: state}
}

// turnProgressor owns the per-turn progression lifecycle.
type turnProgressor struct {
	runner *Runner
}

func newTurnProgressor(runner *Runner) *turnProgressor {
	return &turnProgressor{runner: runner}
}

// advance runs one complete turn: prepare, compaction if needed, model call,
// and tool calls if the response contains them. It returns the outcome which
// the Runner's outer loop interprets for stop/retry decisions.
func (p *turnProgressor) advance(ctx context.Context, in turnInput) turnOutcome {
	assembly, chatRequest, fit, err := prepareTurn(ctx, in)
	if err != nil {
		return p.handleError(ctx, in.Request.Events, in.State, err)
	}

	if !fit.Fits {
		outcome := p.handleCompaction(ctx, in, fit)
		if outcome.Error != nil {
			return p.handleError(ctx, in.Request.Events, outcome.State, outcome.Error)
		}
		return outcome
	}

	outcome := p.executeModelCall(ctx, in, assembly, chatRequest)
	if outcome.Error != nil || outcome.Stop {
		return outcome
	}

	in.State = outcome.State
	return p.executeToolCalls(ctx, in, *outcome.Response)
}

// handleError converts an error into a turnOutcome, checking for cancellation
// first. Cancellation returns Stop with a nil error; everything else sets
// StopReasonError.
func (p *turnProgressor) handleError(ctx context.Context, events output.EventSink, state RunState, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitStop(events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state.StopReason = StopReasonError
	emitStop(events, state, err)
	return turnOutcome{State: state, Error: err, Stop: true}
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

	cm := in.Request.ContextManager
	if cm == nil {
		cm = &NaiveContextManager{}
	}
	var err error
	in.State, err = cm.PreAssembly(ctx, in.State)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, fmt.Errorf("pre assembly: %w", err)
	}

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
