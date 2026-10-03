package tui

import (
	"slices"
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
	m.sidebar.Toggle()
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeCellMotion {
		t.Errorf("sidebar hidden: mode = %v, want cell motion", got)
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
		_, cmd := m.handleSidebarHover(sidebarHoverMsg{agentID: step.agentID})
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

// hoverRow points at agentID's rendered row and applies the resulting hover
// through Update, as the program does.
func hoverRow(t *testing.T, m *Model, agentID string) int {
	t.Helper()
	row := renderedSidebarRow(m, agentID)
	if row < 0 {
		t.Fatalf("agent %q not rendered", agentID)
	}
	if msg := filterMouseMotion(m, tea.MouseMotionMsg{X: sidebarPadH, Y: row}); msg != nil {
		updateModelDirect(m, msg)
	}
	if m.sidebar.rosterHover != agentID || !m.pointerHand {
		t.Fatalf("hover = %q hand = %v, want %q with hand", m.sidebar.rosterHover, m.pointerHand, agentID)
	}
	return row
}

// TestReconcileRosterHover covers hover changing without pointer motion: the
// pointer must not stay a hand over a row that is gone, covered or replaced.
func TestReconcileRosterHover(t *testing.T) {
	arrow := tea.RawMsg{Msg: ansi.SetPointerShape("default")}
	tests := []struct {
		name      string
		start     string
		change    func(m *Model)
		wantHover func(m *Model, row int) string
	}{
		{"sidebar toggled off", "solo", func(m *Model) { m.sidebar.Toggle() }, nil},
		{"resize hides the sidebar", "solo", func(m *Model) { m.width = sidebarMinWidth - 1 }, nil},
		{"roster emptied", "solo", func(m *Model) { m.sidebar.subAgents = nil }, nil},
		{"overlay opened", "solo", func(m *Model) { m.mcpOverlay = m.mcpOverlay.Open(nil, true) }, nil},
		{
			"rows reorder under a still pointer",
			"g1",
			func(m *Model) {
				m.roster.entries["solo"].status = rosterDone
				m.roster.entries["solo"].finishTime = 5
				m.syncRoster()
			},
			func(m *Model, row int) string {
				return m.cachedRosterLayout().targetAt(max(0, m.height-2*sidebarPadV), row-sidebarPadV)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newSidebarClickModel(t, "left", 0)
			row := hoverRow(t, m, tc.start)
			tc.change(m)
			cmd := m.reconcileRosterHover()
			want := ""
			if tc.wantHover != nil {
				want = tc.wantHover(m, row)
				if want == tc.start || want == "" {
					t.Fatalf("reorder left %q under the pointer; test setup is wrong", want)
				}
			}
			if m.sidebar.rosterHover != want {
				t.Errorf("rosterHover = %q, want %q", m.sidebar.rosterHover, want)
			}
			if want == "" {
				if cmd == nil || cmd() != arrow || m.pointerHand {
					t.Errorf("pointer not restored to the arrow (cmd nil=%v, hand=%v)", cmd == nil, m.pointerHand)
				}
			} else if cmd != nil {
				t.Errorf("hover moved between rows but sent pointer command %#v", cmd())
			}
		})
	}
}

// TestUpdateReconcilesRosterHover checks Update runs the reconcile: toggling
// the sidebar off with its key clears hover and the hand.
func TestUpdateReconcilesRosterHover(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	hoverRow(t, m, "solo")
	updateModelDirect(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.sidebar.Visible(m.width) {
		t.Fatal("ctrl+b did not hide the sidebar")
	}
	if m.sidebar.rosterHover != "" || m.pointerHand {
		t.Errorf("hover = %q hand = %v after hiding the sidebar", m.sidebar.rosterHover, m.pointerHand)
	}
}

func TestRosterAgentAtIgnoresOverlays(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	row := renderedSidebarRow(m, "solo")
	if got := m.rosterAgentAt(sidebarPadH, row); got != "solo" {
		t.Fatalf("rosterAgentAt = %q, want solo", got)
	}
	m.mcpOverlay = m.mcpOverlay.Open(nil, true)
	if got := m.rosterAgentAt(sidebarPadH, row); got != "" {
		t.Errorf("rosterAgentAt under an overlay = %q, want none", got)
	}
	if cmd := m.sidebarRosterClick(sidebarPadH, row); cmd != nil {
		t.Error("click under an overlay jumped")
	}
}

func TestCachedRosterLayout(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	first := m.cachedRosterLayout()

	m.sidebar.tickCount++
	m.sidebar.subAgentsNow += 1_000_000_000
	m.sidebar.rosterHover = "solo"
	if got := m.cachedRosterLayout(); got.prefix != first.prefix || !slices.Equal(got.targets, first.targets) {
		t.Errorf("glyph-only changes altered the layout: %+v vs %+v", got, first)
	}
	if want := m.sidebar.rosterLayout(); !slices.Equal(first.targets, want.targets) || first.prefix != want.prefix {
		t.Errorf("cached layout %+v != fresh %+v", first, want)
	}

	m.sidebar.version = "0.27.0-8-ga17d550e-dirty-and-then-some"
	if got := m.cachedRosterLayout(); got.prefix != m.sidebar.rosterLayout().prefix || got.prefix == first.prefix {
		t.Errorf("version wrap: cached prefix %d, fresh %d, before %d", got.prefix, m.sidebar.rosterLayout().prefix, first.prefix)
	}

	m.roster.entries["solo"].status = rosterDone
	m.roster.entries["solo"].finishTime = 5
	m.syncRoster()
	if got, want := m.cachedRosterLayout(), m.sidebar.rosterLayout(); !slices.Equal(got.targets, want.targets) {
		t.Errorf("roster change: cached targets %q, fresh %q", got.targets, want.targets)
	}
}
