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
	// bundled marks a delegate call replayed serially at its call position, so
	// its result message must not replay a second time. Bundling needs
	// authoritative correlation (a ledger entry or a typed admission); legacy
	// results keep the two-phase path so old sessions replay unchanged.
	bundled bool
}

type replayOccurrencePlan struct {
	occurrences map[replayOccurrenceKey]*replayOccurrence
	// resultOwners is the inverse of resultMessageIndex: it resolves which of
	// several same-ID calls a tool message answers.
	resultOwners   map[int]replayOccurrenceKey
	reservedLedger map[int]bool
}

func buildReplayOccurrencePlan(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) replayOccurrencePlan {
	plan := replayOccurrencePlan{occurrences: make(map[replayOccurrenceKey]*replayOccurrence), resultOwners: make(map[int]replayOccurrenceKey)}
	for messageIndex, msg := range msgs {
		if msg.Role != agent.MessageRoleAssistant {
			continue
		}
		for callIndex, call := range msg.ToolCalls {
			plan.occurrences[replayOccurrenceKey{messageIndex: messageIndex, callIndex: callIndex}] = &replayOccurrence{call: call, resultMessageIndex: -1, ledgerIndex: -1}
		}
	}
	for _, pair := range pairToolResults(msgs).pairs {
		plan.occurrences[pair.call].resultMessageIndex = pair.resultIndex
		plan.resultOwners[pair.resultIndex] = pair.call
	}
	assignExplicitReplayLedgerOwnership(msgs, ledger, &plan)
	assignFallbackReplayLedgerOwnership(msgs, ledger, &plan)
	for _, occurrence := range plan.occurrences {
		occurrence.bundled = isDelegateToolCall(occurrence.call.Name) && occurrence.resultMessageIndex >= 0 &&
			(occurrence.ledgerIndex >= 0 || hasKnownAdmission(msgs[occurrence.resultMessageIndex].DelegationAdmission))
	}
	return plan
}

type replayLedger struct {
	msgs         []agent.Message
	entries      []agent.SubAgentLedgerEntry
	occurrences  map[replayOccurrenceKey]*replayOccurrence
	owners       map[int]replayOccurrenceKey
	messageIndex int
}

// replayDelegationBundle emits one owned persisted delegation result exactly
// once: the parent start, admission, lifecycle and terminal, and the actual
// parent finish, before the next delegation occurrence. replayMessage skips the
// bundled result message, so its events are not emitted a second time.
func (s *Session) replayDelegationBundle(call agent.ToolCall, key replayOccurrenceKey, occurrence *replayOccurrence, state replayState, ledger replayLedger) {
	s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
	state.startedToolCalls[key] = true
	s.replayOwnedToolResult(ledger.msgs[occurrence.resultMessageIndex], key, call, state, ledgerEntryForOccurrence(ledger, occurrence))
}

// replayLedgerOrphanBundle emits a ledger orphan: a delegate call with ledger
// evidence but no persisted result. It emits the start, authoritative
// acceptance and lifecycle; no orphan finish is fabricated.
func (s *Session) replayLedgerOrphanBundle(call agent.ToolCall, key replayOccurrenceKey, occurrence *replayOccurrence, state replayState, ledger replayLedger) {
	s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
	entry := ledger.entries[occurrence.ledgerIndex]
	occ := replayOccurrenceFor(call.ID, key.messageIndex, nil, entry, "")
	s.events.Emit(output.NewDelegationAcceptedEvent(occ, entry.Group))
	s.events.Emit(output.NewDelegationStartedEvent(occ, taskFromArgs(call.Arguments), "", ""))
	state.startedToolCalls[key] = true
}

func ledgerEntryForOccurrence(ledger replayLedger, occurrence *replayOccurrence) agent.SubAgentLedgerEntry {
	if occurrence == nil || occurrence.ledgerIndex < 0 {
		return agent.SubAgentLedgerEntry{}
	}
	return ledger.entries[occurrence.ledgerIndex]
}

// assignExplicitReplayLedgerOwnership matches typed-admission results to ledger
// entries by exact call/agent/batch/group identity; it resolves repeated call
// IDs, leaving an entry claimed by several results unassigned.
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

// assignFallbackReplayLedgerOwnership matches admission-less running/queued
// results (and orphan calls) to a ledger entry by parent call ID, only when the
// pairing is one-to-one; it resolves legacy sessions that carry no identity.
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
