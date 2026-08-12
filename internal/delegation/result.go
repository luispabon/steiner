package delegation

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// BuildResult constructs a DelegationResult from an agent.RunState.
// Maps StopReason to DelegationStatus and captures state metrics.
func BuildResult(agentID string, state agent.RunState) DelegationResult {
	return buildResultInternal(agentID, state, nil)
}

// buildResultWithTrace is the trace-aware variant used by SpawnDelegate.
func buildResultWithTrace(agentID string, state agent.RunState, tc *traceCollector) DelegationResult {
	return buildResultInternal(agentID, state, tc)
}

func countToolCalls(conversation []agent.Message) int {
	n := 0
	for _, msg := range conversation {
		if msg.Role == agent.MessageRoleAssistant {
			n += len(msg.ToolCalls)
		}
	}
	return n
}

func buildResultInternal(agentID string, state agent.RunState, tc *traceCollector) DelegationResult {
	output := ""
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		output = msg.Content
	}

	result := DelegationResult{
		AgentID:           agentID,
		Output:            output,
		TurnCount:         state.TurnCount,
		TokenCount:        state.TokenCount,
		ToolCallCount:     countToolCalls(state.Conversation),
		InputTokens:       state.InputTokens,
		CacheReadTokens:   state.CacheReadTokens,
		CacheCreateTokens: state.CacheCreateTokens,
	}

	rawReason := string(state.StopReason)
	switch rawReason {
	case "complete":
		result.Status = StatusComplete
	case "error":
		result.Status = StatusFailed
	case "cancelled":
		if state.TurnCount > 0 && (strings.TrimSpace(output) != "" || countToolCalls(state.Conversation) > 0) {
			result.Status = StatusPartial
			result.StopReason = "cancelled"
		} else {
			result.Status = StatusCancelled
		}
		result.SessionResumable = true
	case "max_turns", "max_tokens":
		result.Status = StatusPartial
		result.StopReason = rawReason
	default:
		result.Status = StatusComplete
	}

	if tc != nil {
		tc.add("status_mapping", fmt.Sprintf("%s -> %s", rawReason, string(result.Status)), map[string]any{
			"raw_stop_reason": rawReason,
			"mapped_status":   string(result.Status),
			"turns":           state.TurnCount,
			"has_output":      strings.TrimSpace(output) != "",
		})
	}

	return result
}
