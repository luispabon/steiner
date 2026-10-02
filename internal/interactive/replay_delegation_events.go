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

func (s *Session) replayOwnedToolResult(msg agent.Message, owner replayOccurrenceKey, call agent.ToolCall, state replayState, entry agent.SubAgentLedgerEntry) {
	if isAdvisorToolCall(call.Name) {
		question, files := advisorQuestionAndFilesFromArgs(call.Arguments)
		s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
		s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
	} else if isDelegateToolCall(call.Name) {
		s.replayDelegateResult(msg, call, owner, state.acks, entry)
	}
	s.replayDisplayFile(msg)
	if state.startedToolCalls[owner] {
		s.replayToolFinished(msg)
		delete(state.startedToolCalls, owner)
	}
}

// replayDelegateResult emits the delegation events for one persisted delegate
// result, every one stamped with the occurrence from replayOccurrenceFor. The
// sequence mirrors live: Accepted, Queued or Started, then a terminal for
// results that are already final.
func (s *Session) replayDelegateResult(msg agent.Message, call agent.ToolCall, owner replayOccurrenceKey, acks *replayAcks, entry agent.SubAgentLedgerEntry) {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == tool.DelegationAdmissionRejected {
		return
	}
	task := taskFromArgs(call.Arguments)
	state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
	occ := replayOccurrenceFor(call.ID, owner.messageIndex, msg.DelegationAdmission, entry, state.agentID)
	state.agentID = occ.AgentID
	// A ledger entry on a still-running ack means the sub-agent is outstanding
	// in the resumed session, so replay must not fabricate a "no result" failure.
	outstanding := entry.AgentID != "" && isAckStatus(state.status)
	if admission := acceptedAdmission(msg); admission != nil {
		s.events.Emit(output.NewDelegationAcceptedEvent(occ, admission.Group))
	} else if outstanding {
		s.events.Emit(output.NewDelegationAcceptedEvent(occ, entry.Group))
	}
	if outstanding {
		s.emitDelegationProgress(occ, task, state.status)
		acks.add(occ, task, true)
		return
	}
	if state.status == "failed" && state.output == "" && acceptedAdmission(msg) != nil {
		s.emitDelegationFailure(occ, task, state.error)
		return
	}
	s.emitDelegationProgress(occ, task, state.status)
	switch {
	case isAckStatus(state.status):
		acks.add(occ, task, false)
	case state.status == "failed":
		s.emitDelegationFailure(occ, task, state.error)
	default:
		s.emitDelegationComplete(occ, state)
	}
}

// emitDelegationProgress emits the queued or started event.
func (s *Session) emitDelegationProgress(occ output.DelegationOccurrence, task, status string) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(occ, "", task))
		return
	}
	s.events.Emit(output.NewDelegationStartedEvent(occ, task, "", ""))
}

func (s *Session) emitDelegationFailure(occ output.DelegationOccurrence, task, err string) {
	s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{DelegationOccurrence: occ, TaskPreview: task, Error: err}))
}

func (s *Session) emitDelegationComplete(occ output.DelegationOccurrence, state replayedDelegationState) {
	s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: occ, Status: state.status, TurnCount: state.turnCount, TokenCount: state.tokenCount, ToolCallCount: state.toolCallCount, Output: state.output, InputTokens: state.inputTokens, CacheReadTokens: state.cacheReadTokens, CacheCreateTokens: state.cacheCreateTokens}))
}
