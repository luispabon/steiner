package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
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
	state, turnTokens := p.finalizeModelCallState(state, turn, response)
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
		Turn:              turn,
		Model:             p.request.ResolvedModel.BackendModelID,
		FinishReason:      response.FinishReason,
		ToolCalls:         len(response.Message.ToolCalls),
		CompletionTokens:  turnTokens,
		PromptTokens:      promptUsageTokens(response.Usage),
		CacheReadTokens:   cacheReadUsageTokens(response.Usage),
		CacheCreateTokens: cacheCreateUsageTokens(response.Usage),
		DurationMs:        durationMs,
		TTFTMs:            ttftMs,
		OutputTPS:         outputTPS,
	}))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(p.request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}
	assistant := fromProviderMessage(response.Message)
	assistant.Turn = turn
	if !isStrictlyEmptyAssistant(assistant) {
		state.Conversation = append(state.Conversation, assistant)
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})
	}

	if len(response.Message.ToolCalls) == 0 {
		return p.finishAssistantOnlyTurn(ctx, state, turn, response), nil
	}

	return turnOutcome{State: state}, &response
}

func (p *turnProgressor) handleModelCallError(ctx context.Context, state RunState, turn int, err error) turnOutcome {
	if cancelled, ok := contextCancellationState(ctx, state); ok {
		emitEvent(p.request.Events, output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
			Turn:  turn,
			Model: p.request.ResolvedModel.BackendModelID,
		}))
		emitStop(p.request.Events, cancelled, nil)
		return turnOutcome{State: cancelled, Stop: true}
	}
	if isLimitTimeout(ctx) {
		err = limitTimeoutError(ctx, p.limitTimeout(ctx))
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
	response.Message.ToolCalls = withToolCallIDs(response.Message.ToolCalls)
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

func promptUsageTokens(usage *provider.UsageStats) int {
	if usage == nil {
		return 0
	}
	return usage.PromptTokens
}

func cacheReadUsageTokens(usage *provider.UsageStats) int {
	if usage == nil {
		return 0
	}
	return usage.CacheReadInputTokens
}

func cacheCreateUsageTokens(usage *provider.UsageStats) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreationInputTokens
}

func (p *turnProgressor) finalizeModelCallState(state RunState, turn int, response provider.ChatResponse) (RunState, int) {
	state.TurnCount = turn
	turnTokens := tokenCount(response.Usage)
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

// finalizeImagesAtRunExitForRequest strips every remaining image payload from
// the state a run is about to return. It applies to every exit path, so the
// caller never receives image bytes regardless of how the run stopped. When the
// lineage is empty the raw conversation is stripped directly; otherwise the
// latest generation's messages are stripped and the conversation is rebuilt
// from the lineage.
func finalizeImagesAtRunExitForRequest(req RunRequest, state RunState) RunState {
	visionState, subAgentConfigured := visionCapabilityContextForRequest(req)
	if state.Lineage.Empty() {
		state.Conversation = stripImagesFromMessages(state.Conversation, visionState, subAgentConfigured)
		return state
	}
	state.Lineage = state.Lineage.WithCurrentMessages(stripImagesFromMessages(state.Lineage.SummaryPrefixStrippedMessages(), visionState, subAgentConfigured))
	state.Conversation = state.Lineage.FullMessages()
	return state
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

	// queuedDelegations holds the delegation calls announced via
	// tool_call_queued for the tool-execution phase currently running. Entries
	// are marked started as each call is dispatched; any left when the phase
	// ends never ran (cancellation or a limit ended the turn first) and are
	// terminated with a tool_call_finished error event so the UI can close them.
	// Nil when no delegation call was queued.
	queuedDelegations *queuedDelegationCalls
	// batchID is the id of the tool batch being executed, stamped on the
	// rejected admissions the loop synthesises so every delegation finish
	// carries its full occurrence identity.
	batchID string
	// delegationAgentIDs maps each spawning delegation call's ID to the child
	// agent ID reserved for it in call-emission order this turn. The ordered
	// queue phase fills it before any handler runs; invokeTool stamps the
	// matching ID onto the call's context so concurrent handlers consume their
	// own call's ID rather than racing the generator. Nil when reservation is
	// disabled or the turn has no spawning delegation calls.
	delegationAgentIDs map[string]string
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
		if outcome.Stop || outcome.Retry {
			return outcome
		}
		// Nothing left to compact but the request still fits the hard limit:
		// continue with the model call instead of aborting the run.
		state = outcome.State
	}

	modelCtx := ctx
	if timeout := p.request.Limits.ModelCallTimeout; timeout > 0 {
		var cancelModel context.CancelFunc
		modelCtx, cancelModel = context.WithTimeoutCause(ctx, timeout, errModelCallTimeout)
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
	if isLimitTimeout(ctx) {
		err = limitTimeoutError(ctx, p.limitTimeout(ctx))
	}
	state.StopReason = StopReasonError
	return turnOutcome{State: state, Error: err, Stop: true}
}

// handleCompaction coordinates compaction when the request does not fit
// the model token budget. It returns a retry outcome on success (the caller
// should re-run the turn with the compacted state) or an error outcome on
// failure.
func (p *turnProgressor) handleCompaction(ctx context.Context, state RunState, fit prompt.RequestTokenBudget) turnOutcome {
	if provider.IsStatefulTranscript(p.request.Provider) {
		if fit.Fits {
			// Soft compaction threshold crossed, but the request fits the hard
			// limit: carry on without compacting a provider-owned transcript.
			return turnOutcome{State: state}
		}
		return turnOutcome{
			State: state,
			Error: fmt.Errorf("request exceeds context window: %s: %w", fit.String(), errStatefulCompaction),
			Stop:  true,
		}
	}
	turn := state.TurnCount + 1
	emitCompactionStartedEvent(p.request.Events, turn)
	compacted, err := p.compactFn(ctx, p.request, &state, turn, &fit, p.compactionHistory, &p.compactionCount)
	if err != nil {
		return turnOutcome{State: state, Error: err, Stop: true}
	}
	if compacted {
		return turnOutcome{State: state, Retry: true}
	}
	if fit.Fits {
		// Soft compaction threshold crossed, but no candidate is left and the
		// request fits the hard limit: proceed without compacting.
		return turnOutcome{State: state}
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
		Model:                  p.request.ResolvedModel.BackendModelID,
		Messages:               assembly.Messages,
		Tools:                  provider.CloneTools(p.request.Tools),
		PromptCacheKey:         p.request.PromptCacheKey,
		TransportSession:       p.request.TransportSession,
		ParentTransportSession: p.request.ParentTransportSession,
		Reasoning:              resolvedReasoningRequest(p.request.ResolvedModel),
		Params:                 p.request.ResolvedModel.Params,
		ExtraParams:            p.request.ResolvedModel.ExtraParams,
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

// getVisionCapabilityContext returns the vision state and sub-agent config for the current alias.
func (p *turnProgressor) getVisionCapabilityContext() (VisionState, bool) {
	return visionCapabilityContextForRequest(p.request)
}

// visionCapabilityContextForRequest returns the vision state for the request's
// resolved model alias and whether a vision sub-agent is configured. When
// VisionCapabilities is nil, returns VisionUnknown and false (preserving old
// behavior).
func visionCapabilityContextForRequest(req RunRequest) (VisionState, bool) {
	vc := req.VisionCapabilities
	if vc == nil {
		return VisionUnknown, false
	}
	return vc.Get(req.ResolvedModel.Alias), vc.SubAgentConfigured()
}

// isStrictlyEmptyAssistant reports whether m carries nothing at all: no
// content, tool calls, reasoning or provider metadata.
func isStrictlyEmptyAssistant(m Message) bool {
	return m.Content == "" && len(m.ToolCalls) == 0 && m.ReasoningContent == "" && m.ProviderMetadata == nil
}
