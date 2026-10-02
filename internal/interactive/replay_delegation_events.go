package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func replayStatus(msg agent.Message) string {
	if msg.Retention != nil && msg.Retention.Status != "" {
		return msg.Retention.Status
	}
	state := buildReplayedDelegationState(msg.ToolCallID, nil, msg.Content)
	return state.status
}

// acceptedAdmission returns msg's admission when it records an accepted
// delegation, or nil otherwise.
func acceptedAdmission(msg agent.Message) *tool.DelegationAdmission {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == tool.DelegationAdmissionAccepted {
		return msg.DelegationAdmission
	}
	return nil
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
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == tool.DelegationAdmissionRejected {
		return
	}
	task := taskFromArgs(call.Arguments)
	state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
	if admission := acceptedAdmission(msg); admission != nil {
		s.events.Emit(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: msg.ToolCallID, BatchID: admission.BatchID, AgentID: admission.AgentID}, admission.Group))
		if admission.AgentID != "" {
			state.agentID = admission.AgentID
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
	if admission := acceptedAdmission(msg); admission != nil && admission.AgentID != "" {
		agentID = admission.AgentID
	}
	s.emitDelegationProgress(agentID, msg.ToolCallID, task, status, true)
}

func (s *Session) replayEmptyAcceptedFailure(msg agent.Message, state replayedDelegationState, task string) bool {
	if state.status != "failed" || state.output != "" || acceptedAdmission(msg) == nil {
		return false
	}
	s.emitDelegationFailure(state.agentID, msg, task, state.error)
	return true
}

func (s *Session) emitReplayProgress(msg agent.Message, state replayedDelegationState, task string) {
	s.emitDelegationProgress(state.agentID, msg.ToolCallID, task, state.status, acceptedAdmission(msg) != nil)
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

// emitDelegationProgress emits the queued or started event. Started events
// carry the call ID only for accepted admissions; legacy results predate it.
func (s *Session) emitDelegationProgress(agentID, callID, task, status string, accepted bool) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(output.DelegationOccurrence{CallID: callID, AgentID: agentID}, "", task))
		return
	}
	if !accepted {
		callID = ""
	}
	s.events.Emit(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: callID, AgentID: agentID}, task, "", ""))
}

func (s *Session) emitDelegationFailure(agentID string, msg agent.Message, task, err string) {
	callID := ""
	if admission := acceptedAdmission(msg); admission != nil {
		callID = msg.ToolCallID
		if admission.AgentID != "" {
			agentID = admission.AgentID
		}
	}
	s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{DelegationOccurrence: output.DelegationOccurrence{AgentID: agentID, CallID: callID}, TaskPreview: task, Error: err}))
}

func (s *Session) emitDelegationComplete(state replayedDelegationState) {
	s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: output.DelegationOccurrence{AgentID: state.agentID}, Status: state.status, TurnCount: state.turnCount, TokenCount: state.tokenCount, ToolCallCount: state.toolCallCount, Output: state.output, InputTokens: state.inputTokens, CacheReadTokens: state.cacheReadTokens, CacheCreateTokens: state.cacheCreateTokens}))
}
