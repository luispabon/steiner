package tui

import (
	"fmt"
	"slices"
	"strings"
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

	// Buttonless motion (hover path, unchanged):
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

	// Classified button/wheel/drag events:
	t.Run("left click classified", func(t *testing.T) {
		msg := tea.MouseClickMsg{X: sidebarPadH, Y: row, Button: tea.MouseLeft}
		got := filterMouseMotion(m, msg)
		want, ok := got.(mouseClickMsg)
		if !ok {
			t.Fatalf("got %T, want mouseClickMsg", got)
		}
		if want.x != sidebarPadH || want.y != row {
			t.Errorf("coords = (%d,%d), want (%d,%d)", want.x, want.y, sidebarPadH, row)
		}
	})

	t.Run("left release classified", func(t *testing.T) {
		msg := tea.MouseReleaseMsg{X: sidebarPadH, Y: row, Button: tea.MouseLeft}
		got := filterMouseMotion(m, msg)
		want, ok := got.(mouseReleaseMsg)
		if !ok {
			t.Fatalf("got %T, want mouseReleaseMsg", got)
		}
		if want.x != sidebarPadH || want.y != row {
			t.Errorf("coords = (%d,%d), want (%d,%d)", want.x, want.y, sidebarPadH, row)
		}
	})

	t.Run("wheel up classified", func(t *testing.T) {
		msg := tea.MouseWheelMsg{X: 40, Y: 10, Button: tea.MouseWheelUp}
		got := filterMouseMotion(m, msg)
		want, ok := got.(mouseWheelMsg)
		if !ok {
			t.Fatalf("got %T, want mouseWheelMsg", got)
		}
		if want.direction != "up" || want.x != 40 || want.y != 10 {
			t.Errorf("got direction=%q, x=%d, y=%d; want up, 40, 10", want.direction, want.x, want.y)
		}
		if want.raw != msg {
			t.Errorf("raw message not preserved")
		}
	})

	t.Run("wheel down classified", func(t *testing.T) {
		msg := tea.MouseWheelMsg{X: 40, Y: 10, Button: tea.MouseWheelDown}
		got := filterMouseMotion(m, msg)
		want, ok := got.(mouseWheelMsg)
		if !ok {
			t.Fatalf("got %T, want mouseWheelMsg", got)
		}
		if want.direction != "down" || want.x != 40 || want.y != 10 {
			t.Errorf("got direction=%q, x=%d, y=%d; want down, 40, 10", want.direction, want.x, want.y)
		}
		if want.raw != msg {
			t.Errorf("raw message not preserved")
		}
	})

	t.Run("left drag motion classified", func(t *testing.T) {
		msg := tea.MouseMotionMsg{X: sidebarPadH + 5, Y: row + 1, Button: tea.MouseLeft}
		got := filterMouseMotion(m, msg)
		want, ok := got.(mouseMotionMsg)
		if !ok {
			t.Fatalf("got %T, want mouseMotionMsg", got)
		}
		if want.x != sidebarPadH+5 || want.y != row+1 {
			t.Errorf("coords = (%d,%d), want (%d,%d)", want.x, want.y, sidebarPadH+5, row+1)
		}
	})

	t.Run("right click (unclassified) passes raw", func(t *testing.T) {
		msg := tea.MouseClickMsg{X: sidebarPadH, Y: row, Button: tea.MouseRight}
		got := filterMouseMotion(m, msg)
		if got != msg {
			t.Errorf("got %#v, want passthrough of %#v", got, msg)
		}
	})
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
	tests := []struct {
		name         string
		setupSidebar func(*Model)
		setupOverlay func(*Model)
		wantMode     tea.MouseMode
	}{
		{
			"roster visible and entries exist, no overlay",
			func(_ *Model) {
				// sidebar already visible with entries from newSidebarClickModel
			},
			func(_ *Model) {},
			tea.MouseModeAllMotion,
		},
		{
			"sidebar hidden",
			func(m *Model) {
				m.sidebar.Toggle()
			},
			func(_ *Model) {},
			tea.MouseModeCellMotion,
		},
		{
			"empty roster",
			func(m *Model) {
				m.sidebar.subAgents = nil
			},
			func(_ *Model) {},
			tea.MouseModeCellMotion,
		},
		{
			"exclusive overlay open (fileList)",
			func(_ *Model) {
				// sidebar already visible with entries
			},
			func(m *Model) {
				m.fileList = m.fileList.Open(".")
			},
			tea.MouseModeCellMotion,
		},
		{
			"bottom-anchored overlay open (slash)",
			func(_ *Model) {
				// sidebar already visible with entries
			},
			func(m *Model) {
				m.slashOverlay = m.slashOverlay.Open(m.buildSlashOverlayItems())
			},
			tea.MouseModeCellMotion,
		},
		{
			"modal overlay open (MCP)",
			func(_ *Model) {
				// sidebar already visible with entries
			},
			func(m *Model) {
				m.mcpOverlay = m.mcpOverlay.Open(nil, true)
			},
			tea.MouseModeCellMotion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newSidebarClickModel(t, "left", 0)
			tc.setupSidebar(m)
			tc.setupOverlay(m)
			got := m.rosterHoverMouseMode()
			if got != tc.wantMode {
				t.Errorf("mode = %v, want %v", got, tc.wantMode)
			}
		})
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

func TestMouseModeChangesWhenOverlayOpens(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)

	// Set hover on a roster row
	hoverRow(t, m, "solo")
	if m.sidebar.rosterHover != "solo" {
		t.Fatalf("setup: hover = %q, want solo", m.sidebar.rosterHover)
	}
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeAllMotion {
		t.Errorf("before overlay: mode = %v, want all motion", got)
	}

	// Open an overlay and trigger reconcile via Update
	m.fileList = m.fileList.Open(".")
	updateModelDirect(m, tea.WindowSizeMsg{Width: m.width, Height: m.height}) // trigger reconcile
	if m.sidebar.rosterHover != "" {
		t.Errorf("after overlay open: hover = %q, want cleared", m.sidebar.rosterHover)
	}
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeCellMotion {
		t.Errorf("with overlay open: mode = %v, want cell motion", got)
	}

	// Close overlay and trigger reconcile; hover restoration depends on pointer position
	// (which is still over the row from setup), so it should re-hover
	m.fileList = m.fileList.Close()
	updateModelDirect(m, tea.WindowSizeMsg{Width: m.width, Height: m.height})
	if m.sidebar.rosterHover != "solo" {
		t.Errorf("after overlay close: hover = %q, want restored to solo", m.sidebar.rosterHover)
	}
	if got := m.rosterHoverMouseMode(); got != tea.MouseModeAllMotion {
		t.Errorf("after overlay close: mode = %v, want all motion", got)
	}
}

// TestRosterRowsAreHyperlinks checks every clickable roster row reaches the
// final frame wrapped in its own OSC 8 link (which gives terminals their hand
// pointer and underline) and non-clickable lines carry none.
func TestRosterRowsAreHyperlinks(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	lines := strings.Split(m.View().Content, "\n")
	for _, id := range []string{"solo", "g1", "g2"} {
		row := renderedSidebarRow(m, id)
		if row < 0 || row >= len(lines) {
			t.Fatalf("agent %q not rendered", id)
		}
		open := ansi.SetHyperlink("steiner://agent/" + id)
		i := strings.Index(lines[row], open)
		if i < 0 {
			t.Errorf("row %d for %q has no link %q: %q", row, id, open, lines[row])
			continue
		}
		linked := lines[row][i+len(open):]
		end := strings.Index(linked, ansi.ResetHyperlink())
		if end < 0 {
			t.Errorf("row %d for %q never closes its link", row, id)
			continue
		}
		if text := ansi.Strip(linked[:end]); !strings.Contains(text, id) {
			t.Errorf("link for %q covers %q, want the agent's row text", id, text)
		}
	}
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), "SUB-AGENTS") || strings.Contains(ansi.Strip(line), "┌ grp") {
			if strings.Contains(line, "\x1b]8;") {
				t.Errorf("non-clickable line %d carries a link: %q", i, line)
			}
		}
	}
}

func TestRosterLinkEscapesAgentID(t *testing.T) {
	got := rosterLink("odd id/1", "x")
	if want := ansi.SetHyperlink("steiner://agent/odd%20id%2F1") + "x" + ansi.ResetHyperlink(); got != want {
		t.Errorf("rosterLink = %q, want %q", got, want)
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
	if m.sidebar.rosterHover != agentID {
		t.Fatalf("hover = %q, want %q", m.sidebar.rosterHover, agentID)
	}
	return row
}

// TestReconcileRosterHover covers hover changing without pointer motion: the
// highlight must not stay on a row that is gone, covered or replaced.
func TestReconcileRosterHover(t *testing.T) {
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
			m.reconcileRosterHover()
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
		})
	}
}

// TestUpdateReconcilesRosterHover checks Update runs the reconcile: toggling
// the sidebar off with its key clears hover.
func TestUpdateReconcilesRosterHover(t *testing.T) {
	m, _ := newSidebarClickModel(t, "left", 0)
	hoverRow(t, m, "solo")
	updateModelDirect(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.sidebar.Visible(m.width) {
		t.Fatal("ctrl+b did not hide the sidebar")
	}
	if m.sidebar.rosterHover != "" {
		t.Errorf("hover = %q after hiding the sidebar", m.sidebar.rosterHover)
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

// TestComposerWheelScrolling verifies that mouse wheel events still scroll an
// overflowing composer textarea, preserving pre-change behaviour where raw
// wheel messages reached the textarea via Update.
func TestComposerWheelScrolling(t *testing.T) {
	m := newModel(Config{
		Model:         "test",
		ModelContexts: map[string]int{"test": 1024},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetHeight(8) // Small viewport so text will overflow

	// Fill the composer with many lines so the content overflows
	for i := 0; i < 50; i++ {
		m.input.InsertString(fmt.Sprintf("Line %d content here\n", i))
	}
	viewBefore := m.input.View()

	// Send a wheel-up event through the normal dispatch path (filter + Update)
	wheelMsg := tea.MouseWheelMsg{X: 40, Y: 4, Button: tea.MouseWheelUp}
	filtered := filterMouseMotion(m, wheelMsg)
	_, _ = m.Update(filtered)
	viewAfter := m.input.View()

	// Verify the composer scrolled (view changed)
	if viewBefore == viewAfter {
		t.Error("composer view unchanged after wheel-up; wheel scrolling broken")
	}
}
