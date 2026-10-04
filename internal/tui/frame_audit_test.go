package tui

// Frame audit scenarios. They are measurement tools, not regression tests: they
// run only when STEINER_FRAME_AUDIT=1 and print their tables through t.Log
// (go test ./internal/tui -run TestFrameAudit -v). The driver they share with
// the deterministic guard tests lives in frame_audit_driver_test.go.

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

func requireFrameAudit(t *testing.T) {
	t.Helper()
	if os.Getenv("STEINER_FRAME_AUDIT") != "1" {
		t.Skip("set STEINER_FRAME_AUDIT=1 to run the frame audit")
	}
}

func TestFrameAuditIdle(t *testing.T) {
	requireFrameAudit(t)
	for _, tc := range []struct {
		name    string
		agents  int
		session bool
	}{
		{"idle, no session started", 0, false},
		{"idle, session started (timer armed)", 0, true},
		{"idle, session started, 3 finished-in-roster sub-agents running flag cleared", 3, true},
	} {
		m := auditModel(t, tc.agents, tc.session)
		if strings.Contains(tc.name, "cleared") {
			// The realistic "agents finished, user reading" state. Flipping
			// roster status alone leaves the content buffer's delegations in
			// flight, which legitimately keeps the tick chain alive.
			m = finishAuditAgents(m, tc.agents)
		}
		d := newAuditDriver(m)
		d.run(5*time.Second, timers(m), nil)
		d.probeKeyWait(tickMsg{}, chunk(1, ""))
		d.report(t, tc.name, 5)
	}
}

func TestFrameAuditStreaming(t *testing.T) {
	requireFrameAudit(t)
	m := auditModel(t, 3, true)
	d := newAuditDriver(m)
	d.send(runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	d.resetStats() // exclude setup
	ids := []string{"audit-agent-0", "audit-agent-1", "audit-agent-2"}
	// main: 40 chunks/s; each sub-agent: 20 chunks/s; one sub-agent tool call per 500ms.
	d.run(5*time.Second, timers(m), func(now int) {
		if now%25 == 0 {
			d.send(chunk(1, ""))
		}
		for i, id := range ids {
			if now%50 == i {
				d.send(chunk(2, id))
			}
			if now%500 == 100+i {
				callID := fmt.Sprintf("tc-%d-%d", i, now)
				d.send(runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallStartedEvent(2, "read", callID, map[string]any{"file": "x.go"}), id)})
				d.send(runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallFinishedEvent(2, "read", callID, strings.Repeat("r", 600), nil), id)})
			}
		}
	})
	d.probeKeyWait(tickMsg{}, chunk(1, ""))
	d.probeKeyWait(syncDebounceFiredMsg{seq: m.syncDebounceSeq}, chunk(1, ""))
	d.report(t, "streaming main + 3 sub-agents", 5)
}

// overlayModel opens the accent picker over the audit model.
func overlayModel(t *testing.T) *Model {
	m := auditModel(t, 3, true)
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	if !m.anyOverlayOpen() {
		t.Fatal("overlay did not open")
	}
	return m
}

func motionSweep(d *auditDriver, n int, button tea.MouseButton) {
	for i := 0; i < n; i++ {
		d.send(tea.MouseMotionMsg{X: (i * 7) % 220, Y: (i * 3) % 60, Button: button})
	}
}

func TestFrameAuditMouseMotion(t *testing.T) {
	requireFrameAudit(t)
	const n = 2000
	for _, tc := range []struct {
		name string
		mk   func() *Model
	}{
		{"overlay open, all-motion mode on", func() *Model { return overlayModel(t) }},
		{"no overlay, roster visible", func() *Model { return auditModel(t, 3, true) }},
	} {
		m := tc.mk()
		t.Logf("%s: View().MouseMode = %v", tc.name, m.View().MouseMode)
		d := newAuditDriver(m)
		motionSweep(d, n, tea.MouseNone)
		d.report(t, tc.name+": buttonless motion sweep", float64(n)/100) // pretend 100 events/s
		// Drag (left button held): passes the filter, raw + classified.
		d2 := newAuditDriver(tc.mk())
		d2.send(tea.MouseClickMsg{X: 100, Y: 20, Button: tea.MouseLeft})
		d2.resetStats()
		motionSweep(d2, 200, tea.MouseLeft)
		d2.report(t, tc.name+": left-drag motion", 2)
	}
	// Hover target changes: sweep across roster rows.
	m := auditModel(t, 3, true)
	d := newAuditDriver(m)
	row := rosterScreenRow(m, 0)
	for i := 0; i < 400; i++ {
		y := row + i%3
		if i%2 == 1 {
			d.send(tea.MouseMotionMsg{X: 150, Y: y}) // leave sidebar
		} else {
			d.send(tea.MouseMotionMsg{X: sidebarPadH + 1, Y: y})
		}
	}
	d.report(t, "hover changes (no overlay)", 4)
}

func TestFrameAuditWheel(t *testing.T) {
	requireFrameAudit(t)
	for _, tc := range []struct {
		name string
		mk   func() *Model
	}{
		{"wheel, no overlay", func() *Model { return auditModel(t, 3, true) }},
		{"wheel, overlay open", func() *Model { return overlayModel(t) }},
	} {
		m := tc.mk()
		m.scrollUp(30)
		d := newAuditDriver(m)
		for i := 0; i < 400; i++ {
			btn := tea.MouseWheelUp
			if i%2 == 1 {
				btn = tea.MouseWheelDown
			}
			d.send(tea.MouseWheelMsg{X: 60, Y: 20, Button: btn})
		}
		d.probeKeyWait(tea.MouseWheelMsg{X: 60, Y: 20, Button: tea.MouseWheelUp})
		d.report(t, tc.name, 4)
	}
}

func TestFrameAuditSidebarToggle(t *testing.T) {
	requireFrameAudit(t)
	m := auditModel(t, 3, true)
	if !m.sidebar.Visible(m.width) {
		t.Fatal("sidebar not visible at audit width")
	}
	d := newAuditDriver(m)
	toggle := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	d.run(3*time.Second, timers(m), func(now int) {
		if now%500 == 0 {
			d.send(toggle)
		}
	})
	d.probeKeyWait(toggle)
	d.report(t, "sidebar_toggle: 6 toggles, 500ms apart", 3)
	if got := d.updates(toggle); got != 6 {
		t.Fatalf("toggle key Updates = %d, want 6", got)
	}
}

func TestFrameAuditOverlayWheel(t *testing.T) {
	requireFrameAudit(t)
	m := auditModel(t, 3, true)
	m.scrollUp(30)
	m.contextOverlay = openContextOverlay("Context", benchContextReport(), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	bx, by, bw, bh := m.contextOverlayBounds()
	inside := tea.MouseWheelMsg{X: bx + bw/2, Y: by + bh/2}
	outside := tea.MouseWheelMsg{X: 1, Y: 1}
	d := newAuditDriver(m)
	for i := 0; i < 400; i++ {
		msg := inside
		if i%2 == 1 {
			msg = outside
		}
		msg.Button = tea.MouseWheelUp
		if i%4 >= 2 {
			msg.Button = tea.MouseWheelDown
		}
		d.send(msg)
	}
	outside.Button = tea.MouseWheelUp
	d.probeKeyWait(outside)
	d.report(t, "overlay_wheel: context overlay, wheel inside/outside bounds", 4)
}

func TestFrameAuditResizeBurst(t *testing.T) {
	requireFrameAudit(t)
	m := auditModel(t, 3, true)
	d := newAuditDriver(m)
	widths := []int{200, 190, 180, 170, 160}
	d.run(300*time.Millisecond, timers(m), func(now int) {
		if now%30 == 0 && now/30 < len(widths) {
			d.send(tea.WindowSizeMsg{Width: widths[now/30], Height: 60})
		}
	})
	d.probeKeyWait(tea.WindowSizeMsg{Width: 150, Height: 60})
	d.report(t, "resize_burst: 5 width changes, 30ms apart", 0.3)
	if got := d.updates(tea.WindowSizeMsg{}); got != 5 {
		t.Fatalf("WindowSizeMsg Updates = %d, want 5", got)
	}
}
