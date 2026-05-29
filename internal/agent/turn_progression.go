package agent

import (
	"context"
	"fmt"
	"log/slog"
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

	p.emitModelCallStarted(in, turn, assembly)

	response, err := p.performModelCall(ctx, in, turn, assembly, chatRequest)
	if err != nil {
		return p.handleModelCallError(ctx, in, turn, err)
	}

	response = p.normalizeModelResponse(in, turn, response)
	state, turnTokens := p.finalizeModelCallState(ctx, in, turn, chatRequest, response)
	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil))
	p.emitAssistantMessage(in, turn, response)
	state = appendAssistantMessage(state, turn, response.Message)

	if len(response.Message.ToolCalls) == 0 {
		return p.finishAssistantOnlyTurn(ctx, in, state, turn, response)
	}

	return turnOutcome{State: state, Response: &response}
}

func (p *turnProgressor) emitModelCallStarted(in turnInput, turn int, assembly prompt.Assembly) {
	emitEvent(in.Request.Events, output.NewTurnStartedEvent(turn, in.Request.ResolvedModel.BackendModelID, len(assembly.Messages)))
	emitEvent(in.Request.Events, output.NewModelCallStartedEvent(turn, in.Request.ResolvedModel.BackendModelID, len(assembly.Messages)))
}

func (p *turnProgressor) performModelCall(ctx context.Context, in turnInput, turn int, assembly prompt.Assembly, chatRequest provider.ChatRequest) (provider.ChatResponse, error) {
	return completeModelCall(ctx, in.Request, turn, chatRequest, assembly.Blocks, in.Request.ModelBudget)
}

func (p *turnProgressor) handleModelCallError(ctx context.Context, in turnInput, turn int, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, in.State); ok {
		emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, "", 0, 0, nil))
		emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", nil))
		emitStop(in.Request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state := in.State
	state.StopReason = StopReasonError
	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, "", 0, 0, err))
	emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", err))
	emitStop(in.Request.Events, state, err)
	return turnOutcome{State: state, Stop: true, Error: err}
}

func (p *turnProgressor) normalizeModelResponse(in turnInput, turn int, response provider.ChatResponse) provider.ChatResponse {
	if response.Message.Role == "" {
		response.Message.Role = provider.MessageRoleAssistant
	}
	if response.Message.Content == "" {
		return response
	}
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
	return response
}

func (p *turnProgressor) finalizeModelCallState(ctx context.Context, in turnInput, turn int, chatRequest provider.ChatRequest, response provider.ChatResponse) (RunState, int) {
	state := in.State
	state.TurnCount = turn
	turnTokens := tokenCount(ctx, chatRequest, response.Usage)
	state.TokenCount += turnTokens
	return state, turnTokens
}

func (p *turnProgressor) emitAssistantMessage(in turnInput, turn int, response provider.ChatResponse) {
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(in.Request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}
}

func appendAssistantMessage(state RunState, turn int, message provider.Message) RunState {
	assistant := fromProviderMessage(message)
	assistant.Turn = turn
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})
	return state
}

func (p *turnProgressor) finishAssistantOnlyTurn(ctx context.Context, in turnInput, state RunState, turn int, response provider.ChatResponse) turnOutcome {
	if in.Request.ContextManager != nil {
		p.maybeRunScaffoldInference(ctx, in, state, turn, response.Message.Content)
		in.Request.ContextManager.OnTurnComplete(turn, false)
	}
	emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, response.FinishReason, response.Message.Content, nil))
	state.StopReason = StopReasonComplete
	emitStop(in.Request.Events, state, nil)
	return turnOutcome{State: state, Stop: true}
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
		var outcome turnOutcome
		state, scratchpadCalled, outcome = p.executeSingleToolCall(ctx, in, state, turn, call, scratchpadCalled)
		if outcome.Stop {
			return outcome
		}
	}

	return p.finalizeToolTurn(ctx, in, state, turn, response, scratchpadCalled)
}

func (p *turnProgressor) executeSingleToolCall(ctx context.Context, in turnInput, state RunState, turn int, call provider.ToolCall, scratchpadCalled bool) (RunState, bool, turnOutcome) {
	if call.Name == "scratchpad" {
		scratchpadCalled = true
	}
	emitEvent(in.Request.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))

	result, err := in.Request.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitEvent(in.Request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
		emitStop(in.Request.Events, cancelled, nil)
		return state, scratchpadCalled, turnOutcome{State: cancelled, Stop: true}
	}

	toolMessage := p.buildToolMessage(in, turn, call, result, err)
	state.Conversation = append(state.Conversation, toolMessage)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{toolMessage})
	return state, scratchpadCalled, turnOutcome{}
}

func (p *turnProgressor) buildToolMessage(in turnInput, turn int, call provider.ToolCall, result any, err error) Message {
	var toolContent string
	var preview output.ToolPreview
	normalizedResult := ToolResultEnvelope{}
	if err != nil {
		toolContent = formatToolError(err)
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		emitEvent(in.Request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, err, preview))
	} else {
		recordMutationForContextManager(in.Request.ContextManager, call.Name, call.Arguments, result)
		normalizedResult = normalizeToolResult(result)
		toolContent = shapeIngestedToolResultForContextManager(in.Request.ContextManager, turn, call.Name, cloneInput(call.Arguments), normalizedResult.Content)
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		emitEvent(in.Request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, nil, preview))
	}
	toolMessage := Message{
		Role:       MessageRoleTool,
		Content:    toolContent,
		ToolCallID: call.ID,
		Name:       call.Name,
		Turn:       turn,
	}
	if err == nil {
		toolMessage.Retention = cloneMessageRetention(normalizedResult.Retention)
	}
	return toolMessage
}

func (p *turnProgressor) finalizeToolTurn(ctx context.Context, in turnInput, state RunState, turn int, response provider.ChatResponse, scratchpadCalled bool) turnOutcome {
	if in.Request.ContextManager != nil {
		p.maybeRunScaffoldInference(ctx, in, state, turn, response.Message.Content)
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

	if fit.ShouldCompact || !fit.Fits {
		outcome := p.handleCompaction(ctx, in, fit)
		if outcome.Error != nil {
			return p.handleError(ctx, in.Request.Events, outcome.State, outcome.Error)
		}
		return outcome
	}

	modelOutcome := p.executeModelCall(ctx, in, assembly, chatRequest)

	// Auto-detect interleaved reasoning: if the model returned reasoning_content
	// but ReasoningEchoBack was not configured, signal the runner to enable it
	// for subsequent turns. Check the last message in the updated conversation
	// so this fires for both assistant-only turns and tool-call turns.
	var detectedReasoningEchoBack bool
	if !in.Request.ResolvedModel.ReasoningEchoBack && modelOutcome.Error == nil {
		msgs := modelOutcome.State.Conversation
		if len(msgs) > 0 && msgs[len(msgs)-1].ReasoningContent != "" {
			detectedReasoningEchoBack = true
		}
	}

	if modelOutcome.Error != nil || modelOutcome.Stop {
		modelOutcome.DetectedReasoningEchoBack = detectedReasoningEchoBack
		return modelOutcome
	}

	in.State = modelOutcome.State
	toolOutcome := p.executeToolCalls(ctx, in, *modelOutcome.Response)
	toolOutcome.DetectedReasoningEchoBack = detectedReasoningEchoBack
	return toolOutcome
}

func (p *turnProgressor) maybeRunScaffoldInference(ctx context.Context, in turnInput, state RunState, turn int, assistantContent string) {
	cm, ok := in.Request.ContextManager.(ScaffoldInferrer)
	if !ok {
		return
	}
	if in.CompactionCount == nil {
		return
	}
	if !cm.ShouldRunScaffoldInference(state, *in.CompactionCount) {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}

	request := buildScaffoldInferenceRequest(in.Request, cm.ScaffoldPromptState(), assistantContent)
	response, err := completeScaffoldInferenceCall(ctx, in.Request, turn, request)
	if err != nil {
		emitEvent(in.Request.Events, output.NewScratchpadEvent(turn, false, cm.ScaffoldPromptState(), 0, err.Error()))
		return
	}

	content := strings.TrimSpace(response.Message.Content)
	if content == "" {
		emitEvent(in.Request.Events, output.NewScratchpadEvent(turn, false, cm.ScaffoldPromptState(), 0, "scaffold inference returned empty content"))
		return
	}

	cm.ApplyScaffoldInference(turn, content)
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
	compacted, err := p.runner.compactConversationForBudget(ctx, in.Request, &state, turn, &fit, in.CompactionHistory, in.CompactionCount)
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

	// Debug log: byte sizes per prompt zone to aid KV-cache tuning.
	systemBytes, conversationBytes := 0, 0
	for _, block := range assembly.Blocks {
		switch block.Source {
		case prompt.ContextSourcePreamble, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD,
			prompt.ContextSourceConversationSummary:
			systemBytes += block.ByteSize
		default:
			conversationBytes += block.ByteSize
		}
	}
	slog.Debug("prompt zones", "turn", turn, "system_bytes", systemBytes, "conversation_bytes", conversationBytes)

	chatRequest := provider.ChatRequest{
		Model:       in.Request.ResolvedModel.BackendModelID,
		Messages:    assembly.Messages,
		Tools:       cloneProviderTools(in.Request.Tools),
		Params:      in.Request.ResolvedModel.Params,
		ExtraParams: in.Request.ResolvedModel.ExtraParams,
	}
	chatRequest = buildTurnChatRequest(in.Request, chatRequest)
	chatRequest.IncludeEmptyReasoning = in.Request.ResolvedModel.ReasoningEchoBack
	if !in.Request.ResolvedModel.ReasoningEchoBack {
		stripReasoningContent(chatRequest.Messages)
	}

	fit, err := in.Request.ModelBudget.FitRequest(ctx, chatRequest)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitRequestTokenDiagnostic(in.Request.Events, turn, fit, fit.ShouldCompact || !fit.Fits)
	return assembly, chatRequest, fit, nil
}

func buildTurnChatRequest(req RunRequest, chatRequest provider.ChatRequest) provider.ChatRequest {
	return applyPromptSuffix(req.ResolvedModel.PromptSuffix, chatRequest)
}
