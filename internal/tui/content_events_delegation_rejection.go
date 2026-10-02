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
	switch admission.Status {
	case tool.DelegationAdmissionAccepted:
		b.admitDelegation(payload.CallID, *admission)
	case tool.DelegationAdmissionRejected:
		if admission.PolicyNotice {
			b.appendStyled("Delegation rejected by policy.", segmentStatus)
		}
		if loc, found := b.takeOpenDelegation(payload.CallID); found {
			b.removeRejectedDelegationCard(loc.dd)
		}
	default:
		if loc, found := b.takeOpenDelegation(payload.CallID); found && b.failUnboundDelegation(loc, payload.CallID, payload.Error) {
			loc.dd.errMsg = "delegation failed"
		}
	}
	b.appendAdmissionError(payload.Error)
	return true
}

func (b *contentBuffer) appendAdmissionError(message string) {
	if message != "" {
		b.segments = append(b.segments, contentSegment{kind: segmentTool, text: message, renderDirty: true})
	}
}

func (b *contentBuffer) removeRejectedDelegationCard(target *delegationDisplayState) bool {
	if target == nil || target.admission != nil {
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
	next, oldToNew := buildRegroupedSegments(kept, members, 0)
	for i := range next {
		if next[i].kind == segmentDelegationGroup && next[i].delegGroupData != nil && len(next[i].delegGroupData.entries) == 1 {
			next[i].kind = segmentDelegation
			next[i].delegData = next[i].delegGroupData.entries[0]
			next[i].delegGroupData = nil
		}
	}
	b.commitSegmentRewrite(next, oldToNew, 0)
	return true
}
