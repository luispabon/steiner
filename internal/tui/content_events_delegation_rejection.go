package tui

import (
	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) handleDelegationAdmissionFinish(payload output.ToolCallFinishedEvent) bool {
	admission := payload.DelegationAdmission
	if admission == nil {
		return false
	}
	switch admission.Status {
	case "accepted":
		if payload.CallID != "" {
			b.appendDelegationAcceptedEvent(output.NewDelegationAcceptedEvent(payload.CallID, admission.AgentID, admission.BatchID, admission.Group))
		}
		b.appendAdmissionError(payload.Error)
		return payload.Error != ""
	case "rejected":
		if admission.PolicyNotice {
			b.appendStyled("Delegation rejected by policy.", segmentStatus)
		}
		removed := payload.CallID != "" && b.removeRejectedDelegation(payload.CallID)
		if payload.Error != "" {
			b.segments = append(b.segments, contentSegment{kind: segmentTool, text: payload.Error, renderDirty: true})
		}
		return removed
	default:
		b.appendAdmissionError(payload.Error)
		// Let the ordinary finish path clear eligible empty-agent pending cards.
		// It does not append the error, so exact evidence is still emitted once.
		return false
	}
}

func (b *contentBuffer) appendAdmissionError(message string) {
	if message != "" {
		b.segments = append(b.segments, contentSegment{kind: segmentTool, text: message, renderDirty: true})
	}
}

func (b *contentBuffer) removeRejectedDelegation(callID string) bool {
	var target *delegationDisplayState
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd != nil && loc.dd.parentCallID == callID {
			target = loc.dd
			return true
		}
		return false
	})
	if target == nil || target.groupAccepted {
		return false
	}

	tokens := flattenDelegationSegments(b.segments)
	kept := make([]delegationSegmentToken, 0, len(tokens))
	removed := false
	for _, token := range tokens {
		if token.dd == target {
			removed = true
			continue
		}
		kept = append(kept, token)
	}
	if !removed {
		return false
	}
	members := make(map[delegationMembership][]*delegationDisplayState)
	for _, token := range kept {
		if key, ok := acceptedMembership(token.dd); ok {
			members[key] = append(members[key], token.dd)
		}
	}
	next, oldToNew := buildRegroupedSegments(kept, members)
	for i := range next {
		if next[i].kind == segmentDelegationGroup && next[i].delegGroupData != nil && len(next[i].delegGroupData.entries) == 1 {
			next[i].kind = segmentDelegation
			next[i].delegData = next[i].delegGroupData.entries[0]
			next[i].delegGroupData = nil
		}
	}
	b.commitSegmentRewrite(next, oldToNew)
	return true
}
