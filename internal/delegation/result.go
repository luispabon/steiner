package delegation

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// BuildResult constructs a Result from an agent.RunState.
// Maps StopReason to Status and captures state metrics.
func BuildResult(agentID string, state agent.RunState) Result {
	return buildResultInternal(agentID, state, nil, tokenUsageOf(state))
}

// buildResultWithTrace is the trace-aware variant used by SpawnDelegate.
func buildResultWithTrace(agentID string, state agent.RunState, tc *traceCollector, cache TokenUsage) Result {
	return buildResultInternal(agentID, state, tc, cache)
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

func buildResultInternal(agentID string, state agent.RunState, tc *traceCollector, cache TokenUsage) Result {
	output := ""
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		output = msg.Content
	}

	result := Result{
		AgentID:           agentID,
		Output:            output,
		TurnCount:         state.TurnCount,
		TokenCount:        cache.OutputTokens,
		ToolCallCount:     countToolCalls(state.Conversation),
		InputTokens:       cache.InputTokens,
		CacheReadTokens:   cache.CacheReadTokens,
		CacheCreateTokens: cache.CacheCreateTokens,
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

// ProjectToolResult returns the compact provider-facing delegation result.
func (r Result) ProjectToolResult() agent.DelegationResultEnvelope {
	envelope := agent.DelegationResultEnvelope{Output: r.Output}
	if r.persisted && r.AgentID != "" {
		envelope.Continuation = &agent.DelegationContinuation{AgentID: r.AgentID}
	}
	switch {
	case r.Status == StatusComplete:
	case r.Status == StatusPartial:
		envelope.Status = "partial"
		envelope.Reason = projectionReason(r)
	case r.Status == StatusCancelled:
		envelope.Status = "cancelled"
	case r.Status == StatusFailed:
		envelope.Status = "failed"
		envelope.Reason = "unknown failure"
	}
	return envelope
}

func projectionReason(r Result) string {
	if r.StopReason == "cancelled" {
		return "cancelled"
	}
	return "limit reached"
}

func (r *Result) clearPersistence() {
	if r != nil {
		r.persisted = false
	}
}
