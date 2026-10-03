package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	// Hover can be cleared without motion (sidebar toggle, resize) while the
	// pointer is still the hand, so compare against both.
	if id == m.sidebar.rosterHover && m.pointerHand == (id != "") {
		return nil
	}
	return sidebarHoverMsg{agentID: id}
}

// handleSidebarHover applies a hover change and switches the terminal pointer
// between the link hand and the default arrow, like a link in a browser.
func (m *Model) handleSidebarHover(msg sidebarHoverMsg) (tea.Model, tea.Cmd) {
	m.sidebar.rosterHover = msg.agentID
	hand := msg.agentID != ""
	if hand == m.pointerHand {
		return m, nil
	}
	m.pointerHand = hand
	return m, tea.Raw(pointerShapeSeq(hand))
}

// pointerShapeSeq returns the OSC 22 sequence for the link hand or the
// default pointer. Terminals without OSC 22 support ignore it.
func pointerShapeSeq(hand bool) string {
	if hand {
		return ansi.SetPointerShape("pointer")
	}
	return ansi.SetPointerShape("default")
}

// rosterHoverMouseMode reports the mouse mode View requests: all motion while
// a hoverable roster is visible, cell motion (drag only) otherwise.
func (m *Model) rosterHoverMouseMode() tea.MouseMode {
	if m.sidebar.Visible(m.width) && len(m.sidebar.subAgents) > 0 {
		return tea.MouseModeAllMotion
	}
	return tea.MouseModeCellMotion
}
