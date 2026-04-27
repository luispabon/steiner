package delegation

import (
	"github.com/luispabon/steiner/internal/agent"
)

// BuildResult constructs a DelegationResult from an agent.RunState and DelegationSpec.
// Maps StopReason to DelegationStatus and captures state metrics.
func BuildResult(agentID string, state agent.RunState, spec DelegationSpec) DelegationResult {
	// Extract last assistant message from conversation
	output := ""
	for i := len(state.Conversation) - 1; i >= 0; i-- {
		msg := state.Conversation[i]
		if msg.Role == agent.MessageRoleAssistant {
			output = msg.Content
			break
		}
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
		result.Status = StatusComplete
	default:
		result.Status = StatusComplete
	}

	return result
}

// checkOutputSize returns true if the output is oversized relative to the token limit.
// Uses len(output)/4 as a rough token approximation.
func checkOutputSize(output string, limitTokens int) bool {
	if limitTokens <= 0 {
		return false
	}
	approximateTokens := len(output) / 4
	return approximateTokens > limitTokens
}
