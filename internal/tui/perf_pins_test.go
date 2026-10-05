package tui

// Behaviour pins for the TUI performance programme. They fix user-visible
// invariants that later stages (wheel coalescing, tick gating) must keep, and
// drive the model through the frame-audit driver so the path matches the
// program's: filter -> Update -> View.

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

func TestWheelRoutingPin(t *testing.T) {
	const (
		scrollTranscript = "transcript"
		scrollOverlay    = "context overlay"
	)
	openContext := func(m *Model) {
		m.contextOverlay = openContextOverlay("Context", benchContextReport(), m.width, m.height, m.styles, m.content.glamourStyleSheet)
		m.contextOverlay.scrollOffset = (m.contextOverlay.lineCount - contextOverlayMaxLines) / 2
	}
	tests := []struct {
		name    string
		open    func(m *Model)
		inside  bool // pointer inside the context overlay bounds
		scrolls string
	}{
		{name: "no overlay", open: func(*Model) {}, scrolls: scrollTranscript},
		{name: "context overlay, pointer inside", open: openContext, inside: true, scrolls: scrollOverlay},
		{name: "context overlay, pointer outside", open: openContext, scrolls: scrollTranscript},
		{name: "mcp overlay", open: func(m *Model) { m.mcpOverlay = m.mcpOverlay.Open(nil, true) }, scrolls: scrollTranscript},
		{name: "lsp overlay", open: func(m *Model) { m.lspOverlay = m.lspOverlay.Open(nil, true) }, scrolls: scrollTranscript},
		{name: "help", open: func(m *Model) { m.helpVisible = true }, scrolls: scrollTranscript},
		{name: "slash overlay", open: func(m *Model) {
			m.slashOverlay = m.slashOverlay.Open([]slashOverlayItem{{command: "/clear", name: "Clear"}})
		}, scrolls: scrollTranscript},
		{name: "model picker", open: func(m *Model) { m.modelPicker = m.modelPicker.Open([]string{"a", "b"}, "a") }, scrolls: scrollTranscript},
		{name: "context overlay under mcp overlay, pointer inside", open: func(m *Model) {
			openContext(m)
			m.mcpOverlay = m.mcpOverlay.Open(nil, true)
		}, inside: true, scrolls: scrollTranscript},
	}
	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		}
		return 0
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := populateLongTranscript(newContentBenchModel(), 5)
			m.viewport.SetYOffset(m.viewport.maxYOffset() / 2)
			tc.open(m)
			x, y := 60, 20
			if tc.inside {
				bx, by, bw, bh := m.contextOverlayBounds()
				x, y = bx+bw/2, by+bh/2
			} else if m.contextOverlay.IsOpen() {
				x, y = 1, 1
			}
			d := newAuditDriver(m)
			for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown} {
				if m.viewport.AtTop() || m.viewport.AtBottom() {
					t.Fatalf("transcript must be scrollable both ways, yOffset=%d", m.viewport.YOffset())
				}
				if m.contextOverlay.IsOpen() && (m.contextOverlay.scrollOffset <= 0 ||
					m.contextOverlay.scrollOffset >= m.contextOverlay.lineCount-contextOverlayMaxLines) {
					t.Fatalf("context overlay must be scrollable both ways, offset=%d lines=%d", m.contextOverlay.scrollOffset, m.contextOverlay.lineCount)
				}
				vpBefore, ovBefore := m.viewport.YOffset(), m.contextOverlay.scrollOffset

				d.send(tea.MouseWheelMsg{X: x, Y: y, Button: button})

				vpMoved, ovMoved := sign(m.viewport.YOffset()-vpBefore), sign(m.contextOverlay.scrollOffset-ovBefore)
				dir := 1
				if button == tea.MouseWheelUp {
					dir = -1
				}
				wantVP, wantOV := dir, 0
				if tc.scrolls == scrollOverlay {
					wantVP, wantOV = 0, dir
				}
				if vpMoved != wantVP || ovMoved != wantOV {
					t.Errorf("%s: transcript moved %d, overlay moved %d (signs), want %d and %d (only the %s scrolls)",
						button, vpMoved, ovMoved, wantVP, wantOV, tc.scrolls)
				}
				// Recentre so the opposite direction still has room.
				m.viewport.SetYOffset(vpBefore)
				if m.contextOverlay.IsOpen() {
					m.contextOverlay.scrollOffset = ovBefore
				}
			}
		})
	}
}

func TestIdleIsIdlePin(t *testing.T) {
	m := auditModelSized(t, 1, 0, false)
	d := newAuditDriver(m)
	d.run(5*time.Second, timers(m), nil)
	if got := d.totalUpdates(); got > 12 {
		t.Errorf("idle model got %d Updates in 5s, want <= 12", got)
	}
	if got := d.updates(tickMsg{}); got > 1 {
		t.Errorf("idle model got %d tickMsg in 5s, want <= 1", got)
	}
}

func TestFinishedRosterIsIdlePin(t *testing.T) {
	tests := []struct {
		name      string
		finish    bool
		wantTicks func(n int) bool
	}{
		{"all sub-agents finished", true, func(n int) bool { return n <= 1 }},
		{"sub-agents running", false, func(n int) bool { return n >= 9 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := auditModelSized(t, 1, 3, true)
			if tc.finish {
				m = finishAuditAgents(m, 3)
				if m.roster.hasRunning() || m.content.HasActiveDelegations() {
					t.Fatal("agents still in flight after finishing them")
				}
			}
			d := newAuditDriver(m)
			d.run(5*time.Second, timers(m), nil)
			if got := d.updates(tickMsg{}); !tc.wantTicks(got) {
				t.Errorf("got %d tickMsg in 5s", got)
			}
		})
	}
}

func TestChunksDoNotTouchViewportPin(t *testing.T) {
	m := auditModelSized(t, 1, 0, false)
	d := newAuditDriver(m)
	d.send(runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	d.send(tickMsg{})
	if m.vpViewCache == "" {
		t.Fatal("viewport view cache not populated before the chunk")
	}
	cache := m.vpViewCache
	content := strings.Join(m.viewport.Lines(), "\n")

	_, _ = m.Update(chunk(1, ""))

	if m.vpViewCache != cache {
		t.Error("assistant chunk invalidated vpViewCache")
	}
	if got := strings.Join(m.viewport.Lines(), "\n"); got != content {
		t.Error("assistant chunk changed viewport content before the debounce/tick sync")
	}

	d.send(tickMsg{})
	if got := strings.Join(m.viewport.Lines(), "\n"); got == content {
		t.Error("tick did not sync the chunk into the viewport")
	}
}

func TestButtonlessMotionIsFreePin(t *testing.T) {
	const n = 1000
	tests := []struct {
		name  string
		model func(t *testing.T) *Model
		// onRoster moves the pointer onto the first roster row after parking it off the roster.
		onRoster      bool
		wantHoverMsgs int
	}{
		{name: "no roster", model: func(t *testing.T) *Model { return auditModelSized(t, 1, 0, false) }},
		{name: "overlay open over roster", model: func(t *testing.T) *Model {
			m := auditModelSized(t, 1, 3, true)
			m.accentPicker = m.accentPicker.Open(m.accentPreset)
			if !m.anyOverlayOpen() {
				t.Fatal("overlay did not open")
			}
			return m
		}},
		{name: "roster, pointer off roster", model: func(t *testing.T) *Model { return auditModelSized(t, 1, 3, true) }},
		{name: "roster, pointer onto a row once", model: func(t *testing.T) *Model { return auditModelSized(t, 1, 3, true) }, onRoster: true, wantHoverMsgs: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model(t)
			d := newAuditDriver(m)
			y := 0
			if tc.onRoster {
				for y = 0; y < m.height && m.rosterAgentAt(sidebarPadH+1, y) == ""; y++ {
				}
				if y == m.height {
					t.Fatal("no roster row found")
				}
			}
			x := 150
			d.send(tea.MouseMotionMsg{X: x, Y: y})
			d.resetStats()
			if tc.onRoster {
				x = sidebarPadH + 1
			}
			for range n {
				d.send(tea.MouseMotionMsg{X: x, Y: y})
			}
			if got := d.updates(sidebarHoverMsg{}); got != tc.wantHoverMsgs {
				t.Errorf("sidebarHoverMsg Updates = %d, want %d", got, tc.wantHoverMsgs)
			}
			if got := d.totalUpdates(); got != tc.wantHoverMsgs {
				t.Errorf("Updates = %d, want %d", got, tc.wantHoverMsgs)
			}
			if got := d.dropped["tea.MouseMotionMsg"]; got != n-tc.wantHoverMsgs {
				t.Errorf("filter dropped %d motion events, want %d", got, n-tc.wantHoverMsgs)
			}
			if tc.onRoster && m.sidebar.rosterHover == "" {
				t.Error("roster hover not set after the pointer moved onto a row")
			}
		})
	}
}
