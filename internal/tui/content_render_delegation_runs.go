package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

type delegationRun struct {
	indices []int
	entries []*delegationDisplayState
}

//nolint:gocyclo // scans source segments once to derive hidden-aware run topology.
func (b *contentBuffer) updateDelegationRuns(width int) {
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
		seg.delegationJoinedAbove = false
		if b.isSegmentHidden(i) {
			continue
		}
		entries := delegationSegmentEntries(seg)
		if len(entries) == 0 {
			flush()
			continue
		}
		eligible := true
		for _, dd := range entries {
			if dd == nil || dd.isAdvisor {
				eligible = false
				break
			}
		}
		if !eligible {
			flush()
			continue
		}
		if current == nil {
			current = &delegationRun{}
		}
		current.indices = append(current.indices, i)
		current.entries = append(current.entries, entries...)
	}
	flush()

	var key strings.Builder
	fmt.Fprintf(&key, "%d/%t/", width, b.showThinking)
	for _, run := range runs {
		grouped := &delegationGroupSegment{entries: run.entries}
		fmt.Fprintf(&key, "%v:%q:%q;", run.indices, delegationVisualGroupName(grouped), delegationGroupBorderLabel(grouped))
		for _, dd := range run.entries {
			fmt.Fprintf(&key, "%p:%q:%q:%q:%q;", dd, dd.group, dd.effectiveTypeLabel(), dd.status, dd.taskPreview)
		}
		for j, idx := range run.indices {
			b.segments[idx].delegationJoinedAbove = j > 0
		}
	}
	newKey := key.String()
	if b.delegationRunKey == newKey {
		return
	}
	b.delegationRunKey = newKey
	for _, run := range runs {
		for _, idx := range run.indices {
			b.segments[idx].renderDirty = true
		}
	}
	b.gen++
	b.stringCacheRendered = ""
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
	isFinal := true
	var full []*delegationDisplayState
	for i := index + 1; i < len(b.segments); i++ {
		if b.isSegmentHidden(i) {
			continue
		}
		if b.segments[i].delegationJoinedAbove {
			isFinal = false
		}
		break
	}
	if isFinal {
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
		group := &delegationGroupSegment{entries: full}
		if delegationVisualGroupName(group) != "" {
			lines[len(lines)-1] = b.renderDelegationGroupFooter(group, width-2, borderStyle.GetForeground())
		}
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
