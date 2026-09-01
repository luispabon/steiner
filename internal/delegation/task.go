package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// AgentRunner defines the contract for executing an agent with a given request.
type AgentRunner interface {
	// Run executes the agent and returns the final state.
	Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error)
}

const delegateRetentionSummaryMaxRunes = 4000

const maxDelegateExtensions = 3

// turnBudgetNoticeFunc builds an agent.RunRequest.TurnBudgetNotice closure
// carrying extensionsLeft, the number of delegate extensions still available
// after the run it is attached to.
func turnBudgetNoticeFunc(extensionsLeft int) func(turnsUsed, maxTurns int) string {
	return func(turnsUsed, maxTurns int) string {
		return fmt.Sprintf("You have used %d of %d turns (%d remaining) with %d extension(s) remaining. Finish the highest-value remaining work now, then report status and what is left, rather than continuing to explore.",
			turnsUsed, maxTurns, maxTurns-turnsUsed, extensionsLeft)
	}
}

func delegateNeedsExtension(state agent.RunState) bool {
	if state.StopReason != agent.StopReasonMaxTurns {
		return false
	}
	msg, ok := agent.LastAssistantMessage(state.Conversation)
	if !ok {
		return false
	}
	return len(msg.ToolCalls) > 0
}

func truncateTaskPreview(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max < 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func emitDelegateStarted(events output.EventSink, spec Spec, modelAlias string, agentType AgentType) {
	if events == nil {
		return
	}
	event := output.NewDelegationStartedEventWithType(spec.AgentID, truncateTaskPreview(spec.Task, 120), spec.ParentCallID, modelAlias, string(agentType))
	event = output.WithAgentScope(event, spec.AgentID)
	event = output.WithAgentTypeScope(event, string(agentType))
	events.Emit(event)
}

func emitDelegateStopped(events output.EventSink, spec Spec, agentType AgentType) {
	if events == nil {
		return
	}
	event := output.NewStopReasonEvent(0, "cancelled", nil)
	event = output.WithAgentScope(event, spec.AgentID)
	event = output.WithAgentTypeScope(event, string(agentType))
	events.Emit(event)
}

func emitDelegateFailed(events output.EventSink, spec Spec, agentType AgentType, errMsg string) {
	if events == nil {
		return
	}
	// Setup-failure sites call this before the child ever ran, so advisor usage
	// is always zero here; post-run failures set advisor fields directly (see
	// SpawnDelegate).
	event := output.NewDelegationFailedEvent(output.DelegationFailedParams{
		AgentID:     spec.AgentID,
		TaskPreview: truncateTaskPreview(spec.Task, 120),
		Error:       errMsg,
	})
	event = output.WithAgentScope(event, spec.AgentID)
	event = output.WithAgentTypeScope(event, string(agentType))
	events.Emit(event)
}

// advisorFieldsOf extracts the advisor budget/uses/denied counters from a
// delegation Result carried in a tool.ExecutionResult, returning zeros when
// the value isn't a Result.
func advisorFieldsOf(result tool.ExecutionResult) (budget, uses, denied int) {
	if dr, ok := result.Value.(Result); ok {
		return dr.AdvisorBudget, dr.AdvisorUses, dr.AdvisorDenied
	}
	return 0, 0, 0
}

func cancelledBeforeDispatchResult(agentID string) tool.ExecutionResult {
	result := Result{
		AgentID:          agentID,
		Status:           StatusCancelled,
		SessionResumable: false,
		Summary:          "delegation cancelled before dispatch",
	}
	return tool.ExecutionResult{
		Value: result,
		Retention: &tool.ToolRetention{
			Kind:    tool.RetentionKindDelegateSummary,
			Summary: result.Summary,
			AgentID: result.AgentID,
			Status:  string(result.Status),
		},
	}
}

// SpawnDelegate executes a child agent with the given specification and runner.
// It always runs a follow-up summarisation turn after successful completion and
// returns the full visible output plus hidden retention metadata.
//
//nolint:gocyclo // delegation lifecycle branches cover setup, execution, remediation, and retention.
func SpawnDelegate(ctx context.Context, spec Spec, req agent.RunRequest, runner AgentRunner, events output.EventSink, logger *TraceLogger, opts ...spawnOption) (tool.ExecutionResult, agent.RunState, TokenUsage, error) {
	var o spawnOptions
	for _, opt := range opts {
		opt(&o)
	}

	tc := newTraceCollector(spec.AgentID, spec.Task)

	childCtx := ctx
	var cancel context.CancelFunc
	if spec.Limits.Timeout > 0 {
		childCtx, cancel = context.WithTimeout(ctx, spec.Limits.Timeout)
		defer cancel()
		tc.add("setup", "timeout applied", map[string]any{"timeout": spec.Limits.Timeout.String()})
	}

	tc.add("start", "delegation started", map[string]any{
		"max_turns":   req.Limits.MaxTurns,
		"max_tokens":  req.Limits.MaxTokens,
		"has_timeout": spec.Limits.Timeout > 0,
	})

	req.TurnBudgetNotice = turnBudgetNoticeFunc(maxDelegateExtensions)
	state, err := runner.Run(childCtx, req)
	if err != nil && o.onChildDone != nil {
		o.onChildDone()
	}

	tc.add("child_run_complete", "initial run finished", runStateFields(childCtx, state, err))

	if err != nil {
		runUsage := tokenUsageOf(state)
		failedResult := failedDelegateExecution(spec, state, runUsage, err, tc, logger)
		if events != nil {
			budget, uses, denied := advisorFieldsOf(failedResult)
			events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
				AgentID:       spec.AgentID,
				TaskPreview:   truncateTaskPreview(spec.Task, 120),
				Error:         err.Error(),
				AdvisorBudget: budget,
				AdvisorUses:   uses,
				AdvisorDenied: denied,
			}))
		}
		return failedResult, state, runUsage, nil
	}

	state, runUsage, extensionsGranted, extErr := runChildToCompletion(childCtx, req, runner, spec.Limits.MaxTurns, events, tc, state, spec.AgentID)
	if o.onChildDone != nil {
		o.onChildDone()
	}
	if extErr != nil {
		failedResult := failedDelegateExecution(spec, state, runUsage, extErr, tc, logger)
		if events != nil {
			budget, uses, denied := advisorFieldsOf(failedResult)
			events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
				AgentID:       spec.AgentID,
				TaskPreview:   truncateTaskPreview(spec.Task, 120),
				Error:         extErr.Error(),
				AdvisorBudget: budget,
				AdvisorUses:   uses,
				AdvisorDenied: denied,
			}))
		}
		return failedResult, state, runUsage, nil
	}

	state, runUsage, result := applyRemediationResult(childCtx, spec, req, runner, state, runUsage, o.remediation, tc)
	result.AdvisorBudget = spec.AdvisorBudget

	tc.add("result", "status mapped", map[string]any{
		"status":             string(result.Status),
		"stop_reason":        string(state.StopReason),
		"turns_used":         result.TurnCount,
		"tokens_used":        result.TokenCount,
		"extensions_granted": extensionsGranted,
		"has_output":         strings.TrimSpace(result.Output) != "",
	})

	summaryCtx, summaryCancel := context.WithTimeout(childCtx, 30*time.Second)
	defer summaryCancel()
	summaryText, summaryUsage := retainedDelegateSummary(summaryCtx, runner, req, state)
	result.TokenCount += summaryUsage.OutputTokens
	result.InputTokens += summaryUsage.InputTokens
	result.CacheReadTokens += summaryUsage.CacheReadTokens
	result.CacheCreateTokens += summaryUsage.CacheCreateTokens
	runUsage = runUsage.Add(summaryUsage)
	tc.add("result_final", "usage folded (incl. summary)", map[string]any{"tokens_used": result.TokenCount, "status": string(result.Status)})
	if summaryText == "" {
		needsSynthetic := result.Status == StatusCancelled ||
			(strings.TrimSpace(result.Output) == "" && countToolCalls(state.Conversation) > 0)

		if needsSynthetic {
			// Cancellation or empty output with tool activity: the child
			// session is preserved, so synthesize a summary that tells the
			// parent the session can be resumed.
			synthetic := cancelledActivitySummary(state)
			if synthetic != "" {
				summaryText = synthetic
				tc.add("summary", "summary from cancelled activity", map[string]any{"length": len(summaryText)})
			} else {
				summaryText = cappedRetentionPreview(result.Output)
				tc.add("summary", "summary empty, using output preview", nil)
			}
			if result.Status == StatusCancelled {
				result.SessionResumable = true
			}
		} else {
			summaryText = cappedRetentionPreview(result.Output)
			tc.add("summary", "summary empty, using output preview", nil)
		}
	} else {
		tc.add("summary", "summary generated", map[string]any{"length": len(summaryText)})
	}
	result.Summary = summaryText
	result.Output = appendAdvisorSummaryLine(result.Output, result.AdvisorUses, result.AdvisorDenied)
	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			AgentID:           spec.AgentID,
			Status:            string(result.Status),
			TurnCount:         result.TurnCount,
			TokenCount:        result.TokenCount,
			ToolCallCount:     result.ToolCallCount,
			Output:            result.Output,
			InputTokens:       result.InputTokens,
			CacheReadTokens:   result.CacheReadTokens,
			CacheCreateTokens: result.CacheCreateTokens,
			AdvisorBudget:     result.AdvisorBudget,
			AdvisorUses:       result.AdvisorUses,
			AdvisorDenied:     result.AdvisorDenied,
		}))
	}
	if fields := toolCallTraceFields(spec.AgentID); fields != nil {
		tc.add("tool_calls", "per-tool-call trace recorded", fields)
	}

	logger.WriteTrace(tc)

	executionResult := tool.ExecutionResult{
		Value: result,
		Retention: &tool.ToolRetention{
			Kind:       tool.RetentionKindDelegateSummary,
			Summary:    summaryText,
			AgentID:    result.AgentID,
			Status:     string(result.Status),
			TurnCount:  result.TurnCount,
			TokenCount: result.TokenCount,
		},
	}

	return executionResult, state, runUsage, nil
}

func applyRemediationResult(
	ctx context.Context,
	spec Spec,
	req agent.RunRequest,
	runner AgentRunner,
	state agent.RunState,
	runUsage TokenUsage,
	remediation *RemediationConfig,
	tc *traceCollector,
) (agent.RunState, TokenUsage, Result) {
	var remediationResult Result
	if remediation != nil {
		state, runUsage, remediationResult, _, _ = applyRemediation(ctx, spec, req, runner, state, runUsage, remediation, tc)
	}

	total := spec.PriorTokenUsage.Add(runUsage)
	result := buildResultWithTrace(spec.AgentID, state, tc, total)
	if remediation != nil {
		result.Status = remediationResult.Status
		result.Output = remediationResult.Output
		result.Warnings = remediationResult.Warnings
		result.SessionResumable = remediationResult.SessionResumable
	}
	return state, runUsage, result
}

// runChildToCompletion executes the Delegate Extension loop.
// It re-runs the child agent when it stops with StopReasonMaxTurns and
// pending tool calls, up to maxDelegateExtensions times.
// Returns the final state, number of extensions granted, and any error
// from the last extension run.
func runChildToCompletion(
	ctx context.Context,
	req agent.RunRequest,
	runner AgentRunner,
	originalMaxTurns int,
	events output.EventSink,
	tc *traceCollector,
	state agent.RunState,
	agentID string,
) (agent.RunState, TokenUsage, int, error) {
	extensionsGranted := 0
	usage := tokenUsageOf(state)
	for ext := 0; ext < maxDelegateExtensions; ext++ {
		if !delegateNeedsExtension(state) {
			tc.add("extension_check", "extension not needed", map[string]any{
				"iteration":          ext,
				"stop_reason":        string(state.StopReason),
				"last_msg_has_tools": lastMessageHasTools(state),
			})
			break
		}
		extensionsGranted++
		tc.add("extension", "granting extension", map[string]any{
			"iteration":      ext + 1,
			"max_extensions": maxDelegateExtensions,
			"new_max_turns":  state.TurnCount + originalMaxTurns,
		})
		if events != nil {
			events.Emit(output.NewDelegationExtensionEvent(agentID, ext+1, maxDelegateExtensions))
		}
		req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation)
		req.Limits.MaxTurns = state.TurnCount + originalMaxTurns
		req.TurnBudgetNotice = turnBudgetNoticeFunc(maxDelegateExtensions - (ext + 1))
		nextState, extensionErr := runner.Run(ctx, req)

		tc.add("extension_run_complete", fmt.Sprintf("extension %d finished", ext+1), runStateFields(ctx, nextState, extensionErr))

		if extensionErr != nil {
			usage = usage.Add(tokenUsageOf(nextState))
			return state, usage, extensionsGranted, extensionErr
		}
		state = nextState
		usage = usage.Add(tokenUsageOf(nextState))
	}
	return state, usage, extensionsGranted, nil
}

func failedDelegateExecution(spec Spec, state agent.RunState, runUsage TokenUsage, err error, tc *traceCollector, logger *TraceLogger) tool.ExecutionResult {
	status := StatusFailed
	stopReason := ""
	ctxCancelled := errors.Is(err, context.Canceled)
	ctxDeadline := errors.Is(err, context.DeadlineExceeded)
	usefulActivity := delegateHasUsefulActivity(state)
	if ctxCancelled || ctxDeadline {
		if usefulActivity {
			status = StatusPartial
			if ctxCancelled {
				stopReason = "cancelled"
			} else {
				stopReason = "limit reached"
			}
		} else {
			status = StatusCancelled
			if ctxDeadline {
				stopReason = "limit reached"
			}
		}
	}

	tc.add("failed", "delegation failed", map[string]any{
		"error":             err.Error(),
		"status":            string(status),
		"context_cancelled": ctxCancelled,
		"context_deadline":  ctxDeadline,
		"child_stop_reason": string(state.StopReason),
		"child_turns":       state.TurnCount,
		"child_tokens":      runUsage.OutputTokens,
		"useful_activity":   usefulActivity,
	})

	advisorUses, advisorDenied := countAdvisorUsage(state.Conversation)
	result := Result{
		AgentID:           spec.AgentID,
		Status:            status,
		TurnCount:         state.TurnCount,
		TokenCount:        runUsage.OutputTokens,
		InputTokens:       runUsage.InputTokens,
		CacheReadTokens:   runUsage.CacheReadTokens,
		CacheCreateTokens: runUsage.CacheCreateTokens,
		ToolCallCount:     countToolCalls(state.Conversation),
		StopReason:        stopReason,
		SessionResumable:  ctxCancelled || ctxDeadline,
		AdvisorBudget:     spec.AdvisorBudget,
		AdvisorUses:       advisorUses,
		AdvisorDenied:     advisorDenied,
	}
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		result.Output = msg.Content
	}

	summaryText := failedDelegateSummaryText(err, state)
	result.Summary = summaryText

	if fields := toolCallTraceFields(spec.AgentID); fields != nil {
		tc.add("tool_calls", "per-tool-call trace recorded", fields)
	}

	logger.WriteTrace(tc)

	return tool.ExecutionResult{
		Value: result,
		Retention: &tool.ToolRetention{
			Kind:       tool.RetentionKindDelegateSummary,
			Summary:    summaryText,
			AgentID:    result.AgentID,
			Status:     string(result.Status),
			TurnCount:  result.TurnCount,
			TokenCount: result.TokenCount,
		},
	}
}

func delegateHasUsefulActivity(state agent.RunState) bool {
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok && strings.TrimSpace(msg.Content) != "" {
		return true
	}
	return countToolCalls(state.Conversation) > 0
}

func failedDelegateSummaryText(err error, state agent.RunState) string {
	parts := []string{fmt.Sprintf("delegation failed: %s", err.Error())}
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		if prev := strings.TrimSpace(msg.Content); prev != "" {
			parts = append(parts, "previous output: "+cappedRetentionPreview(prev))
		}
	}
	if toolCount := countToolCalls(state.Conversation); toolCount > 0 {
		parts = append(parts, fmt.Sprintf("activity before failure: %d tool call(s)", toolCount))
	}
	// Cancellation preserves the child session; surface that to the parent so it
	// does not conclude the session is gone and start a new delegation.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		parts = append(parts, "the child session is preserved and can be resumed with follow_up using the same agent_id")
	}
	return truncateUTF8(strings.Join(parts, "\n"))
}

func retainedDelegateSummary(ctx context.Context, runner AgentRunner, req agent.RunRequest, state agent.RunState) (string, TokenUsage) {
	summaryReq := req
	summaryReq.Events = nil
	summaryReq.Limits.MaxTurns = 1
	summaryReq.Limits.TurnTimeout = 0
	summaryReq.Tools = nil
	summaryReq.Executor = summaryOnlyExecutor{}
	// The checkpoint's 70%-of-MaxTurns threshold floors to 0 turns at
	// MaxTurns=1, which would misfire on every summary turn and inject the
	// notice right before the model's retention summary. The summary turn
	// isn't part of the extension flow, so it never needs this signal.
	summaryReq.TurnBudgetNotice = nil
	rawConv := agent.ToReplaySafeProviderMessages(state.Conversation)
	for i := range rawConv {
		rawConv[i].Turn = 0
	}
	summaryReq.Prompt.Conversation = rawConv
	summaryReq.Prompt.Conversation = append(summaryReq.Prompt.Conversation, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: fmt.Sprintf("Summarize the assistant response you just gave for retention only. Keep it under %d characters. Include key findings, paths, decisions, risks, and the next action when relevant. Do not address the parent and do not add new instructions.", delegateRetentionSummaryMaxRunes),
	})

	summaryState, err := runner.Run(ctx, summaryReq)
	usage := tokenUsageOf(summaryState)
	if err != nil {
		return "", usage
	}
	summaryOutput := ""
	if msg, ok := agent.LastAssistantMessage(summaryState.Conversation); ok {
		summaryOutput = strings.TrimSpace(msg.Content)
	}
	summaryOutput = strings.TrimSpace(summaryOutput)
	if summaryOutput == "" {
		return "", usage
	}
	return truncateUTF8(summaryOutput), usage
}

func cancelledActivitySummary(state agent.RunState) string {
	toolCount := countToolCalls(state.Conversation)
	if toolCount == 0 {
		// No tool activity before the cancellation. The child session is still
		// preserved by SpawnDelegate, so the parent can resume it with follow_up.
		// Spell that out so the parent does not conclude the session is gone.
		return truncateUTF8("cancelled before any work; the child session is preserved and can be resumed with follow_up using the same agent_id")
	}
	msg, ok := agent.LastAssistantMessage(state.Conversation)
	if !ok || len(msg.ToolCalls) == 0 {
		return truncateUTF8(fmt.Sprintf("cancelled after %d turns, %d tool call(s); the child session is preserved and can be resumed with follow_up", state.TurnCount, toolCount))
	}
	last := msg.ToolCalls[len(msg.ToolCalls)-1]
	argsPreview := ""
	if len(last.Arguments) > 0 {
		pairs := make([]string, 0, len(last.Arguments))
		for k, v := range last.Arguments {
			pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
		}
		argsPreview = strings.Join(pairs, ", ")
	}
	summary := fmt.Sprintf("cancelled after %d turns, %d tool call(s); last activity: %s(%s); the child session is preserved and can be resumed with follow_up",
		state.TurnCount, toolCount, last.Name, argsPreview)
	return truncateUTF8(summary)
}

func cappedRetentionPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(empty output)"
	}
	return truncateUTF8(text)
}

func truncateUTF8(text string) string {
	const maxRunes = delegateRetentionSummaryMaxRunes
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes < 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

type summaryOnlyExecutor struct{}

func (summaryOnlyExecutor) Execute(context.Context, string, string, map[string]any) (any, error) {
	return nil, fmt.Errorf("delegate summary turn does not permit tools")
}

// runStateFields builds trace fields from a child run's outcome.
func runStateFields(ctx context.Context, state agent.RunState, err error) map[string]any {
	fields := map[string]any{
		"stop_reason": string(state.StopReason),
		"turns":       state.TurnCount,
		"tokens":      state.TokenCount,
	}
	if err != nil {
		fields["error"] = err.Error()
		fields["is_context_cancelled"] = errors.Is(err, context.Canceled)
		fields["is_context_deadline"] = errors.Is(err, context.DeadlineExceeded)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		fields["ctx_err"] = ctxErr.Error()
	}
	fields["last_msg_has_tools"] = lastMessageHasTools(state)
	return fields
}

func lastMessageHasTools(state agent.RunState) bool {
	msg, ok := agent.LastAssistantMessage(state.Conversation)
	if !ok {
		return false
	}
	return len(msg.ToolCalls) > 0
}
