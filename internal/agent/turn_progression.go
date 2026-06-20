package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
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

	emitEvent(in.Request.Events, output.NewModelCallStartedEvent(turn, in.Request.ResolvedModel.BackendModelID, len(assembly.Messages)))

	startTime := time.Now()
	response, firstChunkTime, err := completeModelCall(ctx, in.Request, turn, chatRequest, assembly.Blocks, in.Request.ModelBudget)
	if err != nil {
		return p.handleModelCallError(ctx, in, turn, err)
	}

	endTime := time.Now()
	durationMs := endTime.Sub(startTime).Milliseconds()

	response = p.normalizeModelResponse(in, turn, response)
	state, turnTokens := p.finalizeModelCallState(ctx, in, turn, chatRequest, response)

	ttftMs := durationMs
	if !firstChunkTime.IsZero() {
		ttftMs = firstChunkTime.Sub(startTime).Milliseconds()
	}
	var outputTPS float64
	if durationMs > 0 && turnTokens > 0 {
		outputTPS = float64(turnTokens) / (float64(durationMs) / 1000.0)
	}

	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil, durationMs, ttftMs, outputTPS))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(in.Request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}
	assistant := fromProviderMessage(response.Message)
	assistant.Turn = turn
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

	if len(response.Message.ToolCalls) == 0 {
		return p.finishAssistantOnlyTurn(ctx, in, state, turn, response)
	}

	return turnOutcome{State: state, Response: &response}
}

func (p *turnProgressor) handleModelCallError(ctx context.Context, in turnInput, turn int, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, in.State); ok {
		emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, "", 0, 0, nil, 0, 0, 0))
		emitStop(in.Request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state := in.State
	state.StopReason = StopReasonError
	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.ResolvedModel.BackendModelID, "", 0, 0, err, 0, 0, 0))

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

func (p *turnProgressor) finishAssistantOnlyTurn(_ context.Context, in turnInput, state RunState, turn int, response provider.ChatResponse) turnOutcome {

	state.StopReason = StopReasonComplete
	state.Conversation = stripImagesFromMessages(state.Conversation)
	state.Lineage = state.Lineage.WithCurrentMessages(stripImagesFromMessages(state.Lineage.SummaryPrefixStrippedMessages()))
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
func (p *turnProgressor) executeToolCalls(ctx context.Context, in turnInput, response provider.ChatResponse) turnOutcome {
	state := in.State
	turn := state.TurnCount

	for _, call := range response.Message.ToolCalls {
		var outcome turnOutcome
		state, outcome = p.executeSingleToolCall(ctx, in, state, turn, call)
		if outcome.Stop {
			return outcome
		}
	}

	return p.finalizeToolTurn(ctx, in, state, turn, response)
}

func (p *turnProgressor) executeSingleToolCall(ctx context.Context, in turnInput, state RunState, turn int, call provider.ToolCall) (RunState, turnOutcome) {
	emitEvent(in.Request.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))

	ctx = WithConversationSnapshot(ctx, liveConversationSnapshot(state))
	result, err := in.Request.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		cancelled = replaySafeRunState(cancelled)
		emitEvent(in.Request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
		emitStop(in.Request.Events, cancelled, nil)
		return state, turnOutcome{State: cancelled, Stop: true}
	}
	if transition, ok := workflowHandoffTransitionFromResult(result); ok {
		state.StopReason = StopReasonWorkflowHandoff
		state.WorkflowHandoff = transition
		emitEvent(in.Request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))

		emitStop(in.Request.Events, state, nil)
		return state, turnOutcome{State: state, Stop: true}

	}
	toolMessage := p.buildToolMessage(in, turn, call, result, err)
	state.Conversation = append(state.Conversation, toolMessage)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{toolMessage})
	return state, turnOutcome{}
}

func liveConversationSnapshot(state RunState) []provider.Message {
	conversation := state.Lineage.FullMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}
	return ToReplaySafeProviderMessages(conversation)
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
		if normalizedResult.Image != nil {
			toolMessage.Images = []ImageBlock{{
				MediaType: normalizedResult.Image.MediaType,
				Data:      normalizedResult.Image.Data,
				Width:     normalizedResult.Image.Width,
				Height:    normalizedResult.Image.Height,
				SizeBytes: normalizedResult.Image.SizeBytes,
			}}
		}
	}
	return toolMessage
}

func (p *turnProgressor) finalizeToolTurn(_ context.Context, in turnInput, state RunState, turn int, response provider.ChatResponse) turnOutcome {

	state.Lineage = state.Lineage.WithCurrentMessages(stripImagesFromMessages(state.Lineage.SummaryPrefixStrippedMessages()))
	state.Conversation = state.Lineage.FullMessages()
	return turnOutcome{State: state}
}

func workflowHandoffTransitionFromResult(result any) (*tool.WorkflowHandoffTransition, bool) {
	switch v := result.(type) {
	case tool.WorkflowHandoffAccepted:
		return cloneWorkflowHandoffTransition(&v.Transition), true
	case *tool.WorkflowHandoffAccepted:
		if v == nil {
			return nil, false
		}
		return cloneWorkflowHandoffTransition(&v.Transition), true
	default:
		return nil, false
	}
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

// handleError converts an error into a turnOutcome, checking for cancellation
// first. Cancellation returns Stop with a nil error; everything else sets
// StopReasonError.
func (p *turnProgressor) handleError(ctx context.Context, events output.EventSink, state RunState, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitStop(events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state.StopReason = StopReasonError
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
		cm = NewContextStateManager()
	}
	var err error
	in.State, err = cm.PrepareTurnState(ctx, in.State)
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
		if block.Source.IsSystemZone() {
			systemBytes += block.ByteSize
		} else {
			conversationBytes += block.ByteSize
		}
	}
	slog.Debug("prompt zones", "turn", turn, "system_bytes", systemBytes, "conversation_bytes", conversationBytes)

	chatRequest := provider.ChatRequest{
		Model:       in.Request.ResolvedModel.BackendModelID,
		Messages:    assembly.Messages,
		Tools:       provider.CloneTools(in.Request.Tools),
		Params:      in.Request.ResolvedModel.Params,
		ExtraParams: in.Request.ResolvedModel.ExtraParams,
	}
	chatRequest = applyPromptSuffix(in.Request.ResolvedModel.PromptSuffix, chatRequest)
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

// imageBlockPlaceholder returns a compact text token describing an image whose
// binary data has been stripped, e.g. "[image: 2560x1545 png 478KB]".
func imageBlockPlaceholder(img ImageBlock) string {
	var dims string
	if img.Width > 0 && img.Height > 0 {
		dims = fmt.Sprintf("%dx%d", img.Width, img.Height)
	} else {
		dims = "?"
	}
	var fmtStr string
	if mt := img.MediaType; strings.Contains(mt, "/") {
		fmtStr = mt[strings.LastIndex(mt, "/")+1:]
	} else {
		fmtStr = mt
	}
	var sizeStr string
	if img.SizeBytes > 0 {
		if img.SizeBytes >= 1024*1024 {
			sizeStr = fmt.Sprintf("%.1fMB", float64(img.SizeBytes)/1024/1024)
		} else {
			sizeStr = fmt.Sprintf("%dKB", img.SizeBytes/1024)
		}
	}
	if sizeStr != "" {
		return fmt.Sprintf("[image: %s %s %s]", dims, fmtStr, sizeStr)
	}
	return fmt.Sprintf("[image: %s %s]", dims, fmtStr)
}

// stripImagesFromMessages clears the Data field of every ImageBlock in msgs
// whose Data is non-empty and appends a placeholder token to the containing
// message's Content so the model retains awareness of the image without the
// full base64 payload being re-sent on subsequent turns.
func stripImagesFromMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		imgs := make([]ImageBlock, len(out[i].Images))
		copy(imgs, out[i].Images)
		for j := range imgs {
			if imgs[j].Data == "" {
				continue
			}
			placeholder := imageBlockPlaceholder(imgs[j])
			imgs[j].Data = ""
			if out[i].Content == "" {
				out[i].Content = placeholder
			} else {
				out[i].Content = out[i].Content + "\n" + placeholder
			}
		}
		out[i].Images = nil
	}
	return out
}
