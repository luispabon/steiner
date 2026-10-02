package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type replayOccurrenceKey struct {
	messageIndex int
	callIndex    int
}

type replayOccurrence struct {
	call               agent.ToolCall
	resultMessageIndex int
	ledgerIndex        int
}

type replayOccurrencePlan struct {
	occurrences    map[replayOccurrenceKey]*replayOccurrence
	resultOwners   map[int]replayOccurrenceKey
	reservedLedger map[int]bool
}

func buildReplayOccurrencePlan(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) replayOccurrencePlan {
	plan := replayOccurrencePlan{occurrences: make(map[replayOccurrenceKey]*replayOccurrence), resultOwners: make(map[int]replayOccurrenceKey)}
	queues := make(map[string][]replayOccurrenceKey)
	for messageIndex, msg := range msgs {
		if msg.Role == agent.MessageRoleAssistant {
			for callIndex, call := range msg.ToolCalls {
				key := replayOccurrenceKey{messageIndex: messageIndex, callIndex: callIndex}
				plan.occurrences[key] = &replayOccurrence{call: call, resultMessageIndex: -1, ledgerIndex: -1}
				if call.ID != "" {
					queues[call.ID] = append(queues[call.ID], key)
				}
			}
		}
		if msg.Role != agent.MessageRoleTool || msg.ToolCallID == "" {
			continue
		}
		queue := queues[msg.ToolCallID]
		for len(queue) > 0 && plan.occurrences[queue[0]].resultMessageIndex >= 0 {
			queue = queue[1:]
		}
		if len(queue) == 0 {
			continue
		}
		key := queue[0]
		queue = queue[1:]
		queues[msg.ToolCallID] = queue
		plan.occurrences[key].resultMessageIndex = messageIndex
		plan.resultOwners[messageIndex] = key
	}
	assignExplicitReplayLedgerOwnership(msgs, ledger, &plan)
	assignFallbackReplayLedgerOwnership(msgs, ledger, &plan)
	return plan
}

type replayLedger struct {
	entries      []agent.SubAgentLedgerEntry
	occurrences  map[replayOccurrenceKey]*replayOccurrence
	owners       map[int]replayOccurrenceKey
	messageIndex int
}

// replayBundlesOccurrence reports whether an owned delegation result replays as
// one serial bundle at its assistant call position. Bundling needs
// authoritative correlation evidence: a ledger entry or a typed accepted or
// rejected admission. Retention-only legacy results keep the original
// two-phase path so old-format sessions replay unchanged.
func replayBundlesOccurrence(state replayState, occurrence *replayOccurrence) bool {
	if occurrence == nil || occurrence.resultMessageIndex < 0 {
		return false
	}
	if occurrence.ledgerIndex >= 0 {
		return true
	}
	if occurrence.resultMessageIndex >= len(state.msgs) {
		return false
	}
	return hasKnownAdmission(state.msgs[occurrence.resultMessageIndex].DelegationAdmission)
}

// replayDelegationBundle emits one owned persisted delegation result exactly
// once: the parent start, admission, lifecycle and terminal, and the actual
// parent finish, before the next delegation occurrence. The result message is
// marked consumed so replayMessage cannot emit its events a second time.
func (s *Session) replayDelegationBundle(call agent.ToolCall, key replayOccurrenceKey, occurrence *replayOccurrence, state replayState, ledger replayLedger) {
	s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
	state.startedToolCalls[key] = true
	toolMsg := state.msgs[occurrence.resultMessageIndex]
	s.replayLedgerAdmission(toolMsg, occurrence, ledger)
	s.replayOwnedToolResult(toolMsg, key, call, state, ledgerEntryForOccurrence(ledger, occurrence))
	state.consumedResults[occurrence.resultMessageIndex] = true
}

// replayLedgerOrphanBundle emits a ledger orphan: a delegate call with ledger
// evidence but no persisted result. It emits the start, authoritative
// acceptance and lifecycle, then closes exactly that parent correlation slot.
// The closure is a control event, not a result, finish, or delivery
// acknowledgement, so no orphan finish is fabricated.
func (s *Session) replayLedgerOrphanBundle(call agent.ToolCall, key replayOccurrenceKey, occurrence *replayOccurrence, state replayState, ledger replayLedger) {
	s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
	entry := ledger.entries[occurrence.ledgerIndex]
	s.events.Emit(output.NewDelegationAcceptedEvent(call.ID, entry.AgentID, entry.BatchID, entry.Group))
	s.events.Emit(output.NewDelegationStartedEvent(entry.AgentID, taskFromArgs(call.Arguments), call.ID))
	s.events.Emit(output.NewReplayDelegationParentClosedEvent(call.ID))
	state.startedToolCalls[key] = true
}

func (s *Session) replayLedgerAdmission(msg agent.Message, occurrence *replayOccurrence, ledger replayLedger) {
	if occurrence == nil || occurrence.ledgerIndex < 0 {
		return
	}
	admission := msg.DelegationAdmission
	if hasKnownAdmission(admission) {
		return
	}
	entry := ledger.entries[occurrence.ledgerIndex]
	status := replayStatus(msg)
	if status != "running" && status != "queued" {
		return
	}
	s.events.Emit(output.NewDelegationAcceptedEvent(msg.ToolCallID, entry.AgentID, entry.BatchID, entry.Group))
}

func ledgerEntryForOccurrence(ledger replayLedger, occurrence *replayOccurrence) agent.SubAgentLedgerEntry {
	if occurrence == nil || occurrence.ledgerIndex < 0 {
		return agent.SubAgentLedgerEntry{}
	}
	return ledger.entries[occurrence.ledgerIndex]
}

func assignExplicitReplayLedgerOwnership(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry, plan *replayOccurrencePlan) {
	reserved := make(map[int]bool)
	ledgerCandidates := make(map[replayOccurrenceKey][]int)
	entryDegrees := make(map[int]int)
	for key, occurrence := range plan.occurrences {
		if occurrence.resultMessageIndex < 0 {
			continue
		}
		admission := msgs[occurrence.resultMessageIndex].DelegationAdmission
		if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.AgentID == "" {
			continue
		}
		candidates := explicitLedgerCandidates(occurrence.call.ID, admission, ledger)
		if len(candidates) == 0 {
			continue
		}
		ledgerCandidates[key] = candidates
		for _, index := range candidates {
			reserved[index] = true
			entryDegrees[index]++
		}
	}
	for key, candidates := range ledgerCandidates {
		if len(candidates) == 1 && entryDegrees[candidates[0]] == 1 {
			plan.occurrences[key].ledgerIndex = candidates[0]
		}
	}
	plan.reservedLedger = reserved
}

func explicitLedgerCandidates(callID string, admission *tool.DelegationAdmission, ledger []agent.SubAgentLedgerEntry) []int {
	var candidates []int
	for i, entry := range ledger {
		if entry.ParentCallID == callID && entry.AgentID == admission.AgentID && optionalMatches(admission.BatchID, entry.BatchID) && optionalMatches(admission.Group, entry.Group) {
			candidates = append(candidates, i)
		}
	}
	return candidates
}

func assignFallbackReplayLedgerOwnership(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry, plan *replayOccurrencePlan) {
	buckets := make(map[string][]*replayOccurrence)
	for _, occurrence := range plan.occurrences {
		if occurrence.ledgerIndex >= 0 || !isDelegateToolCall(occurrence.call.Name) {
			continue
		}
		eligible := occurrence.resultMessageIndex < 0
		if occurrence.resultMessageIndex >= 0 {
			msg := msgs[occurrence.resultMessageIndex]
			status := replayStatus(msg)
			eligible = !hasKnownAdmission(msg.DelegationAdmission) && (status == "running" || status == "queued")
		}
		if eligible {
			buckets[occurrence.call.ID] = append(buckets[occurrence.call.ID], occurrence)
		}
	}
	for parentCallID, occurrences := range buckets {
		var candidates []int
		for i, entry := range ledger {
			if !plan.reservedLedger[i] && entry.ParentCallID == parentCallID {
				candidates = append(candidates, i)
			}
		}
		if len(occurrences) == 1 && len(candidates) == 1 {
			occurrences[0].ledgerIndex = candidates[0]
			plan.reservedLedger[candidates[0]] = true
		}
	}
}

func optionalMatches(authoritative, evidence string) bool {
	return authoritative == "" || evidence == "" || authoritative == evidence
}

func hasKnownAdmission(admission *tool.DelegationAdmission) bool {
	return admission != nil && (admission.Status == tool.DelegationAdmissionAccepted || admission.Status == tool.DelegationAdmissionRejected)
}
