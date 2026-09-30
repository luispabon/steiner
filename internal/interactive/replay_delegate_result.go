package interactive

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

const replayNoResultMessage = "no result"

type replayAck struct {
	callID  string
	agentID string
	task    string
}

// replayAcks tracks acknowledged async sub-agents awaiting a result envelope,
// in ack order.
type replayAcks struct {
	order []replayAck
}

func (r *replayAcks) add(callID, agentID, task string) {
	r.order = append(r.order, replayAck{callID: callID, agentID: agentID, task: task})
}

func (r *replayAcks) take(callID string) (replayAck, bool) {
	for i, ack := range r.order {
		if ack.callID == callID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return ack, true
		}
	}
	return replayAck{}, false
}

func isAckStatus(status string) bool {
	return status == "running" || status == "queued"
}

// isFailedResultStatus reports whether a result envelope status replays as a
// failed delegation.
func isFailedResultStatus(status string) bool {
	switch status {
	case "failed", "lost", "cancelled", "timeout":
		return true
	}
	return false
}

// replaySubAgentResult emits the completion or failure event for one result
// envelope (already parsed), correlating it to its ack by call_id.
func (s *Session) replaySubAgentResult(parsed agent.ParsedSubAgentResult, acks *replayAcks) {
	ack, _ := acks.take(parsed.CallID)
	state := replayedDelegationState{agentID: parsed.AgentID, status: "complete"}
	body, usage := splitResultEnvelopeInner(parsed.Inner)
	state.output = body
	state.error = body
	state.turnCount, state.tokenCount = usage.turns, usage.tokens
	var decoded replayedDelegateResult
	if d, ok := decodeReplayedDelegateResult(body); ok {
		decoded = d
		applyDecodedDelegationState(&state, d)
		state.agentID = parsed.AgentID
		state.turnCount, state.tokenCount = usage.turns, usage.tokens
	}
	if isFailedResultStatus(parsed.Status) || isFailedResultStatus(state.status) {
		msg := decoded.Reason
		if msg == "" {
			msg = decoded.Error
		}
		if msg == "" {
			msg = state.output
		}
		s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
			AgentID:     state.agentID,
			AgentType:   parsed.AgentType,
			DurationMs:  usage.duration.Milliseconds(),
			TaskPreview: ack.task,
			Error:       msg,
		}))
		return
	}
	if parsed.Status != "" {
		state.status = parsed.Status
	}
	s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       state.agentID,
		AgentType:     parsed.AgentType,
		DurationMs:    usage.duration.Milliseconds(),
		Status:        state.status,
		TurnCount:     state.turnCount,
		TokenCount:    state.tokenCount,
		ToolCallCount: state.toolCallCount,
		Output:        state.output,
	}))
}

// replayUnresolvedAcks shows acknowledged sub-agents that never received a
// result envelope (crashed or pre-ledger sessions) as failed with no result.
func (s *Session) replayUnresolvedAcks(acks *replayAcks) {
	for _, ack := range acks.order {
		s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
			AgentID:     ack.agentID,
			TaskPreview: ack.task,
			Error:       replayNoResultMessage,
		}))
	}
	acks.order = nil
}

type resultEnvelopeUsage struct {
	turns    int
	tokens   int
	duration time.Duration
}

// splitResultEnvelopeInner returns the body of an envelope's inner text (after
// the usage line and its blank line) and the usage counters on that line.
func splitResultEnvelopeInner(inner string) (string, resultEnvelopeUsage) {
	var usage resultEnvelopeUsage
	_, after, ok := strings.Cut(inner, "\nusage: ")
	if !ok {
		return inner, usage
	}
	line, body, _ := strings.Cut(after, "\n\n")
	_, _ = fmt.Sscanf(line, "turns=%d tokens=%d", &usage.turns, &usage.tokens) // partial usage is fine
	if _, dur, ok := strings.Cut(line, " duration="); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(dur)); err == nil && d > 0 {
			usage.duration = d
		}
	}
	return body, usage
}

type replayedDelegateResult struct {
	AgentID           string `json:"agent_id"`
	Status            string `json:"status"`
	Output            string `json:"output"`
	Summary           string `json:"summary"`
	Reason            string `json:"reason"`
	TurnCount         int    `json:"turn_count"`
	TokenCount        int    `json:"token_count"`
	ToolCallCount     int    `json:"tool_call_count"`
	Error             string `json:"error"`
	InputTokens       int    `json:"input_tokens"`
	CacheReadTokens   int    `json:"cache_read_tokens"`
	CacheCreateTokens int    `json:"cache_create_tokens"`
	compact           bool
}

type replayedDelegationState struct {
	agentID           string
	status            string
	output            string
	error             string
	turnCount         int
	tokenCount        int
	toolCallCount     int
	inputTokens       int
	cacheReadTokens   int
	cacheCreateTokens int
}

func decodeReplayedDelegateResult(content string) (replayedDelegateResult, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return replayedDelegateResult{}, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &fields); err != nil || fields == nil {
		return replayedDelegateResult{}, false
	}
	if _, ok := fields["output"]; ok && compactEnvelopeFields(fields) {
		var compact struct {
			Output       string                        `json:"output"`
			Status       string                        `json:"status"`
			Reason       string                        `json:"reason"`
			Continuation *agent.DelegationContinuation `json:"continuation"`
		}
		if err := json.Unmarshal([]byte(content), &compact); err != nil {
			return replayedDelegateResult{}, false
		}
		result := replayedDelegateResult{Output: compact.Output, Status: compact.Status, Reason: compact.Reason, compact: true}
		if compact.Continuation != nil {
			result.AgentID = compact.Continuation.AgentID
		}
		return result, true
	}

	var result replayedDelegateResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || !fullDelegateResultFields(fields) {
		return replayedDelegateResult{}, false
	}
	return result, true
}

func compactEnvelopeFields(fields map[string]json.RawMessage) bool {
	for key := range fields {
		switch key {
		case "output", "status", "reason", "continuation", "worktree_path":
		default:
			return false
		}
	}
	return true
}

func fullDelegateResultFields(fields map[string]json.RawMessage) bool {
	for _, key := range []string{
		"agent_id", "summary", "turn_count", "token_count", "tool_call_count", "error",
		"input_tokens", "cache_read_tokens", "cache_create_tokens",
	} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

func buildReplayedDelegationState(toolCallID string, retention *agent.MessageRetention, content string) replayedDelegationState {
	state := replayedDelegationState{
		agentID: "agent-" + toolCallID,
		status:  "complete",
		output:  content,
		error:   content,
	}

	decoded, ok := decodeReplayedDelegateResult(content)
	if ok {
		if !decoded.compact && decoded.AgentID == "" && decoded.Status == "" && decoded.Summary == "" && decoded.Reason == "" && decoded.Error == "" {
			state.output = content
		} else {
			applyDecodedDelegationState(&state, decoded)
		}
	}
	applyRetainedDelegationState(&state, retention)
	if state.status == "failed" && ok {
		if decoded.Reason != "" {
			state.error = decoded.Reason
		} else if decoded.Error != "" {
			state.error = decoded.Error
		}
	}
	return state
}

func applyDecodedDelegationState(state *replayedDelegationState, decoded replayedDelegateResult) {
	if decoded.AgentID != "" {
		state.agentID = decoded.AgentID
	}
	if decoded.Status != "" {
		state.status = decoded.Status
	}
	state.output = decoded.Output
	if decoded.Error != "" {
		state.error = decoded.Error
	}
	if decoded.TurnCount > 0 {
		state.turnCount = decoded.TurnCount
	}
	if decoded.TokenCount > 0 {
		state.tokenCount = decoded.TokenCount
	}
	if decoded.ToolCallCount > 0 {
		state.toolCallCount = decoded.ToolCallCount
	}
	state.inputTokens = decoded.InputTokens
	state.cacheReadTokens = decoded.CacheReadTokens
	state.cacheCreateTokens = decoded.CacheCreateTokens
}

func applyRetainedDelegationState(state *replayedDelegationState, retention *agent.MessageRetention) {
	if retention == nil {
		return
	}
	if retention.AgentID != "" {
		state.agentID = retention.AgentID
	}
	if retention.Status != "" {
		state.status = retention.Status
	}
	if retention.TurnCount > 0 {
		state.turnCount = retention.TurnCount
	}
	if retention.TokenCount > 0 {
		state.tokenCount = retention.TokenCount
	}
}
