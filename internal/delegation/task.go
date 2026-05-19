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

const delegateRetentionSummaryMaxRunes = 1000

const maxDelegateExtensions = 5

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

// SpawnDelegate executes a child agent with the given specification and runner.
// It always runs a follow-up summarisation turn after successful completion and
// returns the full visible output plus hidden retention metadata.
func SpawnDelegate(ctx context.Context, spec DelegationSpec, req agent.RunRequest, runner AgentRunner, events output.EventSink, logger *TraceLogger) (tool.ExecutionResult, error) {
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

	if events != nil {
		events.Emit(output.NewDelegationStartedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120)))
	}

	originalMaxTurns := req.Limits.MaxTurns
	state, err := runner.Run(childCtx, req)

	tc.add("child_run_complete", "initial run finished", runStateFields(childCtx, state, err))

	if err != nil {
		if events != nil {
			events.Emit(output.NewDelegationFailedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120), err.Error()))
		}
		return failedDelegateExecution(spec, state, err, tc, logger), nil
	}

	extensionsGranted := 0
	for ext := 0; ext < maxDelegateExtensions; ext++ {
		needs := delegateNeedsExtension(state)
		if !needs {
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
			events.Emit(output.NewDelegationExtensionEvent(spec.AgentID, ext+1, maxDelegateExtensions))
		}
		req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation)
		req.Limits.MaxTurns = state.TurnCount + originalMaxTurns
		nextState, extensionErr := runner.Run(childCtx, req)

		tc.add("extension_run_complete", fmt.Sprintf("extension %d finished", ext+1), runStateFields(childCtx, nextState, extensionErr))

		if extensionErr != nil {
			if events != nil {
				events.Emit(output.NewDelegationFailedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120), extensionErr.Error()))
			}
			return failedDelegateExecution(spec, state, extensionErr, tc, logger), nil
		}
		state = nextState
	}

	result := buildResultWithTrace(spec.AgentID, state, spec, tc)

	tc.add("result", "status mapped", map[string]any{
		"status":             string(result.Status),
		"stop_reason":        string(state.StopReason),
		"turns_used":         result.TurnCount,
		"tokens_used":        result.TokenCount,
		"extensions_granted": extensionsGranted,
		"has_output":         strings.TrimSpace(result.Output) != "",
	})

	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(spec.AgentID, string(result.Status), result.TurnCount, result.TokenCount, result.Output))
	}

	summaryCtx, summaryCancel := context.WithTimeout(childCtx, 30*time.Second)
	defer summaryCancel()
	summaryText := retainedDelegateSummary(summaryCtx, runner, req, state)
	if summaryText == "" {
		tc.add("summary", "summary empty, using output preview", nil)
		summaryText = cappedRetentionPreview(result.Output)
	} else {
		tc.add("summary", "summary generated", map[string]any{"length": len(summaryText)})
	}
	result.Summary = summaryText
	result.Trace = tc.result()

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

	return executionResult, nil
}

func failedDelegateExecution(spec DelegationSpec, state agent.RunState, err error, tc *traceCollector, logger *TraceLogger) tool.ExecutionResult {
	status := StatusFailed
	ctxCancelled := errors.Is(err, context.Canceled)
	ctxDeadline := errors.Is(err, context.DeadlineExceeded)
	if ctxCancelled || ctxDeadline {
		status = StatusCancelled
	}

	tc.add("failed", "delegation failed", map[string]any{
		"error":             err.Error(),
		"status":            string(status),
		"context_cancelled": ctxCancelled,
		"context_deadline":  ctxDeadline,
		"child_stop_reason": string(state.StopReason),
		"child_turns":       state.TurnCount,
		"child_tokens":      state.TokenCount,
	})

	result := DelegationResult{
		AgentID:    spec.AgentID,
		Status:     status,
		TurnCount:  state.TurnCount,
		TokenCount: state.TokenCount,
		Error:      err.Error(),
	}
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		result.Output = msg.Content
	}

	scratchpadState := lastScratchpadState(state.Conversation)
	summaryText := failedDelegateSummaryText(err, result.Output, scratchpadState)
	result.Summary = summaryText
	result.Trace = tc.result()

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

func failedDelegateSummaryText(err error, previousOutput string, scratchpadState string) string {
	parts := []string{fmt.Sprintf("delegation failed: %s", err.Error())}
	prev := strings.TrimSpace(previousOutput)
	if prev != "" {
		parts = append(parts, "previous output: "+cappedRetentionPreview(prev))
	} else if s := strings.TrimSpace(scratchpadState); s != "" {
		parts = append(parts, "scratchpad state:\n"+s)
	}
	return truncateUTF8(strings.Join(parts, "\n"), delegateRetentionSummaryMaxRunes)
}

func lastScratchpadState(conversation []agent.Message) string {
	for i := len(conversation) - 1; i >= 0; i-- {
		msg := conversation[i]
		for _, tc := range msg.ToolCalls {
			if tc.Name != "scratchpad" {
				continue
			}
			var parts []string
			for _, key := range []string{"intent", "decisions", "open", "next"} {
				if v, ok := tc.Arguments[key]; ok {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						parts = append(parts, key+": "+s)
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	return ""
}

func retainedDelegateSummary(ctx context.Context, runner AgentRunner, req agent.RunRequest, state agent.RunState) string {
	summaryReq := req
	summaryReq.Events = nil
	summaryReq.Limits.MaxTurns = 1
	summaryReq.Limits.TurnTimeout = 0
	summaryReq.Tools = nil
	summaryReq.Executor = summaryOnlyExecutor{}
	rawConv := agent.ToProviderMessages(state.Conversation)
	for i := range rawConv {
		rawConv[i].Turn = 0
	}
	summaryReq.Prompt.Conversation = rawConv
	summaryReq.Prompt.Conversation = append(summaryReq.Prompt.Conversation, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: "Summarize the assistant response you just gave for retention only. Keep it under 1000 characters. Include key findings, paths, decisions, risks, and the next action when relevant. Do not address the parent and do not add new instructions.",
	})

	summaryState, err := runner.Run(ctx, summaryReq)
	if err != nil {
		return ""
	}
	summaryOutput := ""
	if msg, ok := agent.LastAssistantMessage(summaryState.Conversation); ok {
		summaryOutput = strings.TrimSpace(msg.Content)
	}
	summaryOutput = strings.TrimSpace(summaryOutput)
	if summaryOutput == "" {
		return ""
	}
	return truncateUTF8(summaryOutput, delegateRetentionSummaryMaxRunes)
}

func cappedRetentionPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(empty output)"
	}
	return truncateUTF8(text, delegateRetentionSummaryMaxRunes)
}

func truncateUTF8(text string, maxRunes int) string {
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

func (summaryOnlyExecutor) Execute(context.Context, string, map[string]any) (any, error) {
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
