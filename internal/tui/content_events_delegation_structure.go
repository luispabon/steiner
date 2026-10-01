package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

type delegationMembership struct {
	batch string
	group string
}

func acceptedMembership(dd *delegationDisplayState) (delegationMembership, bool) {
	if dd == nil || dd.isAdvisor || !dd.groupAccepted || dd.batchID == "" || strings.TrimSpace(dd.group) == "" {
		return delegationMembership{}, false
	}
	return delegationMembership{batch: dd.batchID, group: strings.TrimSpace(dd.group)}, true
}

func (b *contentBuffer) appendDelegationAcceptedEvent(event output.Event) {
	payload, ok := event.Payload.(output.DelegationAcceptedEvent)
	if !ok || payload.CallID == "" {
		return
	}
	if b.acceptedDelegations == nil {
		b.acceptedDelegations = make(map[string]output.DelegationAcceptedEvent)
	}
	b.acceptedDelegations[payload.CallID] = payload
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd != nil && loc.dd.parentCallID == payload.CallID {
			loc.dd.groupAccepted = true
			loc.dd.batchID = payload.BatchID
			loc.dd.group = strings.TrimSpace(payload.Group)
		}
		return false
	})
	b.regroupAcceptedDelegations()
}

func (b *contentBuffer) regroupAcceptedDelegations() {
	type token struct {
		segment contentSegment
		dd      *delegationDisplayState
		old     int
	}
	var tokens []token
	for oldIndex, seg := range b.segments {
		switch seg.kind {
		case segmentDelegation:
			tokens = append(tokens, token{segment: seg, dd: seg.delegData, old: oldIndex})
		case segmentDelegationGroup:
			for _, dd := range seg.delegGroupData.entries {
				tokens = append(tokens, token{segment: seg, dd: dd, old: oldIndex})
			}
		default:
			tokens = append(tokens, token{segment: seg, old: oldIndex})
		}
	}
	members := make(map[delegationMembership][]*delegationDisplayState)
	for _, tok := range tokens {
		if key, ok := acceptedMembership(tok.dd); ok {
			members[key] = append(members[key], tok.dd)
		}
	}
	next := make([]contentSegment, 0, len(tokens))
	oldToNew := make(map[int]int, len(b.segments))
	seen := make(map[delegationMembership]bool)
	for _, tok := range tokens {
		if tok.dd == nil {
			oldToNew[tok.old] = len(next)
			next = append(next, tok.segment)
			continue
		}
		key, grouped := acceptedMembership(tok.dd)
		if grouped {
			if seen[key] {
				continue
			}
			seen[key] = true
			seg := tok.segment
			seg.kind, seg.delegData = segmentDelegationGroup, nil
			seg.delegGroupData = &delegationGroupSegment{entries: append([]*delegationDisplayState(nil), members[key]...)}
			seg.renderDirty = true
			oldToNew[tok.old] = len(next)
			next = append(next, seg)
			continue
		}
		tok.segment.kind, tok.segment.delegData = segmentDelegation, tok.dd
		tok.segment.delegGroupData = nil
		oldToNew[tok.old] = len(next)
		next = append(next, tok.segment)
	}
	if sameSegments(next, b.segments) {
		return
	}
	b.commitSegmentRewrite(next, oldToNew)
}

func sameSegments(a, b []contentSegment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind || a[i].delegData != b[i].delegData || a[i].delegGroupData != b[i].delegGroupData {
			return false
		}
	}
	return true
}

func (b *contentBuffer) commitSegmentRewrite(next []contentSegment, oldToNew map[int]int) {
	remapDelegation := func(dd *delegationDisplayState) delegationLocator {
		for i, seg := range next {
			if seg.delegData == dd {
				return delegationLocator{seg: i, dd: dd}
			}
			if seg.delegGroupData != nil {
				for _, entry := range seg.delegGroupData.entries {
					if entry == dd {
						return delegationLocator{seg: i, dd: dd}
					}
				}
			}
		}
		return delegationLocator{seg: -1, dd: dd}
	}
	remapTool := func(td *toolCallSegment) toolCallLocator {
		for i, seg := range next {
			if seg.toolData == td {
				return toolCallLocator{seg: i, td: td}
			}
			if seg.toolGroupData != nil {
				for _, entry := range seg.toolGroupData.entries {
					if entry == td {
						return toolCallLocator{seg: i, td: td}
					}
				}
			}
		}
		return toolCallLocator{seg: -1, td: td}
	}
	for key, loc := range b.activeDelegations {
		loc = remapDelegation(loc.dd)
		if loc.seg < 0 {
			delete(b.activeDelegations, key)
		} else {
			b.activeDelegations[key] = loc
		}
	}
	for key, loc := range b.activeToolCalls {
		loc = remapTool(loc.td)
		if loc.seg < 0 {
			delete(b.activeToolCalls, key)
		} else {
			b.activeToolCalls[key] = loc
		}
	}
	remapList := func(list []delegationLocator) []delegationLocator {
		out := list[:0]
		for _, loc := range list {
			mapped := remapDelegation(loc.dd)
			if mapped.seg >= 0 {
				out = append(out, mapped)
			}
		}
		return out
	}
	b.pendingDelegateParents = remapList(b.pendingDelegateParents)
	b.pendingDelegationStarts = remapList(b.pendingDelegationStarts)
	for key, loc := range b.queuedDelegations {
		loc = remapDelegation(loc.dd)
		if loc.seg < 0 {
			delete(b.queuedDelegations, key)
		} else {
			b.queuedDelegations[key] = loc
		}
	}
	if b.activeAdvisorSegment > 0 {
		if mapped, ok := oldToNew[b.activeAdvisorSegment-1]; ok {
			b.activeAdvisorSegment = mapped + 1
		} else {
			b.activeAdvisorSegment = 0
		}
	}
	oldCollapse := b.collapseState
	b.segments = next
	b.collapseState = make(map[int]bool)
	for oldIndex, newIndex := range oldToNew {
		if value, ok := oldCollapse[oldIndex]; ok {
			b.collapseState[newIndex] = value
		}
	}
	b.gen++
	b.structureGen++
	b.stringCacheWidth, b.stringCacheRendered = 0, ""
	b.prefixCacheSet, b.prefixCacheRendered = false, ""
	b.segmentHeights = nil
}
