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
	// Emit delegation started
	if events != nil {
		events.Emit(output.NewDelegationStartedEvent(spec.AgentID, truncateTaskPreview(spec.Task, 120)))
	}

	// Run the child agent
	state, err := runner.Run(ctx, req)
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

	// Check if output is oversized — if so, request a summary turn
	if CheckOutputSize(result.Output, spec.Limits.OutputLimitTokens) {
		summaryReq := req
		summaryReq.Limits.MaxTurns = 1
		summaryConversation := make([]provider.Message, len(req.Prompt.Conversation))
		copy(summaryConversation, req.Prompt.Conversation)
		summaryConversation = append(summaryConversation, provider.Message{
			Role:    provider.MessageRoleUser,
			Content: "Your previous response was too long. Please provide a concise summary.",
		})
		summaryReq.Prompt.Conversation = summaryConversation

		summaryState, summaryErr := runner.Run(ctx, summaryReq)
		if summaryErr == nil {
			summaryOutput := ""
			for i := len(summaryState.Conversation) - 1; i >= 0; i-- {
				msg := summaryState.Conversation[i]
				if msg.Role == agent.MessageRoleAssistant {
					summaryOutput = msg.Content
					break
				}
			}
			if CheckOutputSize(summaryOutput, spec.Limits.OutputLimitTokens) {
				summaryOutput = fmt.Sprintf("%s\n[truncated: exceeded output limit]", summaryOutput[:spec.Limits.OutputLimitTokens*4])
			}
			result.Summary = summaryOutput
		}
	}

	// Emit delegation complete
	if events != nil {
		events.Emit(output.NewDelegationCompleteEvent(spec.AgentID, string(result.Status), result.TurnCount, result.TokenCount))
	}

	return result, nil
}
