package tui

// Frame audit harness. It drives a Model the way bubbletea's event loop does
// (filter -> Update -> View, plus the OnMouse follow-up message) and counts
// Update calls and View cost per message type.
//
// The audit scenarios are measurement tools, not regression tests: they run
// only when STEINER_FRAME_AUDIT=1 and print their tables through t.Logf
// (go test ./internal/tui -run TestFrameAudit -v). The sanity assertions guard
// the harness itself so a broken scenario cannot report empty numbers.
//
// Time is simulated: periodic ticks are delivered by the driver at the
// cadence the production tea.Tick chains would use, gated on the same model
// flags the handlers use to re-arm themselves (m.ticking, m.composerBlinking,
// m.sessionStartedAt).

import (
	"fmt"
	"os"
	"sort"
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

type auditStat struct {
	updates   int
	updateDur time.Duration
	viewDur   time.Duration
	unchanged int // frames whose View content equals the previous frame
}

type auditDriver struct {
	m        *Model
	stats    map[string]*auditStat
	dropped  map[string]int
	filterNs time.Duration
	lastView string
	// useFilter routes messages through filterMouseMotion like the program does.
	useFilter bool
}

func newAuditDriver(m *Model) *auditDriver {
	d := &auditDriver{m: m, stats: map[string]*auditStat{}, dropped: map[string]int{}, useFilter: true}
	d.lastView = m.View().Content
	return d
}

func msgName(msg tea.Msg) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", msg), "*")
}

// send mimics Program.eventLoop for one message: filter, OnMouse follow-up,
// Update, then View.
func (d *auditDriver) send(msg tea.Msg) {
	name := msgName(msg)
	if d.useFilter {
		t0 := time.Now()
		out := filterMouseMotion(d.m, msg)
		d.filterNs += time.Since(t0)
		if out == nil {
			d.dropped[name]++
			return
		}
		msg = out
		name = msgName(msg)
	}
	var follow tea.Cmd
	if mm, ok := msg.(tea.MouseMsg); ok {
		switch mm.(type) {
		case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
			follow = classifyMouse(mm)
		}
	}
	d.step(name, msg)
	if follow != nil {
		d.send(follow())
	}
}

func (d *auditDriver) step(name string, msg tea.Msg) {
	st := d.stats[name]
	if st == nil {
		st = &auditStat{}
		d.stats[name] = st
	}
	t0 := time.Now()
	_, _ = d.m.Update(msg)
	t1 := time.Now()
	v := d.m.View()
	t2 := time.Now()
	st.updates++
	st.updateDur += t1.Sub(t0)
	st.viewDur += t2.Sub(t1)
	if v.Content == d.lastView {
		st.unchanged++
	}
	d.lastView = v.Content
}

func (d *auditDriver) report(t *testing.T, title string, simSeconds float64) {
	t.Helper()
	names := make([]string, 0, len(d.stats))
	for n := range d.stats {
		names = append(names, n)
	}
	sort.Strings(names)
	var totalN int
	var totalDur time.Duration
	var b strings.Builder
	fmt.Fprintf(&b, "\n== %s (simulated %.1fs) ==\n", title, simSeconds)
	fmt.Fprintf(&b, "%-40s %7s %8s %10s %10s %10s %8s\n", "msg", "count", "per_sec", "upd_us", "view_us", "tot_ms/s", "same_frm")
	for _, n := range names {
		s := d.stats[n]
		tot := s.updateDur + s.viewDur
		totalN += s.updates
		totalDur += tot
		fmt.Fprintf(&b, "%-40s %7d %8.1f %10.1f %10.1f %10.2f %7d%%\n", n, s.updates, float64(s.updates)/simSeconds,
			float64(s.updateDur.Microseconds())/float64(s.updates),
			float64(s.viewDur.Microseconds())/float64(s.updates),
			float64(tot.Microseconds())/1000/simSeconds,
			100*s.unchanged/s.updates)
	}
	for n, c := range d.dropped {
		fmt.Fprintf(&b, "%-40s dropped by filter: %d (%.1f/s)\n", n, c, float64(c)/simSeconds)
	}
	if d.filterNs > 0 {
		fmt.Fprintf(&b, "filter total %.2f ms\n", float64(d.filterNs.Microseconds())/1000)
	}
	fmt.Fprintf(&b, "TOTAL update+view: %d msgs (%.1f/s), %.1f ms CPU per simulated second\n", totalN, float64(totalN)/simSeconds, float64(totalDur.Microseconds())/1000/simSeconds)
	t.Log(b.String())
	if totalN == 0 && len(d.dropped) == 0 {
		t.Fatalf("%s: harness delivered no messages", title)
	}
}

// auditModel builds a 220x60 model with the heavy transcript, optionally with
// running sub-agents in the roster and an active session timer.
func auditModel(t *testing.T, subAgents int, session bool) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	for range 4 {
		populateBenchModelHeavy(m)
	}
	m.Init()
	for i := 0; i < subAgents; i++ {
		id := fmt.Sprintf("audit-agent-%d", i)
		occ := agentOcc(id)
		m.content.AppendEvent(output.NewToolCallQueuedEvent(100+i, "sub_agent", occ.CallID, subAgentArgs("")))
		m.content.AppendEvent(output.NewToolCallStartedEvent(100+i, "sub_agent", occ.CallID, subAgentArgs("")))
		m.content.AppendEvent(output.NewDelegationAcceptedEvent(occ, ""))
		m.content.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "audit task", "", "review"), id))
		e := m.roster.upsert(id)
		e.agentType = "review"
		e.occurrence = occurrenceKey{BatchID: occ.BatchID, CallID: occ.CallID}
		e.admitted = true
		e.status = rosterRunning
	}
	if session {
		now := time.Now()
		m.sessionStartedAt = &now
	}
	m.syncRoster()
	m.syncSidebar()
	m.syncViewport()
	if subAgents > 0 && len(m.sidebar.subAgents) == 0 {
		t.Fatal("audit model has no roster entries")
	}
	return m
}

// periodic describes one self-rearming timer chain.
type periodic struct {
	every time.Duration
	next  time.Duration
	// armed reports whether the production handler would still re-arm it.
	armed func(*Model) bool
	msg   func() tea.Msg
}

func timers(_ *Model) []*periodic {
	return []*periodic{
		{every: 500 * time.Millisecond, next: 500 * time.Millisecond, armed: func(m *Model) bool { return m.ticking }, msg: func() tea.Msg { return tickMsg{} }},
		{every: 500 * time.Millisecond, next: 500 * time.Millisecond, armed: func(m *Model) bool { return m.composerBlinking }, msg: func() tea.Msg { return composerBlinkMsg{} }},
		{every: sessionTickInterval, next: sessionTickInterval, armed: func(m *Model) bool { return m.sessionStartedAt != nil }, msg: func() tea.Msg { return sessionTickMsg{} }},
	}
}

// run advances simulated time in 1ms steps, firing timers, the 50ms
// syncDebounce chain, and the supplied per-ms emitter (argument is ms).
func (d *auditDriver) run(dur time.Duration, ts []*periodic, emit func(ms int)) {
	lastSeq := d.m.syncDebounceSeq
	debounceAt := -1
	for now := time.Duration(0); now < dur; now += time.Millisecond {
		ms := int(now / time.Millisecond)
		if emit != nil {
			emit(ms)
		}
		for _, p := range ts {
			if now >= p.next {
				p.next += p.every
				if p.armed(d.m) {
					d.send(p.msg())
				}
			}
		}
		if d.m.syncDebounceSeq != lastSeq {
			lastSeq = d.m.syncDebounceSeq
			debounceAt = ms + 50
		}
		if debounceAt >= 0 && ms >= debounceAt {
			debounceAt = -1
			d.send(syncDebounceFiredMsg{seq: d.m.syncDebounceSeq})
		}
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
			// All roster entries done, no active delegations: the realistic
			// "agents finished, user reading" state.
			for _, e := range m.roster.entries {
				e.status = rosterDone
				e.finishTime = 5
			}
			m.syncRoster()
		}
		d := newAuditDriver(m)
		d.run(5*time.Second, timers(m), nil)
		d.report(t, tc.name, 5)
	}
}

func chunk(turn int, scopeID string) tea.Msg {
	ev := output.NewAssistantChunkEventWithSource(turn, "streaming token text that looks like a short markdown sentence. ", output.ChunkSourceAssistant)
	if scopeID != "" {
		ev = output.WithAgentScope(ev, scopeID)
	}
	return runtimeEventMsg{Event: ev}
}

func TestFrameAuditStreaming(t *testing.T) {
	requireFrameAudit(t)
	m := auditModel(t, 3, true)
	d := newAuditDriver(m)
	d.send(runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	d.stats = map[string]*auditStat{} // exclude setup
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
		d2.stats = map[string]*auditStat{}
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
		d.report(t, tc.name, 4)
	}
}
