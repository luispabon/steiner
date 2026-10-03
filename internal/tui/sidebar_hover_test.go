package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFilterMouseMotion(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	row := renderedSidebarRow(m, "solo")
	if row < 0 {
		t.Fatal("solo not rendered")
	}
	onSolo := tea.MouseMotionMsg{X: sidebarPadH, Y: row}
	offSidebar := tea.MouseMotionMsg{X: sidebarWidth + 10, Y: row}

	key := tea.KeyPressMsg{Text: "a"}
	if got := filterMouseMotion(m, key); got != key {
		t.Errorf("non-mouse message = %#v, want passthrough", got)
	}
	drag := tea.MouseMotionMsg{X: sidebarPadH, Y: row, Button: tea.MouseLeft}
	if got := filterMouseMotion(m, drag); got != drag {
		t.Errorf("drag motion = %#v, want passthrough", got)
	}

	steps := []struct {
		name string
		msg  tea.MouseMotionMsg
		want tea.Msg
	}{
		{"pointer away from sidebar with no hover is dropped", offSidebar, nil},
		{"entering a row hovers it", onSolo, sidebarHoverMsg{agentID: "solo"}},
		{"moving within the same row is dropped", tea.MouseMotionMsg{X: sidebarPadH + 3, Y: row}, nil},
		{"moving to a non-clickable row clears", tea.MouseMotionMsg{X: sidebarPadH, Y: row - 1}, sidebarHoverMsg{}},
		{"re-entering hovers again", onSolo, sidebarHoverMsg{agentID: "solo"}},
		{"leaving the sidebar clears", offSidebar, sidebarHoverMsg{}},
	}
	for _, step := range steps {
		m.lastMouseMotionAt = time.Time{}
		got := filterMouseMotion(m, step.msg)
		if got != step.want {
			t.Fatalf("%s: got %#v, want %#v", step.name, got, step.want)
		}
		if m.lastMouseMotionAt.IsZero() {
			t.Errorf("%s: lastMouseMotionAt not recorded", step.name)
		}
		if hover, ok := got.(sidebarHoverMsg); ok {
			updateModelDirect(m, hover)
			if m.sidebar.rosterHover != hover.agentID {
				t.Fatalf("%s: rosterHover = %q, want %q", step.name, m.sidebar.rosterHover, hover.agentID)
			}
		}
	}
}

func TestRosterHoverRendering(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	w := sidebarWidth - 2*sidebarPadH
	plain, _ := m.sidebar.subAgentsRows(w)
	before := m.renderSidebar(m.width, m.height)

	m.sidebar.rosterHover = "solo"
	hovered, targets := m.sidebar.subAgentsRows(w)
	for i, tg := range targets {
		changed := hovered[i] != plain[i]
		if changed != (tg == "solo") {
			t.Errorf("row %d (target %q): changed = %v", i, tg, changed)
		}
		if lipgloss.Width(hovered[i]) != lipgloss.Width(plain[i]) {
			t.Errorf("row %d width %d, want %d", i, lipgloss.Width(hovered[i]), lipgloss.Width(plain[i]))
		}
	}
	if m.renderSidebar(m.width, m.height) == before {
		t.Error("render cache served the unhovered sidebar")
	}
}

func TestRosterHoverMouseMode(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeAllMotion {
		t.Errorf("roster visible: mode = %v, want all motion", got)
	}
	m.sidebar.rosterHover = "solo"
	m.sidebar.Toggle()
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeCellMotion {
		t.Errorf("sidebar hidden: mode = %v, want cell motion", got)
	}
	if m.sidebar.rosterHover != "" {
		t.Errorf("hiding the sidebar kept hover %q", m.sidebar.rosterHover)
	}
	m.sidebar.Toggle()
	m.sidebar.subAgents = nil
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeCellMotion {
		t.Errorf("empty roster: mode = %v, want cell motion", got)
	}
}

func TestRecentMouseInputCountsMotion(t *testing.T) {
	m := &Model{}
	if m.recentMouseInput() {
		t.Error("no mouse input reported as recent")
	}
	m.lastMouseMotionAt = time.Now()
	if !m.recentMouseInput() {
		t.Error("recent pointer motion not reported")
	}
	m.lastMouseMotionAt = time.Now().Add(-time.Second)
	if m.recentMouseInput() {
		t.Error("stale pointer motion reported as recent")
	}
}

func TestSidebarHoverPointerShape(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	steps := []struct {
		name    string
		agentID string
		want    tea.Msg // nil means no command
	}{
		{"hovering a row shows the hand", "solo", tea.RawMsg{Msg: ansi.SetPointerShape("pointer")}},
		{"moving to another row keeps the hand", "g1", nil},
		{"leaving the rows restores the arrow", "", tea.RawMsg{Msg: ansi.SetPointerShape("default")}},
		{"staying off the rows sends nothing", "", nil},
	}
	for _, step := range steps {
		_, cmd := m.Update(sidebarHoverMsg{agentID: step.agentID})
		var got tea.Msg
		if cmd != nil {
			got = cmd()
		}
		if got != step.want {
			t.Errorf("%s: cmd msg = %#v, want %#v", step.name, got, step.want)
		}
		if m.sidebar.rosterHover != step.agentID {
			t.Errorf("%s: rosterHover = %q", step.name, m.sidebar.rosterHover)
		}
	}
}

// TestFilterMouseMotionRestoresPointerAfterHoverCleared covers hover cleared
// without motion: the next motion must still restore the default pointer.
func TestFilterMouseMotionRestoresPointerAfterHoverCleared(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	row := renderedSidebarRow(m, "solo")
	updateModelDirect(m, filterMouseMotion(m, tea.MouseMotionMsg{X: sidebarPadH, Y: row}))
	if !m.pointerHand {
		t.Fatal("hovering did not set the hand pointer")
	}
	m.sidebar.Toggle()
	m.sidebar.Toggle()
	if got := filterMouseMotion(m, tea.MouseMotionMsg{X: sidebarWidth + 10, Y: row}); got != (sidebarHoverMsg{}) {
		t.Errorf("motion after hover cleared = %#v, want a clear to restore the pointer", got)
	}
}

func TestResizeHidingSidebarClearsHover(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	m.sidebar.rosterHover = "solo"
	updateModelDirect(m, tea.WindowSizeMsg{Width: sidebarMinWidth - 1, Height: 70})
	if m.sidebar.rosterHover != "" {
		t.Errorf("rosterHover = %q after the sidebar was hidden by resize", m.sidebar.rosterHover)
	}
}
