package delegation

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
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

// effectiveAdvisorBudget returns the per-child advisor budget to stamp onto a
// Spec, treating a zero-or-negative configured budget the same as advisor
// being unavailable.
func effectiveAdvisorBudget(advisorAvailable bool, budget int) int {
	if advisorAvailable && budget > 0 {
		return budget
	}
	return 0
}

// appendAdvisorSummaryLine appends an "advisor: N used, M denied" line to
// output when advisor usage is non-zero, so the parent model and the TUI see
// the same text. Returns output unchanged when uses and denied are both zero.
func appendAdvisorSummaryLine(output string, uses, denied int) string {
	if uses == 0 && denied == 0 {
		return output
	}
	status := ""
	if denied > 0 {
		status = " (budget exhausted)"
	}
	line := fmt.Sprintf("advisor: %d used, %d denied%s", uses, denied, status)
	if strings.TrimSpace(output) == "" {
		return line
	}
	return output + "\n" + line
}

// interruptedOutputNotice explains that a child's output is the partial
// assistant message committed when its model call ended mid-message. It returns
// an empty string when the last assistant message completed normally.
func interruptedOutputNotice(state agent.RunState) string {
	msg, ok := agent.LastAssistantMessage(state.Conversation)
	if !ok || !msg.Incomplete {
		return ""
	}
	return fmt.Sprintf("output is the partial assistant message from turn %d; the model call ended mid-message and the text may be truncated", msg.Turn)
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
	case "error", "usage_limit":
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
		result.Status = StatusFailed
		result.StopReason = rawReason
		result.Reason = fmt.Sprintf("unknown stop reason: %s", rawReason)
	}

	// Only a partial projection reads the notice; failed and cancelled results
	// carry their own reason.
	if result.Status == StatusPartial {
		result.truncationNotice = interruptedOutputNotice(state)
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

// continuationAvailable reports whether the child session is still persisted
// and can be named in the projection. The follow_up guidance uses the same
// condition as the continuation field.
func (r Result) continuationAvailable() bool {
	return r.persisted && r.AgentID != ""
}

// followUpBudgetState records whether a persisted child is known to have
// follow-up resumes left. Zero means no handler recorded the budget, so a
// failed projection adds no follow-up guidance.
type followUpBudgetState uint8

const (
	// followUpBudgetAvailable means another follow_up resume is allowed.
	followUpBudgetAvailable followUpBudgetState = iota + 1
	// followUpBudgetExhausted means the child used all of MaxFollowUps resumes.
	followUpBudgetExhausted
)

// followUpBudgetFor returns the budget state for a child that used count
// follow-ups out of max. follow_up refuses at count >= max, so eligibility is
// count < max; an initial save starts at count zero.
func followUpBudgetFor(count, max int) followUpBudgetState {
	if count < max {
		return followUpBudgetAvailable
	}
	return followUpBudgetExhausted
}

// setFollowUpBudget stamps the budget state onto the Result carried by result.
// Only handler paths that also mark the session persisted call it.
func setFollowUpBudget(result *tool.ExecutionResult, state followUpBudgetState) {
	dr, ok := result.Value.(Result)
	if !ok {
		return
	}
	dr.followUpBudget = state
	result.Value = dr
}

// repeatedToolFailureFollowUpHint tells the parent how to resume a child that
// stopped on a repeated identical tool error. It names the corrective message
// the child needs and the limits that still apply, without promising success.
const repeatedToolFailureFollowUpHint = "to continue, call follow_up with the same agent_id and a message correcting the repeated tool error; the prior conversation is retained. If applicable, its worktree must still exist and the execution mode must allow it; further attempts remain subject to the follow-up limit. If follow_up is refused, delegate a fresh sub-agent."

// repeatedToolFailureExhaustedHint tells the parent to delegate fresh when the
// child has no follow-up resumes left. Projection cannot inspect the worktree or
// mode, so it states those refusals as conditions instead of checking them.
const repeatedToolFailureExhaustedHint = "the follow-up limit is exhausted; delegate a fresh sub-agent with the context it needs. A follow_up is also refused when its worktree, if applicable, is gone or the execution mode blocks the work, so delegate fresh then too."

// ProjectToolResult returns the compact provider-facing delegation result.
func (r Result) ProjectToolResult() agent.DelegationResultEnvelope {
	envelope := agent.DelegationResultEnvelope{Output: r.Output}
	if r.continuationAvailable() {
		envelope.Continuation = &agent.DelegationContinuation{AgentID: r.AgentID}
	}
	if r.providerWorktreePath != "" {
		envelope.WorktreePath = r.providerWorktreePath
	}
	switch r.Status {
	case StatusComplete:
	case StatusPartial:
		envelope.Status = "partial"
		envelope.Reason = projectionReason(r)
	case StatusCancelled:
		envelope.Status = "cancelled"
		envelope.Reason = r.Reason
		if envelope.Reason == "" {
			envelope.Reason = cancellationProjectionReason(r)
		}
	case StatusFailed:
		envelope.Status = "failed"
		envelope.Reason = failedProjectionReason(r)
	}
	return envelope
}

// failedProjectionReason builds the model-facing reason for a failed result:
// the failure explanation, or "unknown failure" when empty, plus guidance for a
// repeated tool failure whose session is persisted. A known available budget
// names follow_up; a known exhausted budget points at a fresh sub-agent; an
// unrecorded budget adds nothing.
func failedProjectionReason(r Result) string {
	reason := r.Reason
	if reason == "" {
		reason = "unknown failure"
	}
	if !r.repeatedToolFailure || !r.continuationAvailable() {
		return reason
	}
	switch r.followUpBudget {
	case followUpBudgetAvailable:
		return reason + "; " + repeatedToolFailureFollowUpHint
	case followUpBudgetExhausted:
		return reason + "; " + repeatedToolFailureExhaustedHint
	default:
		return reason
	}
}

// projectionReason builds the model-facing reason for a partial result: the
// short base token for limit stops and cancellations, plus the truncation
// notice when the output is an interrupted partial message. Any other child
// explanation stays out; the cancelled status carries it through Reason.
func projectionReason(r Result) string {
	base := "limit reached"
	if r.StopReason == "cancelled" {
		base = "cancelled"
	}
	if notice := strings.TrimSpace(r.truncationNotice); notice != "" {
		return base + "; " + notice
	}
	return base
}

func cancellationProjectionReason(r Result) string {
	if r.StopReason == "limit reached" {
		return "limit reached"
	}
	return "cancelled"
}

func (r *Result) clearPersistence() {
	if r != nil {
		r.persisted = false
	}
}
