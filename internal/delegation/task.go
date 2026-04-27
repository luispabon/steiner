package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// AgentRunner defines the contract for executing an agent with a given request.
type AgentRunner interface {
	// Run executes the agent and returns the final state.
	Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error)
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
// It invokes the runner with the provided request, handles oversized output via
// a summarisation turn, and emits delegation lifecycle events.
func SpawnDelegate(ctx context.Context, spec DelegationSpec, req agent.RunRequest, runner AgentRunner, events output.EventSink) (DelegationResult, error) {
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
		return result, err
	}

	result := BuildResult(spec.AgentID, state, spec)
	boundedOutput := result.Output

	// Check if output is oversized — if so, request a summary turn
	if checkOutputSize(result.Output, spec.Limits.OutputLimitTokens) {
		summaryReq := req
		summaryReq.Limits.MaxTurns = 1
		summaryConversation := make([]provider.Message, 0, len(req.Prompt.Conversation)+2)
		summaryConversation = append(summaryConversation, req.Prompt.Conversation...)
		if assistantMsg, ok := agent.LastAssistantMessage(state.Conversation); ok {
			summaryConversation = append(summaryConversation, provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: assistantMsg.Content,
			})
		}
		summaryConversation = append(summaryConversation, provider.Message{
			Role: provider.MessageRoleUser,
			Content: fmt.Sprintf(
				"Your previous response was too long. Please summarize the assistant response you just gave. Keep the summary to approximately %d tokens or fewer.",
				spec.Limits.OutputLimitTokens,
			),
		})
		summaryReq.Prompt.Conversation = summaryConversation

		summaryState, summaryErr := runner.Run(childCtx, summaryReq)
		if summaryErr == nil {
		summaryOutput := ""
		if msg, ok := agent.LastAssistantMessage(summaryState.Conversation); ok {
			summaryOutput = msg.Content
		}
			if summaryOutput != "" {
				boundedOutput = summaryOutput
				result.Summary = summaryOutput
			}
		}
	}

	if checkOutputSize(boundedOutput, spec.Limits.OutputLimitTokens) {
		boundedOutput = truncateOutputToLimit(boundedOutput, spec.Limits.OutputLimitTokens)
		if result.Summary != "" {
			result.Summary = boundedOutput
		}
	}
	result.Output = boundedOutput

	// Emit delegation complete
	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(spec.AgentID, string(result.Status), result.TurnCount, result.TokenCount))
	}

	return result, nil
}

func truncateOutputToLimit(s string, maxTokens int) string {
	if maxTokens <= 0 {
		return s
	}
	maxBytes := maxTokens * 4
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes < 3 {
		return s[:maxBytes]
	}
	return s[:maxBytes-3] + "..."
}
