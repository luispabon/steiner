package agent

import (
	"context"
	"errors"
	"math"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

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
	if len(calls) > 0 {
		batchID := newToolBatchID(calls[0].ID)
		ctx = WithToolBatchID(ctx, batchID)
		p.batchID = batchID
		if p.request.Sealer != nil && p.request.GroupScope != "" {
			defer p.request.Sealer.SealGroupBatch(p.request.GroupScope, batchID)
		}
	}
	p.queuedDelegations = p.queueDelegationCalls(turn, calls)
	defer p.drainQueuedDelegations(turn)
	for i := 0; i < len(calls); {
		n := p.parallelRunLength(calls, i)
		if n <= 1 {
			var outcome turnOutcome
			state, outcome = p.executeSingleToolCall(ctx, state, turn, calls[i])
			if outcome.Stop {
				return p.finishStoppedSerial(ctx, state, outcome, turn, calls[i+1:])
			}
			i++
			continue
		}
		results := p.invokeParallel(ctx, state, turn, calls[i:i+n])
		for k := 0; k < n; k++ {
			if _, cancelled := contextCancellationState(ctx, state); cancelled || isLimitTimeout(ctx) {
				return p.finishStoppedParallel(ctx, state, turn, calls[i:], results, k, n)
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

// appendNotDispatched records a "not dispatched" result for each call that
// never ran, keeping one tool_result per tool_use.
func (p *turnProgressor) appendNotDispatched(ctx context.Context, state RunState, turn int, calls []provider.ToolCall) RunState {
	for _, call := range calls {
		state = p.appendToolOutcome(ctx, state, turn, call, nil, errors.Join(errNotDispatched, ctx.Err()), false)
	}
	return state
}

// finishStoppedSerial closes out a turn after a serial tool call stopped the
// run. On cancellation or a runner-imposed limit timeout the remaining calls
// are recorded as not dispatched.
func (p *turnProgressor) finishStoppedSerial(ctx context.Context, state RunState, outcome turnOutcome, turn int, remaining []provider.ToolCall) turnOutcome {
	cancelled := state.StopReason == StopReasonCancelled
	if !cancelled && !isLimitTimeout(ctx) {
		return outcome
	}
	state = p.appendNotDispatched(ctx, state, turn, remaining)
	if cancelled {
		return p.finalizeCancelledTurn(ctx, state)
	}
	outcome.State = state
	return outcome
}

// finishStoppedParallel closes out a turn when the context ended while
// applying the results of a parallel batch. calls starts at the batch (n calls
// long); results from index k on are recorded, then everything after the batch
// is recorded as not dispatched. A runner-imposed limit timeout is an error
// stop, not a cancel.
func (p *turnProgressor) finishStoppedParallel(ctx context.Context, state RunState, turn int, calls []provider.ToolCall, results []batchResult, k, n int) turnOutcome {
	for j := k; j < n; j++ {
		state = p.appendToolOutcome(ctx, state, turn, calls[j], results[j].value, results[j].err, results[j].started)
	}
	state = p.appendNotDispatched(ctx, state, turn, calls[n:])
	if _, cancelled := contextCancellationState(ctx, state); cancelled {
		return p.finalizeCancelledTurn(ctx, state)
	}
	return p.handleError(ctx, state, nil)
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
		p.markDelegationStarted(call.ID)
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
	p.markDelegationStarted(call.ID)
	ctx = WithConversationSnapshot(ctx, liveConversationSnapshot(state))
	result, err := p.invokeTool(ctx, turn, call)
	if state, cancelled := contextCancellationState(ctx, state); cancelled {
		state = p.appendToolOutcome(ctx, state, turn, call, result, err, true)
		return state, turnOutcome{State: state, Stop: true}
	}
	if isLimitTimeout(ctx) {
		state = p.appendToolOutcome(ctx, state, turn, call, result, err, true)
		outcome := p.handleError(ctx, state, nil)
		return outcome.State, outcome
	}
	return p.applyToolResult(ctx, state, turn, call, result, err)
}

// invokeTool runs the executor and returns the raw outcome. It does not touch
// RunState and emits events only when a tool panics (a diagnostic event). It is the single call site (besides the
// read-only vision routing bypass) through which tool execution flows, so
// this is where the file-observed checker is injected for mutate's
// replace-operation guard.
func (p *turnProgressor) invokeTool(ctx context.Context, turn int, call provider.ToolCall) (result any, err error) {
	ctx = WithDelegationAgentID(ctx, p.delegationAgentIDs[call.ID])
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, p.recoverToolPanic(turn, call, r)
		}
	}()
	if p.request.ContextManager != nil {
		ctx = tool.WithFileObservedChecker(ctx, p.request.ContextManager.FileObserved)
		// mutate is not parallel-safe (ParallelClassNone), so this lookup runs
		// serially with respect to the tracker, same as FileObserved; no extra
		// locking is needed for the unguarded FileTracker.
		ctx = tool.WithFileReadLookup(ctx, func(path string) tool.FileReadState {
			state := p.request.ContextManager.FileReadState(path, turn)
			state.Known = true
			return state
		})
	}
	ctx = tool.WithCallDiagnostics(ctx, tool.CallDiagnostics{Turn: turn, Model: p.request.ResolvedModel.BackendModelID})
	return p.request.Executor.Execute(ctx, call.Name, call.ID, cloneInput(call.Arguments))
}

// applyToolResult applies an executor outcome to the conversation state.
func (p *turnProgressor) applyToolResult(ctx context.Context, state RunState, turn int, call provider.ToolCall, result any, err error) (RunState, turnOutcome) {
	if transition, ok := workflowHandoffTransitionFromResult(result); ok {
		state.StopReason = StopReasonWorkflowHandoff
		// Already-executed parallel siblings are intentionally not retained because workflow handoff abandons the source transcript (see internal/interactive/run_flow.go conversation adoption guard).
		state.WorkflowHandoff = transition
		emitEvent(p.request.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
		p.drainQueuedDelegations(turn)
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
		toolMessage = p.buildToolMessage(turn, call, result, err, state.Lineage.latestMessages())
	} else {
		toolMessage = p.buildToolMessageWithEvent(turn, call, result, err, false, state.Lineage.latestMessages())
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
	// Terminate queued delegations before the stop event so their finished
	// events precede the run's stop_reason and the UI never sees a stop while a
	// waiting box is still open.
	p.drainQueuedDelegations(state.TurnCount)
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
