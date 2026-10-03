package tui

import tea "charm.land/bubbletea/v2"

// sidebarRosterClick maps a screen cell to a sidebar roster row and jumps to the
// card that row shows. It returns nil when the cell is not on a clickable row.
func (m *Model) sidebarRosterClick(x, y int) tea.Cmd {
	if !m.sidebar.Visible(m.width) {
		return nil
	}
	left := 0
	if m.sidebarPosition == "right" {
		left = m.width - sidebarWidth
	}
	col := x - left
	if col < sidebarPadH || col >= sidebarWidth-sidebarPadH {
		return nil
	}
	innerHeight := max(0, m.height-2*sidebarPadV)
	id := m.sidebar.rosterTargetAtRow(innerHeight, y-sidebarPadV)
	if id == "" {
		return nil
	}
	entry, ok := m.roster.entries[id]
	if !ok || entry.occurrence == (occurrenceKey{}) {
		return nil
	}
	return m.jumpToDelegation(entry.occurrence)
}
