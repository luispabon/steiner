package tui

import "github.com/luispabon/steiner/internal/tui/theme"

// settledRowsKey lists the render inputs, outside an entry's own data, that a
// settled entry's box lines depend on. A change to any of them starts a fresh
// settled cache.
type settledRowsKey struct {
	width   int
	bodyCap int
	styles  *theme.Styles
}

// settledGroupRows caches the box lines of a group's settled entries (status
// other than "active"). Spinner ticks never set renderDirty, so they do not
// flush it. processSegment flushes it on every real mutation of the group.
type settledGroupRows struct {
	key   settledRowsKey
	lines map[*delegationDisplayState][]string
}

// groupEntryLines returns the box lines of one group entry at width. A settled
// entry comes from group.settled when the render inputs still match. Active
// entries and the entry under a jump flash are always rendered fresh.
func (b *contentBuffer) groupEntryLines(group *delegationGroupSegment, dd *delegationDisplayState, width int) []string {
	var flash *delegationDisplayState
	if b.jumpFlash.on {
		flash = b.jumpFlash.dd
	}
	if dd == nil || dd.status == "active" || dd == flash {
		return b.renderDelegationBoxRows(dd, width)
	}
	cache := &group.settled
	key := settledRowsKey{width: width, bodyCap: b.maxDelegationBodyLines, styles: b.styles}
	if cache.lines == nil || cache.key != key {
		cache.key = key
		cache.lines = make(map[*delegationDisplayState][]string, len(group.entries))
	}
	if lines, ok := cache.lines[dd]; ok {
		return lines
	}
	lines := b.renderDelegationBoxRows(dd, width)
	cache.lines[dd] = lines
	return lines
}
