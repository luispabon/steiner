package delegation

// RunState interface captures the essential state from agent.RunState.
// This avoids circular imports by not directly importing internal/agent.
type RunState interface {
	GetTurnCount() int
	GetTokenCount() int
	GetStopReason() string
	GetLastAssistantMessage() string
}

// BuildResult constructs a DelegationResult from a RunState and DelegationSpec.
// Maps StopReason to DelegationStatus and captures state metrics.
func BuildResult(agentID string, state RunState, spec DelegationSpec) DelegationResult {
	result := DelegationResult{
		AgentID:    agentID,
		Output:     state.GetLastAssistantMessage(),
		TurnCount:  state.GetTurnCount(),
		TokenCount: state.GetTokenCount(),
	}

	// Map StopReason to DelegationStatus
	stopReason := state.GetStopReason()
	switch stopReason {
	case "complete":
		result.Status = StatusComplete
	case "error":
		result.Status = StatusFailed
	case "cancelled":
		result.Status = StatusCancelled
	default:
		result.Status = StatusComplete
	}

	return result
}

// CheckOutputSize returns true if the output is oversized relative to the token limit.
// Uses len(output)/4 as a rough token approximation.
func CheckOutputSize(output string, limitTokens int) bool {
	if limitTokens <= 0 {
		return false
	}
	approximateTokens := len(output) / 4
	return approximateTokens > limitTokens
}
