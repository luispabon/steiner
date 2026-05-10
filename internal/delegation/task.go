package delegation

import (
	"context"
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
	if len(s) <= max {
		return s
	}
	// Ensure we leave room for the ellipsis
	if max < 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// SpawnDelegate executes a child agent with the given specification and runner.
// It always runs a follow-up summarisation turn after successful completion and
// returns the full visible output plus hidden retention metadata.
func SpawnDelegate(ctx context.Context, spec DelegationSpec, req agent.RunRequest, runner AgentRunner, events output.EventSink) (tool.ExecutionResult, error) {
	childCtx := ctx
	var cancel context.CancelFunc
	if spec.Limits.Timeout > 0 {
		childCtx, cancel = context.WithTimeout(ctx, spec.Limits.Timeout)
		defer cancel()
	}

	// Emit delegation started
	if events != nil {
		events.Emit(output.NewDelegationStartedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120)))
	}

	// Run the child agent
	originalMaxTurns := req.Limits.MaxTurns
	state, err := runner.Run(childCtx, req)
	if err != nil {
		result := DelegationResult{
			AgentID: spec.AgentID,
			Status:  StatusFailed,
			Error:   err.Error(),
		}
		if events != nil {
			events.Emit(output.NewDelegationFailedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120), err.Error()))
		}
		return tool.ExecutionResult{Value: result}, err
	}

	for ext := 0; ext < maxDelegateExtensions; ext++ {
		if !delegateNeedsExtension(state) {
			break
		}
		if events != nil {
			events.Emit(output.NewDelegationExtensionEvent(spec.AgentID, ext+1, maxDelegateExtensions))
		}
		req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation)
		req.Limits.MaxTurns = state.TurnCount + originalMaxTurns
		state, err = runner.Run(childCtx, req)
		if err != nil {
			break
		}
	}

	result := BuildResult(spec.AgentID, state, spec)
	summaryText := retainedDelegateSummary(childCtx, runner, req, state)
	if summaryText == "" {
		summaryText = cappedRetentionPreview(result.Output)
	}

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

	// Emit delegation complete
	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(spec.AgentID, string(result.Status), result.TurnCount, result.TokenCount, result.Output))
	}

	return executionResult, nil
}

func retainedDelegateSummary(ctx context.Context, runner AgentRunner, req agent.RunRequest, state agent.RunState) string {
	summaryReq := req
	summaryReq.Limits.MaxTurns = 1
	summaryReq.Tools = nil
	summaryReq.Executor = summaryOnlyExecutor{}
	summaryReq.Prompt.Conversation = append([]provider.Message(nil), req.Prompt.Conversation...)
	if assistantMsg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		summaryReq.Prompt.Conversation = append(summaryReq.Prompt.Conversation, provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: assistantMsg.Content,
		})
	}
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
