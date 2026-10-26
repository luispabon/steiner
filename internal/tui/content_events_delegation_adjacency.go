package tui

// isUnnamedSpecialist reports whether dd is a specialist card outside any named
// group. Advisors never join groups, so they are excluded.
func isUnnamedSpecialist(dd *delegationDisplayState) bool {
	if dd == nil || dd.isAdvisor {
		return false
	}
	_, named := acceptedMembership(dd)
	return !named
}

func allUnnamedSpecialists(entries []*delegationDisplayState) bool {
	for _, dd := range entries {
		if !isUnnamedSpecialist(dd) {
			return false
		}
	}
	return true
}

// appendAdjacentSpecialist joins dd to the tail segment when both are unnamed
// specialists. Adjacency is strict: any other tail segment or card (thinking,
// plain text, separators, status, an advisor card) leaves dd standalone. It
// reports whether dd was joined to the tail.
func (b *contentBuffer) appendAdjacentSpecialist(dd *delegationDisplayState) bool {
	if !isUnnamedSpecialist(dd) || len(b.segments) == 0 {
		return false
	}
	idx := len(b.segments) - 1
	last := &b.segments[idx]
	switch last.kind {
	case segmentDelegation:
		if !isUnnamedSpecialist(last.delegData) {
			return false
		}
		b.invalidatePrefixIfCached(idx)
		last.delegGroupData = &delegationGroupSegment{entries: []*delegationDisplayState{last.delegData, dd}}
		last.delegData = nil
		last.kind = segmentDelegationGroup
	case segmentDelegationGroup:
		if last.delegGroupData == nil || !allUnnamedSpecialists(last.delegGroupData.entries) {
			return false
		}
		b.invalidatePrefixIfCached(idx)
		last.delegGroupData.entries = append(last.delegGroupData.entries, dd)
	default:
		return false
	}
	last.renderDirty = true
	return true
}

// appendUnnamedRun appends one run of adjacent unnamed specialists to next. A
// run of one stays a standalone card. A longer run becomes one unnamed group at
// the first member's position. oldToNew maps every run member's old index.
func appendUnnamedRun(next []contentSegment, oldToNew map[int]int, run []delegationSegmentToken, base int) []contentSegment {
	if len(run) == 0 {
		return next
	}
	at := base + len(next)
	for _, tok := range run {
		oldToNew[tok.old] = at
	}
	seg := run[0].segment
	seg.renderDirty = true
	if len(run) == 1 {
		seg.kind, seg.delegData, seg.delegGroupData = segmentDelegation, run[0].dd, nil
		return append(next, seg)
	}
	entries := make([]*delegationDisplayState, 0, len(run))
	for _, tok := range run {
		entries = append(entries, tok.dd)
	}
	seg.kind, seg.delegData = segmentDelegationGroup, nil
	seg.delegGroupData = &delegationGroupSegment{entries: entries}
	return append(next, seg)
}
