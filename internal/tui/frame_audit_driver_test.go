package tui

// Frame audit driver. It drives a Model the way bubbletea's event loop does
// (filter -> Update -> View, plus the OnMouse follow-up message) and counts
// Update calls and View cost per message type. It is shared by the env-gated
// audit scenarios (frame_audit_test.go) and the deterministic guard tests
// (perf_pins_test.go).
//
// Commands returned by Update are NOT executed: tea.Tick commands block for
// real wall-clock time and would make runs slow and non-deterministic.
// Messages the program would receive from commands are instead injected
// explicitly: the OnMouse follow-up (taken from the last rendered
// View().OnMouse, as the program does) and the periodic chains, which run()
// fires at the production cadence, gated on the same model flags the handlers
// use to re-arm themselves (m.ticking, m.composerBlinking,
// m.sessionStartedAt) plus the 50ms syncDebounce chain.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

type auditStat struct {
	updates   int
	updateDur time.Duration
	viewDur   time.Duration
	maxStall  time.Duration // longest single Update+View
	unchanged int           // frames whose View content equals the previous frame
}

type auditDriver struct {
	m *Model
	// stats is keyed by the message type that reached Update.
	stats map[string]*auditStat
	// offered counts messages entering send, keyed by their raw type (before
	// the filter), including OnMouse follow-ups.
	offered map[string]int
	dropped map[string]int
	// keyWait holds the worst measured key-wait per heavy message type.
	keyWait  map[string]time.Duration
	filterNs time.Duration
	lastView string
	onMouse  func(tea.MouseMsg) tea.Cmd
	// useFilter routes messages through filterMouseMotion like the program does.
	useFilter bool
}

func newAuditDriver(m *Model) *auditDriver {
	d := &auditDriver{
		m:         m,
		stats:     map[string]*auditStat{},
		offered:   map[string]int{},
		dropped:   map[string]int{},
		keyWait:   map[string]time.Duration{},
		useFilter: true,
	}
	v := m.View()
	d.lastView = v.Content
	d.onMouse = v.OnMouse
	return d
}

func msgName(msg tea.Msg) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", msg), "*")
}

// send mimics Program.eventLoop for one message: filter, Update, View, then
// the OnMouse follow-up when the rendered view registered one.
func (d *auditDriver) send(msg tea.Msg) {
	d.offered[msgName(msg)]++
	if d.useFilter {
		t0 := time.Now()
		out := filterMouseMotion(d.m, msg)
		d.filterNs += time.Since(t0)
		if out == nil {
			d.dropped[msgName(msg)]++
			return
		}
		msg = out
	}
	var follow tea.Cmd
	if mm, ok := msg.(tea.MouseMsg); ok && d.onMouse != nil {
		follow = d.onMouse(mm)
	}
	d.step(msgName(msg), msg)
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
	st.maxStall = max(st.maxStall, t2.Sub(t0))
	if v.Content == d.lastView {
		st.unchanged++
	}
	d.lastView = v.Content
	d.onMouse = v.OnMouse
}

// updates returns the number of Update calls that received a message of the
// same type as sample.
func (d *auditDriver) updates(sample tea.Msg) int {
	if st := d.stats[msgName(sample)]; st != nil {
		return st.updates
	}
	return 0
}

func (d *auditDriver) totalUpdates() int {
	n := 0
	for _, st := range d.stats {
		n += st.updates
	}
	return n
}

func (d *auditDriver) resetStats() {
	d.stats = map[string]*auditStat{}
	d.offered = map[string]int{}
	d.dropped = map[string]int{}
	d.keyWait = map[string]time.Duration{}
	d.filterNs = 0
}

// probeKeyWait measures how long a key press queued directly behind heavy
// waits: from before heavy's Update starts to the end of the key's own
// Update+View (heavy's OnMouse follow-up, if any, runs in between). The pre
// messages are delivered first to put the model in the state that makes heavy
// expensive. The probe does not count toward the scenario's stats; the result
// is recorded under heavy's type, keeping the worst of repeated probes.
func (d *auditDriver) probeKeyWait(heavy tea.Msg, pre ...tea.Msg) {
	stats, offered, dropped, filterNs := d.stats, d.offered, d.dropped, d.filterNs
	d.stats = map[string]*auditStat{}
	d.offered = map[string]int{}
	d.dropped = map[string]int{}
	for _, p := range pre {
		d.send(p)
	}
	t0 := time.Now()
	d.send(heavy)
	d.send(tea.KeyPressMsg{Code: 'a', Text: "a"})
	wait := time.Since(t0)
	d.stats, d.offered, d.dropped, d.filterNs = stats, offered, dropped, filterNs
	name := msgName(heavy)
	d.keyWait[name] = max(d.keyWait[name], wait)
}

func durMs(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// report logs one aligned table: per message type, offered count, Update
// calls, CPU per simulated second, worst single Update+View and, for heavy
// messages probed with probeKeyWait, the key-wait.
func (d *auditDriver) report(t *testing.T, title string, simSeconds float64) {
	t.Helper()
	seen := map[string]bool{}
	var names []string
	for _, set := range []func(func(string)){
		func(f func(string)) {
			for n := range d.stats {
				f(n)
			}
		},
		func(f func(string)) {
			for n := range d.offered {
				f(n)
			}
		},
		func(f func(string)) {
			for n := range d.keyWait {
				f(n)
			}
		},
	} {
		set(func(n string) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		})
	}
	sort.Strings(names)
	var totalN int
	var totalDur, worstStall, worstWait time.Duration
	var b strings.Builder
	fmt.Fprintf(&b, "\n== %s (simulated %.2fs) ==\n", title, simSeconds)
	fmt.Fprintf(&b, "%-28s %7s %8s %8s %16s %13s %12s\n", "msg type", "n", "updates", "dropped", "CPU ms/sim-sec", "max stall ms", "key-wait ms")
	for _, n := range names {
		s := d.stats[n]
		if s == nil {
			s = &auditStat{}
		}
		tot := s.updateDur + s.viewDur
		totalN += s.updates
		totalDur += tot
		worstStall = max(worstStall, s.maxStall)
		kw := "-"
		if w, ok := d.keyWait[n]; ok {
			kw = fmt.Sprintf("%.2f", durMs(w))
			worstWait = max(worstWait, w)
		}
		fmt.Fprintf(&b, "%-28s %7d %8d %8d %16.2f %13.2f %12s\n", n, d.offered[n], s.updates, d.dropped[n], durMs(tot)/simSeconds, durMs(s.maxStall), kw)
	}
	fmt.Fprintf(&b, "%-28s %7s %8d %8s %16.2f %13.2f %12.2f\n", "TOTAL", "", totalN, "", durMs(totalDur)/simSeconds, durMs(worstStall), durMs(worstWait))
	if d.filterNs > 0 {
		fmt.Fprintf(&b, "filter total %.2f ms\n", durMs(d.filterNs))
	}
	t.Log(b.String())
	if totalN == 0 && len(d.dropped) == 0 {
		t.Fatalf("%s: harness delivered no messages", title)
	}
}

// auditModel builds a 220x60 model with the 4x heavy transcript, optionally with
// running sub-agents in the roster and an active session timer.
func auditModel(t *testing.T, subAgents int, session bool) *Model {
	t.Helper()
	return auditModelSized(t, 4, subAgents, session)
}

// auditModelSized is auditModel with a configurable number of heavy
// transcript repetitions, for guard tests that need the shape but not the
// audit-scale cost.
func auditModelSized(t *testing.T, heavy, subAgents int, session bool) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	for range heavy {
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

func chunk(turn int, scopeID string) tea.Msg {
	ev := output.NewAssistantChunkEventWithSource(turn, "streaming token text that looks like a short markdown sentence. ", output.ChunkSourceAssistant)
	if scopeID != "" {
		ev = output.WithAgentScope(ev, scopeID)
	}
	return runtimeEventMsg{Event: ev}
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
