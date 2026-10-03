package tui

import tea "charm.land/bubbletea/v2"

// rosterAgentAt returns the agent ID of the clickable sidebar roster row at
// screen cell (x, y), or "" when the cell is not on one. The column checks run
// first so pointer motion away from the sidebar stays cheap.
func (m *Model) rosterAgentAt(x, y int) string {
	if !m.sidebar.Visible(m.width) || len(m.sidebar.subAgents) == 0 {
		return ""
	}
	left := 0
	if m.sidebarPosition == "right" {
		left = m.width - sidebarWidth
	}
	col := x - left
	if col < sidebarPadH || col >= sidebarWidth-sidebarPadH {
		return ""
	}
	innerHeight := max(0, m.height-2*sidebarPadV)
	return m.sidebar.rosterTargetAtRow(innerHeight, y-sidebarPadV)
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
