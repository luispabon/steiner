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
	if target, found := b.peekDelegationOccurrence(payload.CallID); found {
		b.applyDelegationAccepted(target.dd, payload)
		return
	}
	if target, found := b.unambiguousDelegationByCallID(payload.CallID); found {
		b.applyDelegationAccepted(target.dd, payload)
	}
}

func (b *contentBuffer) applyDelegationAccepted(dd *delegationDisplayState, payload output.DelegationAcceptedEvent) {
	if dd == nil || dd.admissionAccepted || dd.groupAccepted {
		return
	}
	dd.admissionAccepted = true
	dd.groupAccepted = true
	dd.batchID = payload.BatchID
	dd.group = strings.TrimSpace(payload.Group)
	if _, ok := acceptedMembership(dd); ok {
		b.regroupAcceptedDelegations()
	}
}

func (b *contentBuffer) peekDelegationOccurrence(callID string) (delegationLocator, bool) {
	b.purgeDelegationOccurrences()
	for _, loc := range b.pendingDelegationOccurrences {
		if loc.dd != nil && loc.dd.parentCallID == callID && !loc.dd.admissionAccepted {
			return loc, true
		}
	}
	return delegationLocator{}, false
}

func (b *contentBuffer) retireDelegationOccurrence(dd *delegationDisplayState) {
	if dd == nil {
		return
	}
	for i, loc := range b.pendingDelegationOccurrences {
		if loc.dd == dd {
			b.pendingDelegationOccurrences = append(b.pendingDelegationOccurrences[:i], b.pendingDelegationOccurrences[i+1:]...)
			return
		}
	}
}

func (b *contentBuffer) takeDelegationOccurrence(callID string) (delegationLocator, bool) {
	b.purgeDelegationOccurrences()
	for _, loc := range b.pendingDelegationOccurrences {
		if loc.dd != nil && loc.dd.parentCallID == callID {
			b.retireDelegationOccurrence(loc.dd)
			return loc, true
		}
	}
	return delegationLocator{}, false
}

func (b *contentBuffer) appendReplayDelegationParentClosedEvent(event output.Event) {
	payload, ok := event.Payload.(output.ReplayDelegationParentClosedEvent)
	if !ok || payload.CallID == "" {
		return
	}
	b.takeDelegationOccurrence(payload.CallID)
}

func (b *contentBuffer) purgeDelegationOccurrences() {
	out := b.pendingDelegationOccurrences[:0]
	for _, loc := range b.pendingDelegationOccurrences {
		if index := findDelegationSegmentAt(b.segments, loc.dd, loc.seg); index >= 0 {
			loc.seg = index
			out = append(out, loc)
		}
	}
	b.pendingDelegationOccurrences = out
}

func (b *contentBuffer) unambiguousDelegationByCallID(callID string) (delegationLocator, bool) {
	var found delegationLocator
	count := 0
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd != nil && loc.dd.parentCallID == callID {
			found = loc
			count++
		}
		return count > 1
	})
	return found, count == 1
}

type delegationSegmentToken struct {
	segment contentSegment
	dd      *delegationDisplayState
	old     int
}

func flattenDelegationSegments(segments []contentSegment) []delegationSegmentToken {
	var tokens []delegationSegmentToken
	for old, seg := range segments {
		switch seg.kind {
		case segmentDelegation:
			tokens = append(tokens, delegationSegmentToken{segment: seg, dd: seg.delegData, old: old})
		case segmentDelegationGroup:
			for _, dd := range seg.delegGroupData.entries {
				tokens = append(tokens, delegationSegmentToken{segment: seg, dd: dd, old: old})
			}
		default:
			tokens = append(tokens, delegationSegmentToken{segment: seg, old: old})
		}
	}
	return tokens
}

func (b *contentBuffer) regroupAcceptedDelegations() {
	tokens := flattenDelegationSegments(b.segments)
	members := make(map[delegationMembership][]*delegationDisplayState)
	for _, tok := range tokens {
		if key, ok := acceptedMembership(tok.dd); ok {
			members[key] = append(members[key], tok.dd)
		}
	}
	next, oldToNew := buildRegroupedSegments(tokens, members)
	if sameSegments(next, b.segments) {
		return
	}
	b.commitSegmentRewrite(next, oldToNew)
}

func buildRegroupedSegments(tokens []delegationSegmentToken, members map[delegationMembership][]*delegationDisplayState) ([]contentSegment, map[int]int) {
	next := make([]contentSegment, 0, len(tokens))
	oldToNew := make(map[int]int, len(tokens))
	seen := make(map[delegationMembership]bool)
	for _, tok := range tokens {
		if tok.dd == nil {
			oldToNew[tok.old] = len(next)
			next = append(next, tok.segment)
			continue
		}
		key, grouped := acceptedMembership(tok.dd)
		if !grouped {
			seg := tok.segment
			seg.kind, seg.delegData, seg.delegGroupData = segmentDelegation, tok.dd, nil
			oldToNew[tok.old] = len(next)
			next = append(next, seg)
			continue
		}
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
	}
	return next, oldToNew
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

func findDelegationSegment(segments []contentSegment, dd *delegationDisplayState) int {
	return findDelegationSegmentAt(segments, dd, -1)
}

func findDelegationSegmentAt(segments []contentSegment, dd *delegationDisplayState, hint int) int {
	if hint >= 0 && hint < len(segments) && segmentHoldsDelegation(segments[hint], dd) {
		return hint
	}
	for i, seg := range segments {
		if segmentHoldsDelegation(seg, dd) {
			return i
		}
	}
	return -1
}

func segmentHoldsDelegation(seg contentSegment, dd *delegationDisplayState) bool {
	if seg.delegData == dd {
		return true
	}
	if seg.delegGroupData != nil {
		for _, entry := range seg.delegGroupData.entries {
			if entry == dd {
				return true
			}
		}
	}
	return false
}

func findToolCallSegment(segments []contentSegment, td *toolCallSegment) int {
	return findToolCallSegmentAt(segments, td, -1)
}

func findToolCallSegmentAt(segments []contentSegment, td *toolCallSegment, hint int) int {
	if hint >= 0 && hint < len(segments) && segmentHoldsToolCall(segments[hint], td) {
		return hint
	}
	for i, seg := range segments {
		if segmentHoldsToolCall(seg, td) {
			return i
		}
	}
	return -1
}

func segmentHoldsToolCall(seg contentSegment, td *toolCallSegment) bool {
	if seg.toolData == td {
		return true
	}
	if seg.toolGroupData != nil {
		for _, entry := range seg.toolGroupData.entries {
			if entry == td {
				return true
			}
		}
	}
	return false
}

func remapDelegationLocators(locators []delegationLocator, segments []contentSegment) []delegationLocator {
	out := locators[:0]
	for _, loc := range locators {
		if index := findDelegationSegmentAt(segments, loc.dd, loc.seg); index >= 0 {
			out = append(out, delegationLocator{seg: index, dd: loc.dd})
		}
	}
	return out
}

func remapActiveDelegations(active map[string]delegationLocator, segments []contentSegment) {
	for key, loc := range active {
		if index := findDelegationSegmentAt(segments, loc.dd, loc.seg); index < 0 {
			delete(active, key)
		} else {
			active[key] = delegationLocator{seg: index, dd: loc.dd}
		}
	}
}

func remapActiveToolCalls(active map[string]toolCallLocator, segments []contentSegment) {
	for key, loc := range active {
		if index := findToolCallSegmentAt(segments, loc.td, loc.seg); index < 0 {
			delete(active, key)
		} else {
			active[key] = toolCallLocator{seg: index, td: loc.td}
		}
	}
}

func (b *contentBuffer) commitSegmentRewrite(next []contentSegment, oldToNew map[int]int) {
	remapActiveDelegations(b.activeDelegations, next)
	remapActiveToolCalls(b.activeToolCalls, next)
	b.pendingDelegateParents = remapDelegationLocators(b.pendingDelegateParents, next)
	b.pendingDelegationStarts = remapDelegationLocators(b.pendingDelegationStarts, next)
	b.pendingDelegationOccurrences = remapDelegationLocators(b.pendingDelegationOccurrences, next)
	remapActiveDelegations(b.queuedDelegations, next)
	b.activeAdvisorSegment = remapAdvisorSegment(b.activeAdvisorSegment, oldToNew)
	oldCollapse := b.collapseState
	b.segments = next
	b.collapseState = remapCollapseState(oldCollapse, oldToNew)
	b.gen++
	b.structureGen++
	b.stringCacheWidth, b.stringCacheRendered = 0, ""
	b.prefixCacheSet, b.prefixCacheRendered = false, ""
	b.segmentHeights = nil
}

func remapAdvisorSegment(advisor int, oldToNew map[int]int) int {
	if advisor <= 0 {
		return advisor
	}
	if mapped, ok := oldToNew[advisor-1]; ok {
		return mapped + 1
	}
	return 0
}

func remapCollapseState(collapse map[int]bool, oldToNew map[int]int) map[int]bool {
	remapped := make(map[int]bool)
	for oldIndex, newIndex := range oldToNew {
		if value, ok := collapse[oldIndex]; ok {
			remapped[newIndex] = value
		}
	}
	return remapped
}
