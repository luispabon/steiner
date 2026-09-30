package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type delegationRun struct {
	indices []int
	entries []*delegationDisplayState
}

//nolint:gocyclo // scans source segments once to derive hidden-aware run topology.
func (b *contentBuffer) updateDelegationRuns(width int) {
	oldJoined := make([]bool, len(b.segments))
	oldFinal := make([]bool, len(b.segments))
	oldMutable := make([]bool, len(b.segments))
	for i := range b.segments {
		oldJoined[i] = b.segments[i].delegationJoinedAbove
		oldFinal[i] = b.segments[i].delegationRunFinal
		oldMutable[i] = b.segments[i].delegationRunMutable
	}

	var runs []delegationRun
	var current *delegationRun
	flush := func() {
		if current != nil {
			runs = append(runs, *current)
			current = nil
		}
	}
	for i := range b.segments {
		seg := &b.segments[i]
		if b.isSegmentHidden(i) {
			continue
		}
		if !isDelegationRunSegment(seg) {
			flush()
			continue
		}
		if current == nil {
			current = &delegationRun{}
		}
		current.indices = append(current.indices, i)
		current.entries = append(current.entries, delegationSegmentEntries(seg)...)
	}
	flush()

	member := make([]bool, len(b.segments))
	prefixInvalid := false
	for _, run := range runs {
		terminal := run.indices[len(run.indices)-1] == lastVisibleSegmentIndex(b)
		mutable := terminal
		for _, idx := range run.indices {
			for _, dd := range delegationSegmentEntries(&b.segments[idx]) {
				if dd.status == "active" {
					mutable = true
				}
			}
		}
		dirty := false
		for _, idx := range run.indices {
			member[idx] = true
			seg := &b.segments[idx]
			dirty = dirty || seg.renderDirty || segmentHasActiveDelegation(seg) || seg.cachedRender == "" || seg.cachedRenderWidth != width
			joined := idx != run.indices[0]
			final := idx == run.indices[len(run.indices)-1]
			if oldJoined[idx] != joined || oldFinal[idx] != final || oldMutable[idx] != mutable {
				dirty = true
			}
			seg.delegationJoinedAbove = joined
			seg.delegationRunFinal = final
			seg.delegationRunMutable = mutable
		}
		if dirty {
			for _, idx := range run.indices {
				b.segments[idx].renderDirty = true
				if idx < b.prefixCacheLen {
					prefixInvalid = true
				}
			}
		}
		if mutable {
			for _, idx := range run.indices {
				if idx < b.prefixCacheLen {
					prefixInvalid = true
				}
			}
		}
	}
	for i := range b.segments {
		if member[i] {
			continue
		}
		seg := &b.segments[i]
		if oldJoined[i] || oldFinal[i] || oldMutable[i] {
			seg.renderDirty = true
			if i < b.prefixCacheLen {
				prefixInvalid = true
			}
		}
		seg.delegationJoinedAbove = false
		seg.delegationRunFinal = false
		seg.delegationRunMutable = false
	}
	if prefixInvalid {
		b.prefixCacheSet = false
	}
}

func lastVisibleSegmentIndex(b *contentBuffer) int {
	for i := len(b.segments) - 1; i >= 0; i-- {
		if !b.isSegmentHidden(i) {
			return i
		}
	}
	return -1
}

func isDelegationRunSegment(seg *contentSegment) bool {
	entries := delegationSegmentEntries(seg)
	if len(entries) == 0 {
		return false
	}
	for _, dd := range entries {
		if dd == nil || dd.isAdvisor {
			return false
		}
	}
	return true
}

func containsDelegationKind(kinds []contentSegmentKind) bool {
	for _, kind := range kinds {
		if isDelegationSegment(kind) {
			return true
		}
	}
	return false
}

func delegationSegmentEntries(seg *contentSegment) []*delegationDisplayState {
	switch seg.kind {
	case segmentDelegation:
		if seg.delegData != nil {
			return []*delegationDisplayState{seg.delegData}
		}
	case segmentDelegationGroup:
		if seg.delegGroupData != nil {
			return seg.delegGroupData.entries
		}
	}
	return nil
}

func (b *contentBuffer) renderDelegationRunFragment(index, width int) string {
	seg := &b.segments[index]
	entries := delegationSegmentEntries(seg)
	if len(entries) == 0 {
		return ""
	}
	if width < 12 {
		width = 12
	}
	rows := make([]string, 0, len(entries)*8)
	for i, dd := range entries {
		rows = append(rows, b.renderDelegationBoxRows(dd, width)...)
		if i < len(entries)-1 {
			rows = append(rows, renderDelegationFragmentDivider(width))
		}
	}
	_, borderStyle := b.delegationStyles(delegationRunBorderLabel(b, index))
	box := renderStyledBox(strings.Join(rows, "\n"), borderStyle.GetForeground(), lipgloss.Color(b.styles.Palette.ContentBG), width)
	lines := strings.Split(box, "\n")
	if seg.delegationJoinedAbove {
		lines[0] = renderDelegationFragmentDivider(lipgloss.Width(lines[0]))
	}
	var full []*delegationDisplayState
	for i := index; i >= 0; i-- {
		if b.isSegmentHidden(i) {
			continue
		}
		entries := delegationSegmentEntries(&b.segments[i])
		if len(entries) == 0 {
			break
		}
		full = append(entries, full...)
		if !b.segments[i].delegationJoinedAbove {
			break
		}
	}
	if seg.delegationRunFinal && delegationVisualGroupName(&delegationGroupSegment{entries: full}) != "" {
		group := &delegationGroupSegment{entries: full}
		lines[len(lines)-1] = b.renderDelegationGroupFooter(group, width-2, borderStyle.GetForeground())
	} else {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderDelegationFragmentDivider(width int) string {
	if width < 2 {
		width = 2
	}
	return "│" + strings.Repeat("─", width-2) + "│"
}

func delegationRunBorderLabel(b *contentBuffer, index int) string {
	entries := append([]*delegationDisplayState(nil), delegationSegmentEntries(&b.segments[index])...)
	if !b.segments[index].delegationJoinedAbove {
		return delegationGroupBorderLabel(&delegationGroupSegment{entries: entries})
	}
	for i := index - 1; i >= 0; i-- {
		if b.isSegmentHidden(i) {
			continue
		}
		if !isDelegationRunSegment(&b.segments[i]) {
			break
		}
		previous := delegationSegmentEntries(&b.segments[i])
		entries = append(previous, entries...)
		if !b.segments[i].delegationJoinedAbove {
			break
		}
	}
	for i := index + 1; i < len(b.segments); i++ {
		if b.isSegmentHidden(i) {
			continue
		}
		if !b.segments[i].delegationJoinedAbove {
			break
		}
		next := delegationSegmentEntries(&b.segments[i])
		if len(next) == 0 {
			break
		}
		entries = append(entries, next...)
	}
	return delegationGroupBorderLabel(&delegationGroupSegment{entries: entries})
}
