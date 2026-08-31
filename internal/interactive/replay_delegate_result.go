package interactive

import (
	"encoding/json"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

type replayedDelegateResult struct {
	AgentID           string `json:"agent_id"`
	Status            string `json:"status"`
	Output            string `json:"output"`
	Summary           string `json:"summary"`
	TurnCount         int    `json:"turn_count"`
	TokenCount        int    `json:"token_count"`
	ToolCallCount     int    `json:"tool_call_count"`
	Error             string `json:"error"`
	InputTokens       int    `json:"input_tokens"`
	CacheReadTokens   int    `json:"cache_read_tokens"`
	CacheCreateTokens int    `json:"cache_create_tokens"`
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

	var result replayedDelegateResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return replayedDelegateResult{}, false
	}
	return result, true
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
		if decoded.AgentID == "" && decoded.Status == "" && decoded.Summary == "" && decoded.Error == "" {
			state.output = content
		} else {
			applyDecodedDelegationState(&state, decoded)
		}
	}
	applyRetainedDelegationState(&state, retention)
	if state.status == "failed" && ok && decoded.Error != "" {
		state.error = decoded.Error
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
