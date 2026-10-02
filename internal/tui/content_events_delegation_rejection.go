package tui

import (
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func (b *contentBuffer) handleDelegationAdmissionFinish(payload output.ToolCallFinishedEvent) bool {
	admission := payload.DelegationAdmission
	if admission == nil {
		return false
	}
	loc, found := b.takeDelegationOccurrence(payload.CallID)
	if !found {
		loc, found = b.unambiguousDelegationByCallID(payload.CallID)
	}
	switch admission.Status {
	case tool.DelegationAdmissionAccepted:
		if found {
			b.applyDelegationAccepted(loc.dd, output.DelegationAcceptedEvent{
				CallID:  payload.CallID,
				AgentID: admission.AgentID,
				BatchID: admission.BatchID,
				Group:   admission.Group,
			})
		}
		b.appendAdmissionError(payload.Error)
		return true
	case tool.DelegationAdmissionRejected:
		if admission.PolicyNotice {
			b.appendStyled("Delegation rejected by policy.", segmentStatus)
		}
		if found && loc.dd != nil && !loc.dd.groupAccepted {
			b.removeRejectedDelegationCard(loc.dd)
		}
		if payload.Error != "" {
			b.appendAdmissionError(payload.Error)
		}
		return true
	default:
		if found {
			b.applyDelegationAdmissionFinish(loc, payload)
		}
		b.appendAdmissionError(payload.Error)
		return true
	}
}

func (b *contentBuffer) applyDelegationAdmissionFinish(loc delegationLocator, payload output.ToolCallFinishedEvent) {
	if loc.dd == nil {
		return
	}
	if payload.Error != "" && loc.dd.agentID == "" {
		b.removeFromPendingDelegateParents(loc.dd)
		b.clearQueuedDelegation(payload.CallID)
		loc.dd.status = "failed"
		loc.dd.errMsg = "delegation failed"
		b.markDelegationDirty(loc.seg)
	}
}

func (b *contentBuffer) appendAdmissionError(message string) {
	if message != "" {
		b.segments = append(b.segments, contentSegment{kind: segmentTool, text: message, renderDirty: true})
	}
}

func (b *contentBuffer) removeRejectedDelegationCard(target *delegationDisplayState) bool {
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
