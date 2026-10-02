package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func replayStatus(msg agent.Message) string {
	if msg.Retention != nil && msg.Retention.Status != "" {
		return msg.Retention.Status
	}
	state := buildReplayedDelegationState(msg.ToolCallID, nil, msg.Content)
	return state.status
}

func (s *Session) emitAcceptedAdmission(callID, agentID, batchID, group string) {
	s.events.Emit(output.NewDelegationAcceptedEvent(callID, agentID, batchID, group))
}

func (s *Session) replayOwnedToolResult(msg agent.Message, owner replayOccurrenceKey, call agent.ToolCall, state replayState, inferred agent.SubAgentLedgerEntry) {
	if isAdvisorToolCall(call.Name) {
		question, files := advisorQuestionAndFilesFromArgs(call.Arguments)
		s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
		s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
	} else if isDelegateToolCall(call.Name) {
		s.replayDelegateResult(msg, call, state.acks, inferred)
	}
	s.replayDisplayFile(msg)
	if state.startedToolCalls[owner] {
		s.replayToolFinished(msg)
		delete(state.startedToolCalls, owner)
	}
}

func (s *Session) replayDelegateResult(msg agent.Message, call agent.ToolCall, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "rejected" {
		return
	}
	task := taskFromArgs(call.Arguments)
	state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" {
		s.emitAcceptedAdmission(msg.ToolCallID, msg.DelegationAdmission.AgentID, msg.DelegationAdmission.BatchID, msg.DelegationAdmission.Group)
		if msg.DelegationAdmission.AgentID != "" {
			state.agentID = msg.DelegationAdmission.AgentID
		}
	}
	if replayLedgerBackedAck(inferred, state.status) {
		s.emitLedgerProgress(msg, inferred, task, state.status)
		return
	}
	if s.replayEmptyAcceptedFailure(msg, state, task) {
		return
	}
	s.emitReplayProgress(msg, state, task)
	s.replayDelegationTerminal(msg, state, task, acks, inferred)
}

func replayLedgerBackedAck(inferred agent.SubAgentLedgerEntry, status string) bool {
	return inferred.AgentID != "" && isAckStatus(status)
}

func (s *Session) emitLedgerProgress(msg agent.Message, inferred agent.SubAgentLedgerEntry, task, status string) {
	agentID := inferred.AgentID
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" && msg.DelegationAdmission.AgentID != "" {
		agentID = msg.DelegationAdmission.AgentID
	}
	s.emitDelegationProgress(agentID, msg.ToolCallID, task, status)
}

func (s *Session) replayEmptyAcceptedFailure(msg agent.Message, state replayedDelegationState, task string) bool {
	if state.status != "failed" || state.output != "" || !hasKnownAdmission(msg.DelegationAdmission) || msg.DelegationAdmission.Status != "accepted" {
		return false
	}
	s.emitDelegationFailure(state.agentID, msg, task, state.error)
	return true
}

func (s *Session) emitReplayProgress(msg agent.Message, state replayedDelegationState, task string) {
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" {
		s.emitDelegationProgress(state.agentID, msg.ToolCallID, task, state.status)
	} else {
		s.emitLegacyDelegationProgress(state.agentID, task, state.status, msg.ToolCallID)
	}
}

func (s *Session) replayDelegationTerminal(msg agent.Message, state replayedDelegationState, task string, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
	switch {
	case isAckStatus(state.status):
		if inferred.AgentID == "" {
			acks.add(msg.ToolCallID, state.agentID, task)
		}
	case state.status == "failed":
		s.emitDelegationFailure(state.agentID, msg, task, state.error)
	default:
		s.emitDelegationComplete(state)
	}
}

func (s *Session) emitDelegationProgress(agentID, callID, task, status string) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(agentID, callID, "", task))
	} else {
		s.events.Emit(output.NewDelegationStartedEvent(agentID, task, callID))
	}
}

func (s *Session) emitLegacyDelegationProgress(agentID, task, status, callID string) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(agentID, callID, "", task))
	} else {
		s.events.Emit(output.NewDelegationStartedEvent(agentID, task))
	}
}

func (s *Session) emitDelegationFailure(agentID string, msg agent.Message, task, err string) {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "accepted" && msg.DelegationAdmission.AgentID != "" {
		agentID = msg.DelegationAdmission.AgentID
	}
	callID := ""
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "accepted" {
		callID = msg.ToolCallID
	}
	s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: agentID, CallID: callID, TaskPreview: task, Error: err}))
}

func (s *Session) emitDelegationComplete(state replayedDelegationState) {
	s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: state.agentID, Status: state.status, TurnCount: state.turnCount, TokenCount: state.tokenCount, ToolCallCount: state.toolCallCount, Output: state.output, InputTokens: state.inputTokens, CacheReadTokens: state.cacheReadTokens, CacheCreateTokens: state.cacheCreateTokens}))
}
