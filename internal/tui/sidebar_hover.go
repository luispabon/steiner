package tui

import (
	"net/url"
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
	if id == m.sidebar.rosterHover {
		return nil
	}
	return sidebarHoverMsg{agentID: id}
}

// reconcileRosterHover re-derives hover from the last pointer position after
// every Update, so the highlight follows roster changes, overlays and the
// sidebar hiding without waiting for motion that cell-motion mode would never
// report.
func (m *Model) reconcileRosterHover() {
	id := ""
	if m.pointer.known {
		id = m.rosterAgentAt(m.pointer.x, m.pointer.y)
	}
	m.sidebar.rosterHover = id
}

// rosterLink wraps a clickable roster row in an OSC 8 hyperlink. Terminals
// show their own link affordance over it (underline and a hand pointer in
// VTE, kitty, WezTerm, Ghostty and foot) even while the app captures the
// mouse, which no pointer-shape sequence achieves portably. Plain clicks still
// reach the app; only a terminal's open-link gesture (e.g. ctrl+click) acts on
// the URI, which no handler is registered for.
func rosterLink(agentID, row string) string {
	return ansi.SetHyperlink("steiner://agent/"+url.PathEscape(agentID)) + row + ansi.ResetHyperlink()
}

// rosterHoverMouseMode reports the mouse mode View requests: cell motion
// (drag only) when an overlay is open (no hovering possible), all motion while
// a hoverable roster is visible and no overlay blocks interaction, or cell
// motion otherwise. Mode changes are free: Bubble Tea applies MouseMode only
// when it differs from the last frame.
func (m *Model) rosterHoverMouseMode() tea.MouseMode {
	if m.anyOverlayOpen() {
		return tea.MouseModeCellMotion
	}
	if m.sidebar.Visible(m.width) && len(m.sidebar.subAgents) > 0 {
		return tea.MouseModeAllMotion
	}
	return tea.MouseModeCellMotion
}
