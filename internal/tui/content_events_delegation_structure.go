package tui

import (
	"maps"
	"slices"
)

type delegationMembership struct {
	batch string
	group string
}

func acceptedMembership(dd *delegationDisplayState) (delegationMembership, bool) {
	if dd == nil || dd.isAdvisor || dd.admission == nil || dd.admission.BatchID == "" || dd.admission.Group == "" {
		return delegationMembership{}, false
	}
	return delegationMembership{batch: dd.admission.BatchID, group: dd.admission.Group}, true
}

type delegationSegmentToken struct {
	segment contentSegment
	dd      *delegationDisplayState
	old     int
}

func flattenDelegationSegments(segments []contentSegment) []delegationSegmentToken {
	return flattenDelegationSegmentsFrom(segments, 0)
}

// flattenDelegationSegmentsFrom flattens segments, numbering tokens from base
// so old indices stay absolute when segments is a tail of the buffer.
func flattenDelegationSegmentsFrom(segments []contentSegment, base int) []delegationSegmentToken {
	var tokens []delegationSegmentToken
	for i, seg := range segments {
		old := base + i
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

// regroupAdmittedDelegations regroups the tail of the buffer that can hold
// members of dd's batch. A batch cannot span a user prompt, so the rewrite
// starts at the earliest segment of the batch within the current turn and
// leaves everything before it untouched.
func (b *contentBuffer) regroupAdmittedDelegations(dd *delegationDisplayState) {
	key, ok := acceptedMembership(dd)
	if !ok {
		return
	}
	start := b.batchRewriteStart(key.batch)
	if start < 0 {
		return
	}
	tokens := flattenDelegationSegmentsFrom(b.segments[start:], start)
	members := make(map[delegationMembership][]*delegationDisplayState)
	for _, tok := range tokens {
		if key, ok := acceptedMembership(tok.dd); ok {
			members[key] = append(members[key], tok.dd)
		}
	}
	tail, oldToNew := buildRegroupedSegments(tokens, members, start)
	if sameSegments(tail, b.segments[start:]) {
		return
	}
	b.segments = append(b.segments[:start], tail...)
	b.commitSegmentRewrite(b.segments, oldToNew, start)
}

// batchRewriteStart returns the index of the earliest segment in the current
// turn holding an accepted member of batch, or -1 when there is none.
func (b *contentBuffer) batchRewriteStart(batch string) int {
	start := -1
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if isUserSegment(seg.kind) {
			break
		}
		if segmentHoldsBatch(seg, batch) {
			start = i
		}
	}
	return start
}

func segmentHoldsBatch(seg contentSegment, batch string) bool {
	if key, ok := acceptedMembership(seg.delegData); ok && key.batch == batch {
		return true
	}
	if seg.delegGroupData != nil {
		for _, dd := range seg.delegGroupData.entries {
			if key, ok := acceptedMembership(dd); ok && key.batch == batch {
				return true
			}
		}
	}
	return false
}

// buildRegroupedSegments rebuilds tokens into segments. Old indices in tokens
// are absolute; base is the absolute index the first rebuilt segment lands at.
func buildRegroupedSegments(tokens []delegationSegmentToken, members map[delegationMembership][]*delegationDisplayState, base int) ([]contentSegment, map[int]int) {
	next := make([]contentSegment, 0, len(tokens))
	oldToNew := make(map[int]int, len(tokens))
	seen := make(map[delegationMembership]bool)
	for _, tok := range tokens {
		if tok.dd == nil {
			oldToNew[tok.old] = base + len(next)
			next = append(next, tok.segment)
			continue
		}
		key, grouped := acceptedMembership(tok.dd)
		if !grouped {
			seg := tok.segment
			seg.kind, seg.delegData, seg.delegGroupData = segmentDelegation, tok.dd, nil
			oldToNew[tok.old] = base + len(next)
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
		oldToNew[tok.old] = base + len(next)
		next = append(next, seg)
	}
	return next, oldToNew
}

func sameSegments(a, b []contentSegment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind || a[i].delegData != b[i].delegData || !sameGroupEntries(a[i].delegGroupData, b[i].delegGroupData) {
			return false
		}
	}
	return true
}

func sameGroupEntries(a, b *delegationGroupSegment) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.Equal(a.entries, b.entries)
}

func findDelegationSegment(segments []contentSegment, dd *delegationDisplayState) int {
	return findDelegationSegmentFrom(segments, dd, -1, 0)
}

// findDelegationSegmentFrom checks hint, then scans segments[start:], then the
// unchanged segments ahead of start.
func findDelegationSegmentFrom(segments []contentSegment, dd *delegationDisplayState, hint, start int) int {
	return findSegmentFrom(segments, hint, start, func(seg contentSegment) bool { return segmentHoldsDelegation(seg, dd) })
}

func findSegmentFrom(segments []contentSegment, hint, start int, holds func(contentSegment) bool) int {
	if hint >= 0 && hint < len(segments) && holds(segments[hint]) {
		return hint
	}
	for i := start; i < len(segments); i++ {
		if holds(segments[i]) {
			return i
		}
	}
	for i := range min(start, len(segments)) {
		if holds(segments[i]) {
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

func findToolCallSegmentFrom(segments []contentSegment, td *toolCallSegment, hint, start int) int {
	return findSegmentFrom(segments, hint, start, func(seg contentSegment) bool { return segmentHoldsToolCall(seg, td) })
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

func remapDelegationIndex[K comparable](index map[K]delegationLocator, segments []contentSegment, start int) {
	for key, loc := range index {
		if i := findDelegationSegmentFrom(segments, loc.dd, loc.seg, start); i < 0 {
			delete(index, key)
		} else {
			index[key] = delegationLocator{seg: i, dd: loc.dd}
		}
	}
}

func remapDelegationLocators(locators []delegationLocator, segments []contentSegment, start int) []delegationLocator {
	out := locators[:0]
	for _, loc := range locators {
		if index := findDelegationSegmentFrom(segments, loc.dd, loc.seg, start); index >= 0 {
			out = append(out, delegationLocator{seg: index, dd: loc.dd})
		}
	}
	return out
}

func remapActiveToolCalls(active map[string]toolCallLocator, segments []contentSegment, start int) {
	for key, loc := range active {
		if index := findToolCallSegmentFrom(segments, loc.td, loc.seg, start); index < 0 {
			delete(active, key)
		} else {
			active[key] = toolCallLocator{seg: index, td: loc.td}
		}
	}
}

// commitSegmentRewrite installs next, whose segments before start are the
// buffer's unchanged leading segments; oldToNew maps the old indices at or
// after start. The settled-prefix cache survives when it covers only the
// unchanged leading segments (prefixCacheLen <= start); a longer one spans
// rewritten segments and is dropped.
func (b *contentBuffer) commitSegmentRewrite(next []contentSegment, oldToNew map[int]int, start int) {
	remapDelegationIndex(b.activeDelegations, next, start)
	remapDelegationIndex(b.openDelegations, next, start)
	remapDelegationIndex(b.delegations, next, start)
	remapDelegationIndex(b.queuedDelegations, next, start)
	remapActiveToolCalls(b.activeToolCalls, next, start)
	b.pendingDelegationStarts = remapDelegationLocators(b.pendingDelegationStarts, next, start)
	b.activeAdvisorSegment = remapAdvisorSegment(b.activeAdvisorSegment, oldToNew, start)
	oldCollapse := b.collapseState
	b.segments = next
	b.collapseState = remapCollapseState(oldCollapse, oldToNew, start)
	prefixHeld := b.prefixCacheSet && b.prefixCacheGen == b.gen && b.prefixCacheLen <= start
	b.gen++
	b.structureGen++
	b.stringCacheWidth, b.stringCacheRendered = 0, ""
	if prefixHeld {
		b.prefixCacheGen = b.gen
	} else {
		b.prefixCacheSet, b.prefixCacheRendered = false, ""
	}
	if start == 0 {
		b.segmentHeights = nil
	} else {
		b.segmentHeights = b.segmentHeights[:min(start, len(b.segmentHeights))]
	}
}

func remapAdvisorSegment(advisor int, oldToNew map[int]int, start int) int {
	if advisor <= start {
		return advisor
	}
	if mapped, ok := oldToNew[advisor-1]; ok {
		return mapped + 1
	}
	return 0
}

// remapCollapseState rewrites collapse in place: entries before start are
// untouched, entries at or after it move to their new indices.
func remapCollapseState(collapse map[int]bool, oldToNew map[int]int, start int) map[int]bool {
	if collapse == nil {
		return make(map[int]bool)
	}
	moved := make(map[int]bool)
	for oldIndex, newIndex := range oldToNew {
		if value, ok := collapse[oldIndex]; ok {
			moved[newIndex] = value
		}
	}
	for index := range collapse {
		if index >= start {
			delete(collapse, index)
		}
	}
	maps.Copy(collapse, moved)
	return collapse
}
