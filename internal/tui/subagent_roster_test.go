package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func ev(payload any) output.Event { return output.Event{Payload: payload} }

func statusesByID(r *subAgentRoster) map[string]string {
	out := map[string]string{}
	for _, e := range r.snapshot() {
		out[e.agentID] = e.status
	}
	return out
}

func TestRosterTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		events []output.Event
		want   map[string]string
		group  map[string]string
	}{
		{
			name: "queued then started then complete",
			events: []output.Event{
				ev(output.DelegationQueuedEvent{AgentID: "c1", AgentType: "explore"}),
				ev(output.DelegationStartedEvent{AgentID: "c1", AgentType: "explore"}),
				ev(output.DelegationCompleteEvent{AgentID: "c1", AgentType: "explore", Status: "completed", DurationMs: 5000}),
			},
			want: map[string]string{"c1": rosterDone},
		},
		{
			name: "failed and running mix",
			events: []output.Event{
				ev(output.DelegationStartedEvent{AgentID: "c1", AgentType: "code"}),
				ev(output.DelegationStartedEvent{AgentID: "c2", AgentType: "review"}),
				ev(output.DelegationFailedEvent{AgentID: "c1", AgentType: "code", Error: "boom"}),
			},
			want: map[string]string{"c1": rosterFailed, "c2": rosterRunning},
		},
		{
			name: "group from tool call args",
			events: []output.Event{
				ev(output.ToolCallQueuedEvent{CallID: "call-1", Tool: "delegate", Arguments: map[string]any{"group": " final-review "}}),
				ev(output.ToolCallStartedEvent{CallID: "call-2", Tool: "delegate", Arguments: map[string]any{}}),
				ev(output.DelegationStartedEvent{AgentID: "c1", AgentType: "review", CallID: "call-1"}),
				ev(output.DelegationStartedEvent{AgentID: "c2", AgentType: "review", CallID: "call-2"}),
			},
			want:  map[string]string{"c1": rosterRunning, "c2": rosterRunning},
			group: map[string]string{"c1": "final-review", "c2": ""},
		},
		{
			name: "advisor excluded",
			events: []output.Event{
				ev(output.DelegationStartedEvent{AgentID: "adv", AgentType: "advisor"}),
				ev(output.DelegationCompleteEvent{AgentID: "adv", AgentType: "advisor", Status: "completed"}),
			},
			want: map[string]string{},
		},
		{
			name: "delivered marks lost",
			events: []output.Event{
				ev(output.DelegationStartedEvent{AgentID: "c1", AgentType: "explore"}),
				ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "c1", AgentType: "explore", Status: "lost"}}}),
			},
			want: map[string]string{"c1": rosterLost},
		},
		{
			name: "delivered creates unknown entry as done",
			events: []output.Event{
				ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "c9", AgentType: "code", Status: "completed", DurationMs: 1000}}}),
			},
			want: map[string]string{"c9": rosterDone},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var r subAgentRoster
			for i, e := range tc.events {
				r.observe(e, int64(i+1)*1_000_000_000)
			}
			got := statusesByID(&r)
			if len(got) != len(tc.want) {
				t.Fatalf("statuses = %v, want %v", got, tc.want)
			}
			for id, st := range tc.want {
				if got[id] != st {
					t.Errorf("%s status = %q, want %q", id, got[id], st)
				}
			}
			for _, e := range r.snapshot() {
				if want, ok := tc.group[e.agentID]; ok && e.group != want {
					t.Errorf("%s group = %q, want %q", e.agentID, e.group, want)
				}
			}
		})
	}
}

func TestRosterPrune(t *testing.T) {
	t.Parallel()
	seed := func() *subAgentRoster {
		r := &subAgentRoster{}
		r.observe(ev(output.DelegationStartedEvent{AgentID: "run", AgentType: "explore"}), 1)
		r.observe(ev(output.DelegationQueuedEvent{AgentID: "q", AgentType: "explore"}), 2)
		r.observe(ev(output.DelegationFailedEvent{AgentID: "bad", AgentType: "code"}), 3)
		r.observe(ev(output.DelegationCompleteEvent{AgentID: "ok", AgentType: "code", Status: "completed"}), 4)
		return r
	}
	for name, fn := range map[string]func(*subAgentRoster){
		"prompt":         func(r *subAgentRoster) { r.prune() },
		"session_loaded": func(r *subAgentRoster) { r.observe(ev(output.ContextDiagnosticsEvent{Kind: "session_loaded"}), 9) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := seed()
			fn(r)
			got := statusesByID(r)
			if len(got) != 2 || got["run"] != rosterRunning || got["q"] != rosterQueued {
				t.Fatalf("after prune = %v, want only run and q", got)
			}
		})
	}
}

func TestRosterSnapshotDeterministicOrder(t *testing.T) {
	t.Parallel()
	var r subAgentRoster
	for _, id := range []string{"c5", "c1", "c3", "c2", "c4"} {
		// identical start times force the seq tie-breaker.
		r.observe(ev(output.DelegationStartedEvent{AgentID: id, AgentType: "explore"}), 100)
	}
	for range 20 {
		var ids []string
		for _, e := range r.snapshot() {
			ids = append(ids, e.agentID)
		}
		if strings.Join(ids, ",") != "c5,c1,c3,c2,c4" {
			t.Fatalf("order = %v", ids)
		}
	}
}

func rosterSidebar(entries []rosterEntry, now int64) sidebarState {
	return sidebarState{styles: testStyles(theme.AccentAmber), subAgents: entries, subAgentsNow: now}
}

func TestSubAgentsSectionRender(t *testing.T) {
	t.Parallel()
	sec := func(s sidebarState) string { return stripANSI(strings.Join(s.subAgentsSection(40), "\n")) }
	const sec1 = int64(1_000_000_000)
	if got := rosterSidebar(nil, 0).subAgentsSection(40); got != nil {
		t.Fatalf("empty roster rendered %v", got)
	}

	t.Run("running done and grouped", func(t *testing.T) {
		t.Parallel()
		out := sec(rosterSidebar([]rosterEntry{
			{agentID: "child-5", agentType: "review", group: "final-review", status: rosterRunning, startTime: 0},
			{agentID: "child-4", agentType: "sanity_check", group: "final-review", status: rosterDone, startTime: 0, finishTime: 252 * sec1},
			{agentID: "child-6", agentType: "explore", status: rosterRunning, startTime: 0},
			{agentID: "child-3", agentType: "code", status: rosterFailed, startTime: 0, finishTime: 62 * sec1},
		}, 130*sec1))
		for _, want := range []string{"SUB-AGENTS · 2 RUNNING", "┌ final-review", "│", "child-5", "2m10s", "4m12s", "1m02s", "✓", "✗"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
	})

	t.Run("collapses finished past limit", func(t *testing.T) {
		t.Parallel()
		var entries []rosterEntry
		for i := range 12 {
			entries = append(entries, rosterEntry{agentID: "c" + string(rune('a'+i)), agentType: "code", status: rosterDone, startTime: int64(i), finishTime: sec1})
		}
		out := sec(rosterSidebar(entries, 0))
		if !strings.Contains(out, "+4 finished") {
			t.Errorf("want +4 finished in:\n%s", out)
		}
		if strings.Contains(out, "cl") {
			t.Errorf("last finished entry should be collapsed:\n%s", out)
		}
	})
}

func TestStatusSubAgentsChip(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	tests := []struct {
		name    string
		s       statusState
		want    []string
		notWant []string
	}{
		{"absent when empty", statusState{styles: styles}, nil, []string{"sub-agents"}},
		{"running", statusState{styles: styles, subAgentsTotal: 3, subAgentsFinished: 1}, []string{"sub-agents", "1/3"}, []string{"queued", "✗", "✓"}},
		{"queued", statusState{styles: styles, subAgentsTotal: 3, subAgentsQueued: 2}, []string{"0/3 · 2 queued"}, nil},
		{"failure while running", statusState{styles: styles, subAgentsTotal: 3, subAgentsFinished: 2, subAgentsFailed: 1}, []string{"✗ 2/3"}, nil},
		{"all done", statusState{styles: styles, subAgentsTotal: 2, subAgentsFinished: 2}, []string{"✓", "2/2"}, []string{"✗"}},
		{"all done with failure", statusState{styles: styles, subAgentsTotal: 2, subAgentsFinished: 2, subAgentsFailed: 1}, []string{"✗", "2/2"}, []string{"✓"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := stripANSI(tc.s.view(200))
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in %q", w, out)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q in %q", w, out)
				}
			}
		})
	}
}

func TestStatusChipTruncationPriority(t *testing.T) {
	t.Parallel()
	s := statusState{
		model: "gpt", styles: testStyles(theme.AccentAmber), sandboxStatus: "active",
		subAgentsTotal: 3, subAgentsFinished: 1, promptUsed: 100, contextBudget: 1000,
	}
	full := lipgloss.Width(s.view(0))
	sawChipWithoutTrailing := false
	for w := full; w > 10; w-- {
		out := stripANSI(s.view(w))
		hasChip := strings.Contains(out, "sub-agents")
		if !hasChip {
			// once the chip is gone, nothing after it may remain.
			if strings.Contains(out, "sidebar") || strings.Contains(out, "help") || strings.Contains(out, "ctx") {
				t.Fatalf("width %d dropped chip but kept later parts: %q", w, out)
			}
			continue
		}
		if !strings.Contains(out, "sbx") {
			t.Fatalf("width %d kept chip but lost sandbox badge: %q", w, out)
		}
		if !strings.Contains(out, "ctx") && !strings.Contains(out, "help") && !strings.Contains(out, "sidebar") {
			sawChipWithoutTrailing = true
		}
	}
	if !sawChipWithoutTrailing {
		t.Error("chip never survived after ctx, help and sidebar were dropped")
	}
}

func TestSyncRosterFrameAdvancesOnlyWhileRunning(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.sidebar.styles = testStyles(theme.AccentAmber)
	m.roster.observe(ev(output.DelegationStartedEvent{AgentID: "c1", AgentType: "explore"}), 1)
	m.sidebar.tickCount = 3
	m.syncRoster()
	if m.status.spinnerFrame != 3 {
		t.Fatalf("frame = %d, want 3 while running", m.status.spinnerFrame)
	}
	m.roster.observe(ev(output.DelegationCompleteEvent{AgentID: "c1", Status: "completed"}), 2)
	m.sidebar.tickCount = 5
	m.syncRoster()
	if m.status.spinnerFrame != 0 {
		t.Fatalf("frame = %d, want 0 when nothing running", m.status.spinnerFrame)
	}
	if m.roster.hasRunning() {
		t.Fatal("hasRunning should be false after completion")
	}
}
