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

// compactConversationFn is the compaction operation decoupled from *Runner.
// It matches the signature of Runner.compactConversationForBudget.
type compactConversationFn func(ctx context.Context, req RunRequest, state *RunState, turn int, beforeFit *prompt.RequestTokenBudget, skipped map[string]bool, compactionCount *int) (bool, error)

// executeModelCall runs the model-call phase of the turn lifecycle and applies
// the assistant response to the conversation state. It owns:
//   - ModelCallStarted event emission
//   - completeModelCall invocation
//   - cancellation and error handling
//   - token accounting and ModelCallFinished event
//   - AssistantMessage event
//   - assistant transcript and state mutation
//   - StopReason event emission for assistant-only turns
//
// When the response contains tool calls, it returns the response as a local
// value so turnProgressor.advance can pass it to executeToolCalls.
func (p *turnProgressor) executeModelCall(ctx context.Context, state RunState, assembly prompt.Assembly, chatRequest provider.ChatRequest) (turnOutcome, *provider.ChatResponse) {
	turn := state.TurnCount + 1

	emitEvent(p.request.Events, output.NewModelCallStartedEvent(turn, p.request.ResolvedModel.BackendModelID, len(assembly.Messages)))

	startTime := time.Now()
	response, firstChunkTime, err := completeModelCall(ctx, p.request, turn, chatRequest, assembly.Blocks, p.request.ModelBudget, &p.skipNonStream)
	if err != nil {
		return p.handleModelCallError(ctx, state, turn, err), nil
	}

	endTime := time.Now()
	durationMs := endTime.Sub(startTime).Milliseconds()

	response = p.normalizeModelResponse(state, turn, response)
	state, turnTokens := p.finalizeModelCallState(ctx, state, turn, chatRequest, response)

	ttftMs := durationMs
	if !firstChunkTime.IsZero() {
		ttftMs = firstChunkTime.Sub(startTime).Milliseconds()
	}
	var outputTPS float64
	if durationMs > 0 && turnTokens > 0 {
		outputTPS = float64(turnTokens) / (float64(durationMs) / 1000.0)
	}

	emitEvent(p.request.Events, output.NewModelCallFinishedEvent(turn, p.request.ResolvedModel.BackendModelID, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil, durationMs, ttftMs, outputTPS))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(p.request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}
	assistant := fromProviderMessage(response.Message)
	assistant.Turn = turn
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

	if len(response.Message.ToolCalls) == 0 {
		return p.finishAssistantOnlyTurn(ctx, state, turn, response), nil
	}

	return turnOutcome{State: state}, &response
}

func (p *turnProgressor) handleModelCallError(ctx context.Context, state RunState, turn int, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitEvent(p.request.Events, output.NewModelCallFinishedEvent(turn, p.request.ResolvedModel.BackendModelID, "", 0, 0, nil, 0, 0, 0))
		emitStop(p.request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state.StopReason = StopReasonError
	emitEvent(p.request.Events, output.NewModelCallFinishedEvent(turn, p.request.ResolvedModel.BackendModelID, "", 0, 0, err, 0, 0, 0))

	return turnOutcome{State: state, Stop: true, Error: err}
}

func (p *turnProgressor) normalizeModelResponse(_ RunState, turn int, response provider.ChatResponse) provider.ChatResponse {
	if response.Message.Role == "" {
		response.Message.Role = provider.MessageRoleAssistant
	}
	if response.Message.Content == "" {
		return response
	}
	sanitized, note := processAssistantResponseForContextManager(p.request.ContextManager, turn, response.Message.Content)
	response.Message.Content = sanitized
	if note != "" {
		emitEvent(p.request.Events, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Turn:     turn,
			Notes:    []string{note},
		}))
	}
	return response
}

func (p *turnProgressor) finalizeModelCallState(ctx context.Context, state RunState, turn int, chatRequest provider.ChatRequest, response provider.ChatResponse) (RunState, int) {
	state.TurnCount = turn
	turnTokens := tokenCount(ctx, chatRequest, response.Usage)
	state.TokenCount += turnTokens
	return state, turnTokens
}

func (p *turnProgressor) finishAssistantOnlyTurn(_ context.Context, state RunState, _ int, _ provider.ChatResponse) turnOutcome {
	state.StopReason = StopReasonComplete
	state.Conversation = stripImagesFromMessages(state.Conversation)
	state.Lineage = state.Lineage.WithCurrentMessages(stripImagesFromMessages(state.Lineage.SummaryPrefixStrippedMessages()))
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
//   - StopReason / finished-turn handling after all tools
func (p *turnProgressor) executeToolCalls(ctx context.Context, state RunState, response provider.ChatResponse) turnOutcome {
	turn := state.TurnCount

	for _, call := range response.Message.ToolCalls {
		var outcome turnOutcome
		state, outcome = p.executeSingleToolCall(ctx, state, turn, call)
		if outcome.Stop {
			return outcome
		}
	}

	return p.finalizeToolTurn(ctx, state, turn, response)
}

func (p *turnProgressor) executeSingleToolCall(ctx context.Context, state RunState, turn int, call provider.ToolCall) (RunState, turnOutcome) {
	emitEvent(p.request.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))

	ctx = WithConversationSnapshot(ctx, liveConversationSnapshot(state))
	result, err := p.request.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		cancelled = replaySafeRunState(cancelled)
		emitEvent(p.request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
		emitStop(p.request.Events, cancelled, nil)
		return state, turnOutcome{State: cancelled, Stop: true}
	}
	if transition, ok := workflowHandoffTransitionFromResult(result); ok {
		state.StopReason = StopReasonWorkflowHandoff
		state.WorkflowHandoff = transition
		emitEvent(p.request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))

		emitStop(p.request.Events, state, nil)
		return state, turnOutcome{State: state, Stop: true}

	}
	toolMessage := p.buildToolMessage(turn, call, result, err)
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

func (p *turnProgressor) buildToolMessage(turn int, call provider.ToolCall, result any, err error) Message {
	var toolContent string
	var preview output.ToolPreview
	normalizedResult := ToolResultEnvelope{}
	if err != nil {
		toolContent = formatToolError(err)
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		emitEvent(p.request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, err, preview))
	} else {
		recordMutationForContextManager(p.request.ContextManager, call.Name, call.Arguments, result)
		normalizedResult = normalizeToolResult(result)
		toolContent = shapeIngestedToolResultForContextManager(p.request.ContextManager, turn, call.Name, cloneInput(call.Arguments), normalizedResult.Content)
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		emitEvent(p.request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, nil, preview))
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

func (p *turnProgressor) finalizeToolTurn(_ context.Context, state RunState, _ int, _ provider.ChatResponse) turnOutcome {

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

// turnProgressor owns the per-turn progression lifecycle and its per-run state.
type turnProgressor struct {
	request           RunRequest
	basePrompt        prompt.AssemblyOptions
	compactFn         compactConversationFn
	compactionHistory map[string]bool
	// compactionCount tracks the number of successful compactions in this run.
	// It is distinct from state.Context.CompactionCount, which carries the
	// initial value from the prompt's durable context state (reserved for
	// future cross-run persistence; currently always 0).
	compactionCount int
	// skipNonStream signals that non-streaming requests should be skipped.
	// Set to true after detecting a "stream required" error, so subsequent
	// turns go straight to streaming.
	skipNonStream bool
}

func newTurnProgressor(req RunRequest, base prompt.AssemblyOptions, compactFn compactConversationFn) *turnProgressor {
	return &turnProgressor{
		request:           req,
		basePrompt:        base,
		compactFn:         compactFn,
		compactionHistory: map[string]bool{},
	}
}

// advance runs one complete turn: prepare, compaction if needed, model call,
// and tool calls if the response contains them. It returns the outcome which
// the Runner's outer loop interprets for stop/retry decisions. Echo-back
// detection is internal: when the model returns reasoning_content but
// ReasoningEchoBack was not configured, it enables it on the progressor's
// request so subsequent turns preserve reasoning.
func (p *turnProgressor) advance(ctx context.Context, state RunState) turnOutcome {
	assembly, chatRequest, fit, err := p.prepareTurn(ctx, state)
	if err != nil {
		return p.handleError(ctx, state, err)
	}

	if fit.ShouldCompact || !fit.Fits {
		outcome := p.handleCompaction(ctx, state, fit)
		if outcome.Error != nil {
			return p.handleError(ctx, outcome.State, outcome.Error)
		}
		return outcome
	}

	modelOutcome, response := p.executeModelCall(ctx, state, assembly, chatRequest)

	// Auto-detect interleaved reasoning: if the model returned reasoning_content
	// but ReasoningEchoBack was not configured, enable it on the progressor's
	// request so subsequent turns preserve reasoning.
	if !p.request.ResolvedModel.ReasoningEchoBack && modelOutcome.Error == nil {
		msgs := modelOutcome.State.Conversation
		if len(msgs) > 0 && msgs[len(msgs)-1].ReasoningContent != "" {
			p.request.ResolvedModel.ReasoningEchoBack = true
		}
	}

	if modelOutcome.Error != nil || modelOutcome.Stop {
		return modelOutcome
	}

	return p.executeToolCalls(ctx, modelOutcome.State, *response)
}

// handleError converts an error into a turnOutcome, checking for cancellation
// first. Cancellation returns Stop with a nil error; everything else sets
// StopReasonError.
func (p *turnProgressor) handleError(ctx context.Context, state RunState, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitStop(p.request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state.StopReason = StopReasonError
	return turnOutcome{State: state, Error: err, Stop: true}
}

// handleCompaction coordinates compaction when the request does not fit
// the model token budget. It returns a retry outcome on success (the caller
// should re-run the turn with the compacted state) or an error outcome on
// failure.
func (p *turnProgressor) handleCompaction(ctx context.Context, state RunState, fit prompt.RequestTokenBudget) turnOutcome {
	turn := state.TurnCount + 1
	emitCompactionStartedEvent(p.request.Events, turn)
	compacted, err := p.compactFn(ctx, p.request, &state, turn, &fit, p.compactionHistory, &p.compactionCount)
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
// against the model token budget. Diagnostics are emitted through the progressor's
// event sink.
func (p *turnProgressor) prepareTurn(ctx context.Context, state RunState) (prompt.Assembly, provider.ChatRequest, prompt.RequestTokenBudget, error) {
	turn := state.TurnCount + 1

	cm := p.request.ContextManager
	if cm == nil {
		cm = NewContextStateManager()
	}
	var err error
	state, err = cm.PrepareTurnState(ctx, state)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, fmt.Errorf("pre assembly: %w", err)
	}

	assembly, err := prompt.Assemble(ctx, assemblyOptions(p.basePrompt, state))
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitAssemblyDiagnostics(p.request.Events, p.request.Prompt, turn, assembly)

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
		Model:          p.request.ResolvedModel.BackendModelID,
		Messages:       assembly.Messages,
		Tools:          provider.CloneTools(p.request.Tools),
		PromptCacheKey: p.request.PromptCacheKey,
		Params:         p.request.ResolvedModel.Params,
		ExtraParams:    p.request.ResolvedModel.ExtraParams,
	}
	chatRequest = applyPromptSuffix(p.request.ResolvedModel.PromptSuffix, chatRequest)
	chatRequest.IncludeEmptyReasoning = p.request.ResolvedModel.ReasoningEchoBack
	if !p.request.ResolvedModel.ReasoningEchoBack {
		stripReasoningContent(chatRequest.Messages)
	}

	fit, err := p.request.ModelBudget.FitRequest(ctx, chatRequest)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitRequestTokenDiagnostic(p.request.Events, turn, fit, fit.ShouldCompact || !fit.Fits)
	return assembly, chatRequest, fit, nil
}

// formatSize returns a human-readable size string like "2KB" or "1.5MB".
func formatSize(sizeBytes int) string {
	if sizeBytes <= 0 {
		return ""
	}
	if sizeBytes >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(sizeBytes)/1024/1024)
	}
	return fmt.Sprintf("%dKB", sizeBytes/1024)
}

// imageBlockPlaceholder returns a compact text token describing an image whose
// binary data has been stripped. If ID and FilePath are set, includes a hint
// about using the vision tool; otherwise uses the legacy format.
// Examples: "[image img-1: /path/to/img.png 2560x1545 png 478KB — use vision tool with image_id "img-1" or read tool to re-examine]"
//
//	"[image: 2560x1545 png 478KB]" (legacy format for backward compat)
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
	sizeStr := formatSize(img.SizeBytes)

	// New format when ID and FilePath are both set.
	if img.ID != "" && img.FilePath != "" {
		if sizeStr != "" {
			return fmt.Sprintf("[image %s: %s %s %s %s — use vision tool with image_id \"%s\" or read tool to re-examine]",
				img.ID, img.FilePath, dims, fmtStr, sizeStr, img.ID)
		}
		return fmt.Sprintf("[image %s: %s %s %s — use vision tool with image_id \"%s\" or read tool to re-examine]",
			img.ID, img.FilePath, dims, fmtStr, img.ID)
	}

	// Legacy format for backward compat (when ID/FilePath are not set).
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
