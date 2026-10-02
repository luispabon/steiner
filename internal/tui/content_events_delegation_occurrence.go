package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// occurrenceKey identifies one delegation occurrence: one sub_agent or
// follow_up call within one tool batch. Provider call IDs repeat across batches
// and follow_up reuses agent IDs, so neither identifies an occurrence alone.
type occurrenceKey struct {
	BatchID string
	CallID  string
}

func keyOf(occ output.DelegationOccurrence) occurrenceKey {
	return occurrenceKey{BatchID: occ.BatchID, CallID: occ.CallID}
}

func (dd *delegationDisplayState) acceptedGroup() string {
	if dd == nil || dd.admission == nil {
		return ""
	}
	return dd.admission.Group
}

func (b *contentBuffer) appendDelegationAcceptedEvent(event output.Event) {
	payload, ok := event.Payload.(output.DelegationAcceptedEvent)
	if !ok || payload.CallID == "" {
		return
	}
	b.admitDelegation(payload.CallID, output.DelegationAdmission{
		Status:  tool.DelegationAdmissionAccepted,
		BatchID: payload.BatchID,
		Group:   payload.Group,
		AgentID: payload.AgentID,
	})
}

// admitDelegation records an accepted admission on its occurrence's card.
// Repeats are no-ops.
func (b *contentBuffer) admitDelegation(callID string, admission output.DelegationAdmission) {
	loc, found := b.lookupOccurrence(occurrenceKey{BatchID: admission.BatchID, CallID: callID})
	if !found {
		return
	}
	admission.Group = strings.TrimSpace(admission.Group)
	loc.dd.admission = &admission
	if _, ok := acceptedMembership(loc.dd); ok {
		b.regroupAdmittedDelegations(loc.dd)
	}
}

// lookupOccurrence resolves the card an occurrence event belongs to: the card
// already keyed by it, else the open parent card for its call, which moves
// under the key.
func (b *contentBuffer) lookupOccurrence(key occurrenceKey) (delegationLocator, bool) {
	if key.CallID == "" {
		return delegationLocator{}, false
	}
	if loc, found := b.delegations[key]; found {
		return loc, true
	}
	loc, found := b.takeOpenDelegation(key.CallID)
	if found {
		b.registerOccurrence(key, loc)
	}
	return loc, found
}

func (b *contentBuffer) registerOccurrence(key occurrenceKey, loc delegationLocator) {
	if key.CallID == "" || loc.dd == nil {
		return
	}
	if b.delegations == nil {
		b.delegations = make(map[occurrenceKey]delegationLocator)
	}
	b.delegations[key] = loc
}

func (b *contentBuffer) openDelegation(loc delegationLocator) {
	if loc.dd == nil || loc.dd.parentCallID == "" {
		return
	}
	if b.openDelegations == nil {
		b.openDelegations = make(map[string]delegationLocator)
	}
	b.openDelegations[loc.dd.parentCallID] = loc
}

// takeOpenDelegation removes and returns the open parent card for a call.
func (b *contentBuffer) takeOpenDelegation(callID string) (delegationLocator, bool) {
	loc, found := b.openDelegations[callID]
	if found {
		delete(b.openDelegations, callID)
	}
	return loc, found && loc.dd != nil
}

func (b *contentBuffer) closeOpenDelegation(callID string, dd *delegationDisplayState) {
	if loc, found := b.openDelegations[callID]; found && loc.dd == dd {
		delete(b.openDelegations, callID)
	}
}

// forgetDelegation drops every index entry for a card that reached a terminal
// state outside its normal lifecycle.
func (b *contentBuffer) forgetDelegation(dd *delegationDisplayState) {
	b.closeOpenDelegation(dd.parentCallID, dd)
	b.clearQueuedDelegation(dd.parentCallID)
	for agentID, loc := range b.activeDelegations {
		if loc.dd == dd {
			delete(b.activeDelegations, agentID)
		}
	}
}
