package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// sidebarHoverMsg sets the sidebar roster row under the pointer ("" clears it).
type sidebarHoverMsg struct{ agentID string }

// pointerPos is the last screen cell a mouse event reported.
type pointerPos struct {
	x, y  int
	known bool
}

// filterMouseMotion is the program message filter. It records the pointer
// position from every mouse event. View enables all-motion mouse reporting
// while the roster is shown so rows can highlight on hover; buttonless motion
// becomes a sidebarHoverMsg only when the hover changes and is dropped
// otherwise, so plain pointer movement costs no Update or render. Everything
// else, including drag motion, passes through.
//
// Bubble Tea runs the filter on its event loop goroutine with the model the
// last Update returned. Update always returns m itself, so mutating m here is
// race-free and persists; a non-*Model passes everything through.
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	mouseMsg, ok := msg.(tea.MouseMsg)
	if !ok {
		return msg
	}
	m, ok := model.(*Model)
	if !ok {
		return msg
	}
	mouse := mouseMsg.Mouse()
	m.pointer = pointerPos{x: mouse.X, y: mouse.Y, known: true}
	if _, motion := msg.(tea.MouseMotionMsg); !motion || mouse.Button != tea.MouseNone {
		return msg
	}
	m.lastMouseMotionAt = time.Now()
	id := m.rosterAgentAt(mouse.X, mouse.Y)
	if !m.hoverChanged(id) {
		return nil
	}
	return sidebarHoverMsg{agentID: id}
}

// hoverChanged reports whether hovering id differs from the current hover or
// pointer shape. Hover can be cleared without motion while the pointer is
// still the hand, so both are compared.
func (m *Model) hoverChanged(id string) bool {
	return id != m.sidebar.rosterHover || m.pointerHand != (id != "")
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

// reconcileRosterHover re-derives hover from the last pointer position after
// every Update, so the highlight and pointer shape follow roster changes,
// overlays and the sidebar hiding without waiting for motion that cell-motion
// mode would never report.
func (m *Model) reconcileRosterHover() tea.Cmd {
	id := ""
	if m.pointer.known {
		id = m.rosterAgentAt(m.pointer.x, m.pointer.y)
	}
	if !m.hoverChanged(id) {
		return nil
	}
	_, cmd := m.handleSidebarHover(sidebarHoverMsg{agentID: id})
	return cmd
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
