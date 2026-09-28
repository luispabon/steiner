package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// usageLimitDelegateNotice tells the parent how to proceed when a child hit a
// provider usage limit. It deliberately names neither times nor profiles.
const usageLimitDelegateNotice = "To continue now, delegate a fresh sub-agent; new sub-agents use the currently configured model. If that also reports a usage limit, stop delegating and tell the user."

// turnBudgetNoticeFunc builds an agent.RunRequest.TurnBudgetNotice closure.
func turnBudgetNoticeFunc() func(turnsUsed, maxTurns int) string {
	return func(turnsUsed, maxTurns int) string {
		return fmt.Sprintf("You have used %d of %d turns (%d remaining). Finish the highest-value remaining work now, then report status and what is left, rather than continuing to explore.",
			turnsUsed, maxTurns, maxTurns-turnsUsed)
	}
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
		CallID:      spec.ParentCallID,
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
		Reason:           "delegation cancelled before dispatch",
	}
	return tool.ExecutionResult{
		Value: result,
		Retention: &tool.ToolRetention{
			Kind:    tool.RetentionKindDelegateSummary,
			AgentID: result.AgentID,
			Status:  string(result.Status),
		},
	}
}

// SpawnDelegate executes a child agent with the given specification and runner.
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

	req.TurnBudgetNotice = turnBudgetNoticeFunc()
	state, err := runner.Run(childCtx, req)
	if o.onChildDone != nil {
		o.onChildDone()
	}

	runUsage := tokenUsageOf(state)
	tc.add("child_run_complete", "child run finished", runStateFields(childCtx, state, err))

	if err != nil {
		failedResult := finalizeDelegateFailure(spec, state, runUsage, err, events, tc, logger)

		return failedResult, state, runUsage, nil
	}

	state, runUsage, result := applyRemediationResult(childCtx, spec, req, runner, state, runUsage, o.remediation, tc)
	result.AdvisorBudget = spec.AdvisorBudget

	tc.add("result", "status mapped", map[string]any{
		"status":      string(result.Status),
		"stop_reason": string(state.StopReason),
		"turns_used":  result.TurnCount,
		"tokens_used": result.TokenCount,
		"has_output":  strings.TrimSpace(result.Output) != "",
	})

	tc.add("result_final", "final result", map[string]any{"tokens_used": result.TokenCount, "status": string(result.Status)})
	if result.Status == StatusCancelled {
		// Cancellation: a reason that tells the parent the session can be
		// resumed with follow_up.
		result.Reason = cancelledDelegateReason(state)
		result.SessionResumable = true
	}
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

func finalizeDelegateFailure(spec Spec, state agent.RunState, runUsage TokenUsage, err error, events output.EventSink, tc *traceCollector, logger *TraceLogger) tool.ExecutionResult {
	result := failedDelegateExecution(spec, state, runUsage, err, tc, logger)
	if events != nil {
		budget, uses, denied := advisorFieldsOf(result)
		events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
			AgentID:       spec.AgentID,
			CallID:        spec.ParentCallID,
			TaskPreview:   truncateTaskPreview(spec.Task, 120),
			Error:         err.Error(),
			AdvisorBudget: budget,
			AdvisorUses:   uses,
			AdvisorDenied: denied,
		}))
	}
	return result
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

	result.Reason = failedDelegateReason(err, state)

	if fields := toolCallTraceFields(spec.AgentID); fields != nil {
		tc.add("tool_calls", "per-tool-call trace recorded", fields)
	}

	logger.WriteTrace(tc)

	return tool.ExecutionResult{
		Value: result,
		Retention: &tool.ToolRetention{
			Kind:       tool.RetentionKindDelegateSummary,
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

// failedDelegateReason builds the failure explanation surfaced to the parent
// model, including the tool activity recorded before the failure and the
// preserved-session resume notice for context cancellation.
func failedDelegateReason(err error, state agent.RunState) string {
	parts := []string{fmt.Sprintf("delegation failed: %s", err.Error())}
	if toolCount := countToolCalls(state.Conversation); toolCount > 0 {
		parts = append(parts, fmt.Sprintf("activity before failure: %d tool call(s)", toolCount))
	}
	// Cancellation preserves the child session; surface that to the parent so it
	// does not conclude the session is gone and start a new delegation.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		parts = append(parts, "the child session is preserved and can be resumed with follow_up using the same agent_id")
	}
	if _, ok := provider.AsUsageLimit(err); ok {
		parts = append(parts, usageLimitDelegateNotice)
	}
	return strings.Join(parts, "\n")
}

// cancelledDelegateReason builds the deterministic cancellation explanation
// surfaced to the parent model. It names the tool call count and the last tool
// without previewing arguments, and always carries the resume notice.
func cancelledDelegateReason(state agent.RunState) string {
	toolCount := countToolCalls(state.Conversation)
	if toolCount == 0 {
		// No tool activity before the cancellation. The child session is still
		// preserved by SpawnDelegate, so the parent can resume it with follow_up.
		// Spell that out so the parent does not conclude the session is gone.
		return "cancelled before any work; the child session is preserved and can be resumed with follow_up using the same agent_id"
	}
	msg, ok := agent.LastAssistantMessage(state.Conversation)
	if !ok || len(msg.ToolCalls) == 0 {
		return fmt.Sprintf("cancelled after %d turns, %d tool call(s); the child session is preserved and can be resumed with follow_up", state.TurnCount, toolCount)
	}
	last := msg.ToolCalls[len(msg.ToolCalls)-1]
	return fmt.Sprintf("cancelled after %d turns, %d tool call(s); last activity: %s; the child session is preserved and can be resumed with follow_up",
		state.TurnCount, toolCount, last.Name)
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
