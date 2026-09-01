package delegation

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/advisor"
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

func countAdvisorUsage(conversation []agent.Message) (uses, denied int) {
	attempts := 0
	denied = 0

	for _, msg := range conversation {
		if msg.Role == agent.MessageRoleAssistant {
			for _, call := range msg.ToolCalls {
				if call.Name == advisor.ToolName {
					attempts++
				}
			}
		}
		if msg.Role == agent.MessageRoleTool && msg.Name == advisor.ToolName {
			if strings.HasPrefix(msg.Content, advisor.BudgetExhaustedMessagePrefix) {
				denied++
			}
		}
	}

	uses = attempts - denied
	if uses < 0 {
		uses = 0
	}
	return uses, denied
}

func buildResultInternal(agentID string, state agent.RunState, tc *traceCollector, cache TokenUsage) Result {
	output := ""
	if msg, ok := agent.LastAssistantMessage(state.Conversation); ok {
		output = msg.Content
	}

	uses, denied := countAdvisorUsage(state.Conversation)
	result := Result{
		AgentID:           agentID,
		Output:            output,
		TurnCount:         state.TurnCount,
		TokenCount:        cache.OutputTokens,
		ToolCallCount:     countToolCalls(state.Conversation),
		AdvisorUses:       uses,
		AdvisorDenied:     denied,
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
	switch r.Status {
	case StatusComplete:
	case StatusPartial:
		envelope.Status = "partial"
		envelope.Reason = projectionReason(r)
	case StatusCancelled:
		envelope.Status = "cancelled"
		envelope.Reason = cancellationProjectionReason(r)
	case StatusFailed:
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

func cancellationProjectionReason(r Result) string {
	if r.StopReason == "limit reached" {
		return "limit reached"
	}
	return ""
}

func (r *Result) clearPersistence() {
	if r != nil {
		r.persisted = false
	}
}
