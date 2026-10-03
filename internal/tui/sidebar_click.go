package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// rosterLayoutCache memoizes rosterLayout so hit-testing pointer motion does
// not re-render the sidebar. It keys on the sidebar state minus the fields that
// change glyphs but never line counts (spinner frame, elapsed clock, hover).
type rosterLayoutCache struct {
	set    bool
	key    sidebarStateComparable
	roster []rosterEntry
	layout rosterLayout
}

func (m *Model) cachedRosterLayout() rosterLayout {
	key := m.sidebar.comparable()
	key.tickCount, key.subAgentsNow, key.rosterHover = 0, 0, ""
	c := &m.rosterLayoutCache
	if !c.set || c.key != key || !slices.Equal(c.roster, m.sidebar.subAgents) {
		*c = rosterLayoutCache{
			set:    true,
			key:    key,
			roster: append([]rosterEntry(nil), m.sidebar.subAgents...),
			layout: m.sidebar.rosterLayout(),
		}
	}
	return c.layout
}

// rosterAgentAt returns the agent ID of the clickable sidebar roster row at
// screen cell (x, y), or "" when the cell is not on one or an overlay is open.
// The cheap checks run first so pointer motion away from the roster stays cheap.
func (m *Model) rosterAgentAt(x, y int) string {
	if !m.sidebar.Visible(m.width) || len(m.sidebar.subAgents) == 0 {
		return ""
	}
	left := 0
	if m.sidebarPosition == "right" {
		left = m.width - sidebarWidth
	}
	col := x - left
	if col < sidebarPadH || col >= sidebarWidth-sidebarPadH || m.anyOverlayOpen() {
		return ""
	}
	innerHeight := max(0, m.height-2*sidebarPadV)
	return m.cachedRosterLayout().targetAt(innerHeight, y-sidebarPadV)
}

// sidebarRosterClick maps a screen cell to a sidebar roster row and jumps to the
// card that row shows. It returns nil when the cell is not on a clickable row.
func (m *Model) sidebarRosterClick(x, y int) tea.Cmd {
	id := m.rosterAgentAt(x, y)
	if id == "" {
		return nil
	}
	entry, ok := m.roster.entries[id]
	if !ok || entry.occurrence == (occurrenceKey{}) {
		return nil
	}
	return m.jumpToDelegation(entry.occurrence)
}
