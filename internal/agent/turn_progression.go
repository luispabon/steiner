package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

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
		// Check for vision capability discovery error; translate to turn-level retry.
		if errors.Is(err, errRetryTurnForVision) {
			return turnOutcome{State: state, Retry: true}, nil
		}
		return p.handleModelCallError(ctx, state, turn, err), nil
	}

	endTime := time.Now()
	durationMs := endTime.Sub(startTime).Milliseconds()

	response = p.normalizeModelResponse(state, turn, response)
	state, turnTokens := p.finalizeModelCallState(ctx, state, turn, chatRequest, response)
	visionState, subAgentConfigured := p.getVisionCapabilityContext()
	state.Lineage = state.Lineage.WithCurrentMessages(stripDeferredReadImages(state.Lineage.SummaryPrefixStrippedMessages(), visionState, subAgentConfigured))
	state.Conversation = state.Lineage.FullMessages()

	ttftMs := durationMs
	if !firstChunkTime.IsZero() {
		ttftMs = firstChunkTime.Sub(startTime).Milliseconds()
	}
	var outputTPS float64
	if durationMs > 0 && turnTokens > 0 {
		outputTPS = float64(turnTokens) / (float64(durationMs) / 1000.0)
	}

	emitEvent(p.request.Events, output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn:             turn,
		Model:            p.request.ResolvedModel.BackendModelID,
		FinishReason:     response.FinishReason,
		ToolCalls:        len(response.Message.ToolCalls),
		CompletionTokens: turnTokens,
		DurationMs:       durationMs,
		TTFTMs:           ttftMs,
		OutputTPS:        outputTPS,
	}))
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
		cancelled = p.finalizeDeferredReadImages(cancelled)
		emitEvent(p.request.Events, output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
			Turn:  turn,
			Model: p.request.ResolvedModel.BackendModelID,
		}))
		emitStop(p.request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	state.StopReason = StopReasonError
	emitEvent(p.request.Events, output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn:  turn,
		Model: p.request.ResolvedModel.BackendModelID,
		Err:   err,
	}))

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
	if response.Usage != nil {
		nonCached := response.Usage.NonCachedPromptTokens()
		state.InputTokens += nonCached
		state.CacheReadTokens += response.Usage.CacheReadInputTokens
		state.CacheCreateTokens += response.Usage.CacheCreationInputTokens
	}
	return state, turnTokens
}

func (p *turnProgressor) finishAssistantOnlyTurn(_ context.Context, state RunState, _ int, _ provider.ChatResponse) turnOutcome {
	state.StopReason = StopReasonComplete
	visionState, subAgentConfigured := p.getVisionCapabilityContext()
	state.Conversation = stripImagesFromMessages(state.Conversation, visionState, subAgentConfigured)
	state.Lineage = state.Lineage.WithCurrentMessages(stripImagesFromMessages(state.Lineage.SummaryPrefixStrippedMessages(), visionState, subAgentConfigured))
	return turnOutcome{State: state, Stop: true}
}

func (p *turnProgressor) finalizeDeferredReadImages(state RunState) RunState {
	return finalizeDeferredReadImagesForRequest(p.request, state)
}

func finalizeDeferredReadImagesForRequest(req RunRequest, state RunState) RunState {
	visionState, subAgentConfigured := VisionUnknown, false
	if req.VisionCapabilities != nil {
		visionState = req.VisionCapabilities.Get(req.ResolvedModel.Alias)
		subAgentConfigured = req.VisionCapabilities.SubAgentConfigured()
	}
	state.Lineage = state.Lineage.WithCurrentMessages(stripDeferredReadImages(state.Lineage.SummaryPrefixStrippedMessages(), visionState, subAgentConfigured))
	state.Conversation = state.Lineage.FullMessages()
	return state
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
	calls := response.Message.ToolCalls
	for i := 0; i < len(calls); {
		n := p.parallelRunLength(calls, i)
		if n <= 1 {
			var outcome turnOutcome
			state, outcome = p.executeSingleToolCall(ctx, state, turn, calls[i])
			if outcome.Stop {
				if state.StopReason == StopReasonCancelled {
					for _, call := range calls[i+1:] {
						state = p.appendToolOutcome(ctx, state, turn, call, nil, errors.Join(errNotDispatched, ctx.Err()), false)
					}
					return p.finalizeCancelledTurn(ctx, state)
				}
				return outcome
			}
			i++
			continue
		}
		results := p.invokeParallel(ctx, state, turn, calls[i:i+n])
		for k := 0; k < n; k++ {
			if _, cancelled := contextCancellationState(ctx, state); cancelled {
				for j := k; j < n; j++ {
					state = p.appendToolOutcome(ctx, state, turn, calls[i+j], results[j].value, results[j].err, results[j].started)
				}
				for _, call := range calls[i+n:] {
					state = p.appendToolOutcome(ctx, state, turn, call, nil, errors.Join(errNotDispatched, ctx.Err()), false)
				}
				return p.finalizeCancelledTurn(ctx, state)
			}
			var outcome turnOutcome
			state, outcome = p.applyToolResult(ctx, state, turn, calls[i+k], results[k].value, results[k].err)
			if outcome.Stop {
				return outcome
			}
		}
		i += n
	}
	return p.finalizeToolTurn(ctx, state, turn, response)
}

func (p *turnProgressor) parallelLimitForClass(class ParallelClass) int {
	switch class {
	case ParallelClassDelegation:
		return p.request.MaxParallelDelegations
	case ParallelClassTool:
		return p.request.MaxParallelTools
	default:
		return 0
	}
}

func (p *turnProgressor) parallelRunLength(calls []provider.ToolCall, start int) int {
	if p.request.ParallelClassOf == nil {
		return 1
	}
	class := p.request.ParallelClassOf(calls[start].Name)
	if class == ParallelClassNone || p.parallelLimitForClass(class) <= 1 {
		return 1
	}
	n := 1
	for start+n < len(calls) && p.request.ParallelClassOf(calls[start+n].Name) == class {
		n++
	}
	return n
}

// errNotDispatched marks calls that never acquired the execution gate and were never launched.
var errNotDispatched = errors.New("tool not dispatched")

type batchResult struct {
	value   any
	err     error
	started bool
}

func (p *turnProgressor) invokeParallel(ctx context.Context, state RunState, turn int, calls []provider.ToolCall) []batchResult {
	batchCtx := WithConversationSnapshot(ctx, liveConversationSnapshot(state))
	results := make([]batchResult, len(calls))

	var gate *semaphore.Weighted
	if p.request.ParallelClassOf != nil {
		limit := p.parallelLimitForClass(p.request.ParallelClassOf(calls[0].Name))
		if limit > 0 {
			gate = semaphore.NewWeighted(int64(limit))
		}
	}
	var wg sync.WaitGroup
	for i, call := range calls {
		if gate != nil {
			if err := gate.Acquire(batchCtx, 1); err != nil {
				// Calls from this index onward never acquired the gate and were never launched.
				for j := i; j < len(calls); j++ {
					results[j].err = errors.Join(errNotDispatched, err)
				}
				break
			}
		}
		results[i].started = true
		emitEvent(p.request.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))
		wg.Add(1)
		go func(i int, call provider.ToolCall) {
			defer wg.Done()
			if gate != nil {
				defer gate.Release(1)
			}
			results[i].value, results[i].err = p.invokeTool(batchCtx, turn, call)
		}(i, call)
	}
	wg.Wait()
	return results
}

func (p *turnProgressor) executeSingleToolCall(ctx context.Context, state RunState, turn int, call provider.ToolCall) (RunState, turnOutcome) {
	emitEvent(p.request.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))
	ctx = WithConversationSnapshot(ctx, liveConversationSnapshot(state))
	result, err := p.invokeTool(ctx, turn, call)
	if state, cancelled := contextCancellationState(ctx, state); cancelled {
		state = p.appendToolOutcome(ctx, state, turn, call, result, err, true)
		return state, turnOutcome{State: state, Stop: true}
	}
	return p.applyToolResult(ctx, state, turn, call, result, err)
}

// invokeTool runs the executor and returns the raw outcome. It does not emit
// events or touch RunState. It is the single call site (besides the
// read-only vision routing bypass) through which tool execution flows, so
// this is where the file-observed checker is injected for mutate's
// replace-operation guard.
func (p *turnProgressor) invokeTool(ctx context.Context, _ int, call provider.ToolCall) (any, error) {
	if p.request.ContextManager != nil {
		ctx = tool.WithFileObservedChecker(ctx, p.request.ContextManager.FileObserved)
	}
	return p.request.Executor.Execute(ctx, call.Name, call.ID, cloneInput(call.Arguments))
}

// applyToolResult applies an executor outcome to the conversation state.
func (p *turnProgressor) applyToolResult(ctx context.Context, state RunState, turn int, call provider.ToolCall, result any, err error) (RunState, turnOutcome) {
	if transition, ok := workflowHandoffTransitionFromResult(result); ok {
		state.StopReason = StopReasonWorkflowHandoff
		// Already-executed parallel siblings are intentionally not retained because workflow handoff abandons the source transcript (see internal/interactive/run_flow.go conversation adoption guard).
		state.WorkflowHandoff = transition
		emitEvent(p.request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
		state = p.finalizeDeferredReadImages(state)
		emitStop(p.request.Events, state, nil)
		return state, turnOutcome{State: state, Stop: true}
	}
	return p.appendToolOutcome(ctx, state, turn, call, result, err, true), turnOutcome{}
}

func calibratedToolDelta(delta, previousRaw, calibrated int) int {
	if previousRaw > 0 && calibrated > 0 {
		return int(math.Round(float64(delta) * float64(calibrated) / float64(previousRaw)))
	}
	if previousRaw == 0 && calibrated > 0 {
		return delta
	}
	return 0
}

func (p *turnProgressor) appendToolOutcome(ctx context.Context, state RunState, turn int, call provider.ToolCall, result any, err error, emitFinished bool) RunState {
	var toolMessage Message
	if emitFinished {
		toolMessage = p.buildToolMessage(turn, call, result, err)
	} else {
		toolMessage = p.buildToolMessageWithEvent(turn, call, result, err, false)
	}
	state.Conversation = append(state.Conversation, toolMessage)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{toolMessage})
	if p.lastBudget != nil && p.lastBudget.ContextSize > 0 {
		provMsg := toProviderMessage(toolMessage)
		delta, err := provider.EstimateMessageTokens(ctx, p.request.ResolvedModel.BackendModelID, provMsg)
		if err == nil && delta > 0 {
			previousRaw := p.lastBudget.RawEstimatedPromptTokens
			p.lastBudget.RawEstimatedPromptTokens += delta
			calibratedDelta := calibratedToolDelta(delta, previousRaw, p.lastBudget.EstimatedPromptTokens)
			p.lastBudget.EstimatedPromptTokens += calibratedDelta
			p.lastBudget.TotalTokens += calibratedDelta
			p.lastBudget.PromptUsage = float64(p.lastBudget.EstimatedPromptTokens) / float64(p.lastBudget.ContextSize)
			emitRequestTokenDiagnostic(p.request.Events, turn, *p.lastBudget, false)
		}
	}
	return state
}

func (p *turnProgressor) finalizeCancelledTurn(ctx context.Context, state RunState) turnOutcome {
	cancelled, _ := contextCancellationState(ctx, state)
	visionState, subAgentConfigured := p.getVisionCapabilityContext()
	cancelled.Lineage = cancelled.Lineage.WithCurrentMessages(stripImagesFromMessages(cancelled.Lineage.SummaryPrefixStrippedMessages(), visionState, subAgentConfigured))
	cancelled.Conversation = cancelled.Lineage.FullMessages()
	cancelled = replaySafeRunState(cancelled)
	emitStop(p.request.Events, cancelled, nil)
	return turnOutcome{State: cancelled, Stop: true}
}

func liveConversationSnapshot(state RunState) []provider.Message {
	conversation := state.Lineage.FullMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}
	return ToReplaySafeProviderMessages(conversation)
}

func (p *turnProgressor) buildToolMessage(turn int, call provider.ToolCall, result any, err error) Message {
	return p.buildToolMessageWithEvent(turn, call, result, err, true)
}

func (p *turnProgressor) buildToolMessageWithEvent(turn int, call provider.ToolCall, result any, err error, emitFinished bool) Message {
	var toolContent string
	var preview output.ToolPreview
	normalizedResult := ToolResultEnvelope{}
	if err != nil {
		if projected, ok := projectedToolError(err); ok {
			toolContent = projected
		} else {
			toolContent = formatToolError(err)
		}
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		if emitFinished {
			emitEvent(p.request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, err, preview))
		}
	} else {
		recordMutationForContextManager(p.request.ContextManager, call.Name, call.Arguments, result)
		normalizedResult = normalizeToolResult(result)
		if normalizedResult.Projected {
			projected, ok := projectedToolResult(resultValue(result))
			if ok {
				toolContent = projected
			} else {
				toolContent = normalizedResult.Content
			}
		} else {
			toolContent = shapeIngestedToolResultForContextManager(p.request.ContextManager, turn, call.Name, cloneInput(call.Arguments), normalizedResult.Content)
		}
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		if emitFinished {
			emitEvent(p.request.Events, output.NewToolCallFinishedEventWithPreview(turn, call.Name, call.ID, toolContent, nil, preview))
		}
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
			image := *normalizedResult.Image
			if call.Name == "read" && p.request.ImageStore != nil && image.FilePath != "" {
				ref := p.request.ImageStore.Register(image.FilePath, image.MediaType, image.Width, image.Height, image.SizeBytes)
				image.ID = ref.ID
				image.FilePath = ref.FilePath
			}
			toolMessage.Images = []ImageBlock{image}
		}
	}
	return toolMessage
}

func resultValue(result any) any {
	if execution, ok := result.(tool.ExecutionResult); ok {
		return execution.Value
	}
	return result
}

func (p *turnProgressor) finalizeToolTurn(_ context.Context, state RunState, _ int, _ provider.ChatResponse) turnOutcome {
	visionState, subAgentConfigured := p.getVisionCapabilityContext()
	messages := state.Lineage.SummaryPrefixStrippedMessages()
	if p.request.VisionCapabilities != nil {
		messages = stripImagesFromMessagesExceptDeferredRead(messages, visionState, subAgentConfigured)
	} else {
		messages = stripImagesFromMessages(messages, visionState, subAgentConfigured)
	}
	state.Lineage = state.Lineage.WithCurrentMessages(messages)
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

	// lastBudget holds a copy of the most recent request token budget.
	// It is updated after each tool result is appended so the context
	// meter reflects mid-turn prompt growth.
	lastBudget *prompt.RequestTokenBudget
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
	_ = p.handleImagesForVision(ctx, &state)

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

	modelCtx := ctx
	if timeout := p.request.Limits.ModelCallTimeout; timeout > 0 {
		var cancelModel context.CancelFunc
		modelCtx, cancelModel = context.WithTimeout(ctx, timeout)
		defer cancelModel()
	}
	modelOutcome, response := p.executeModelCall(modelCtx, state, assembly, chatRequest)

	// Auto-detect interleaved reasoning: if the model returned reasoning_content
	// but ReasoningEchoBack was not configured, enable it on the progressor's
	// request so subsequent turns preserve reasoning.
	if !p.request.ResolvedModel.ReasoningEchoBack && modelOutcome.Error == nil {
		msgs := modelOutcome.State.Conversation
		if len(msgs) > 0 && msgs[len(msgs)-1].ReasoningContent != "" {
			p.request.ResolvedModel.ReasoningEchoBack = true
		}
	}

	if modelOutcome.Error != nil || modelOutcome.Stop || modelOutcome.Retry {
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
	p.lastBudget = nil

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
		Reasoning:      resolvedReasoningRequest(p.request.ResolvedModel),
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
	// Store a copy so mid-turn tool result appends can incrementally
	// update the context meter without full re-assembly.
	budgetCopy := fit
	p.lastBudget = &budgetCopy
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
// binary data has been stripped. Wording adapts based on whether the model can see images.
// visionState: the capability state for the current alias (Capable/Incapable/Unknown).
// subAgentConfigured: whether a vision sub-agent is configured (for routing).
func imageBlockPlaceholder(img ImageBlock, visionState VisionState, subAgentConfigured bool) string {
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
		descriptive := ""
		if sizeStr != "" {
			descriptive = fmt.Sprintf("[image %s: %s %s %s %s",
				img.ID, img.FilePath, dims, fmtStr, sizeStr)
		} else {
			descriptive = fmt.Sprintf("[image %s: %s %s %s",
				img.ID, img.FilePath, dims, fmtStr)
		}

		var suffix string
		switch {
		case visionState == VisionIncapable && subAgentConfigured:
			// Non-vision with sub-agent: advertise follow_up, not read.
			suffix = " — use follow_up with the agent_id from the image analysis]"
		case visionState == VisionIncapable:
			// Non-vision without sub-agent: no re-examine hint.
			suffix = "]"
		default:
			suffix = fmt.Sprintf(" — use vision tool with image_id \"%s\" or read tool to re-examine]", img.ID)
		}
		return descriptive + suffix
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
// visionState: the capability state for the current alias (Capable/Incapable/Unknown).
// subAgentConfigured: whether a vision sub-agent is configured (for routing).
func stripImagesFromMessages(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
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
			placeholder := imageBlockPlaceholder(imgs[j], visionState, subAgentConfigured)
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

// stripImagesFromMessagesExceptDeferredRead strips all image data except read
// tool results, which remain available for the immediately following model request.
func stripImagesFromMessagesExceptDeferredRead(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionState, subAgentConfigured)[0]
	}
	return out
}

// stripDeferredReadImages removes read image data after the next model request
// has consumed it. It does not affect pasted images or other tool messages.
func stripDeferredReadImages(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if !isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionState, subAgentConfigured)[0]
	}
	return out
}

func isDeferredReadImageMessage(msg Message) bool {
	return msg.Role == MessageRoleTool && msg.Name == "read" && len(msg.Images) > 0 && msg.Images[0].Data != ""
}

// getVisionCapabilityContext returns the vision state and sub-agent config for the current alias.
// When VisionCapabilities is nil, returns VisionUnknown and false (preserving old behavior).
func (p *turnProgressor) getVisionCapabilityContext() (VisionState, bool) {
	vc := p.request.VisionCapabilities
	if vc == nil {
		return VisionUnknown, false
	}
	alias := p.request.ResolvedModel.Alias
	return vc.Get(alias), vc.SubAgentConfigured()
}
