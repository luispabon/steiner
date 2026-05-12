package delegation

import (
	"github.com/luispabon/steiner/internal/agent"
)

// BuildResult constructs a DelegationResult from an agent.RunState and DelegationSpec.
// Maps StopReason to DelegationStatus and captures state metrics.
func BuildResult(agentID string, state agent.RunState, spec DelegationSpec) DelegationResult {
	// Extract last assistant message from conversation
	output := ""
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		output = msg.Content
	}

	result := DelegationResult{
		AgentID:    agentID,
		Output:     output,
		TurnCount:  state.TurnCount,
		TokenCount: state.TokenCount,
	}

	// Map StopReason to DelegationStatus
	switch string(state.StopReason) {
	case "complete":
		result.Status = StatusComplete
	case "error":
		result.Status = StatusFailed
	case "cancelled":
		result.Status = StatusCancelled
	case "max_turns", "max_tokens":
		result.Status = StatusPartial
		result.StopReason = string(state.StopReason)
	default:
		result.Status = StatusComplete
	}

	return result
}
