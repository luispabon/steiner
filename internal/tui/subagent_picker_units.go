package tui

import (
	"cmp"
	"math"
	"slices"
)

// subAgentPickerUnits groups the non-advisor occurrences by segment, sorted newest first.
func (b *contentBuffer) subAgentPickerUnits() []pickerUnit {
	bySeg := b.bucketPickerOccurrences()
	units := make([]pickerUnit, 0, len(bySeg))
	for seg, u := range bySeg {
		b.finalizePickerUnit(seg, u)
		units = append(units, *u)
	}
	slices.SortFunc(units, func(a, c pickerUnit) int {
		if d := cmp.Compare(c.age, a.age); d != 0 {
			return d
		}
		return cmp.Compare(c.seg, a.seg)
	})
	return units
}

// pickerSegment resolves the segment that renders dd, or -1 when none does.
func (b *contentBuffer) pickerSegment(loc delegationLocator, dd *delegationDisplayState) int {
	seg := loc.seg
	if seg < 0 || seg >= len(b.segments) || !segmentHoldsDelegation(b.segments[seg], dd) {
		return findDelegationSegment(b.segments, dd)
	}
	return seg
}

// bucketPickerOccurrences buckets non-advisor occurrences by their segment.
func (b *contentBuffer) bucketPickerOccurrences() map[int]*pickerUnit {
	bySeg := map[int]*pickerUnit{}
	for key, loc := range b.delegations {
		dd := loc.dd
		if dd == nil || dd.isAdvisor {
			continue
		}
		seg := b.pickerSegment(loc, dd)
		if seg < 0 {
			continue
		}
		u := bySeg[seg]
		if u == nil {
			u = &pickerUnit{seg: seg}
			if g := b.segments[seg].delegGroupData; g != nil {
				u.grouped = true
				u.group = delegationVisualGroupName(g)
			}
			bySeg[seg] = u
		}
		if u.grouped && u.group == "" && dd.admission != nil {
			u.group = dd.admission.Group
		}
		u.members = append(u.members, pickerMember{key: key, dd: dd})
	}
	return bySeg
}

// finalizePickerUnit orders a unit's members and computes its running state and age.
func (b *contentBuffer) finalizePickerUnit(seg int, u *pickerUnit) {
	if g := b.segments[seg].delegGroupData; g != nil {
		order := map[*delegationDisplayState]int{}
		for i, e := range g.entries {
			order[e] = i
		}
		slices.SortFunc(u.members, func(a, c pickerMember) int { return cmp.Compare(order[a.dd], order[c.dd]) })
	}
	// A nameless group has no frame label, so its members render as plain rows.
	u.grouped = u.grouped && u.group != ""
	for _, mem := range u.members {
		if mem.dd.status == "active" {
			u.running = true
		}
		age := mem.dd.startTime
		if age == 0 {
			age = math.MaxInt64
		}
		u.age = max(u.age, age)
	}
}
