package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// sidebarHoverMsg sets the sidebar roster row under the pointer ("" clears it).
type sidebarHoverMsg struct{ agentID string }

// filterMouseMotion is the program message filter. View enables all-motion
// mouse reporting while the roster is shown so rows can highlight on hover;
// buttonless motion becomes a sidebarHoverMsg only when the hovered row
// changes and is dropped otherwise, so plain pointer movement costs no Update
// or render. Everything else, including drag motion, passes through.
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	motion, ok := msg.(tea.MouseMotionMsg)
	if !ok {
		return msg
	}
	mouse := motion.Mouse()
	if mouse.Button != tea.MouseNone {
		return msg
	}
	m, ok := model.(*Model)
	if !ok {
		return msg
	}
	m.lastMouseMotionAt = time.Now()
	id := m.rosterAgentAt(mouse.X, mouse.Y)
	if id == m.sidebar.rosterHover {
		return nil
	}
	return sidebarHoverMsg{agentID: id}
}

// rosterHoverMouseMode reports the mouse mode View requests: all motion while
// a hoverable roster is visible, cell motion (drag only) otherwise.
func (m *Model) rosterHoverMouseMode() tea.MouseMode {
	if m.sidebar.Visible(m.width) && len(m.sidebar.subAgents) > 0 {
		return tea.MouseModeAllMotion
	}
	return tea.MouseModeCellMotion
}
