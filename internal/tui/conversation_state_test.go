package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func applyConversationStateEvent(t *testing.T, m *Model, state string, held bool, pending int, budget bool) {
	t.Helper()
	m.applyEvent(output.NewConversationStateEvent(state, held, pending, budget))
}

func TestConversationStateWaitingLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		held    bool
		budget  bool
		pending int
	}{
		{name: "waiting", pending: 3},
		{name: "held", held: true, pending: 4},
		{name: "budget exhausted", budget: true, pending: 5},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		m := newModel(Config{}, nil)
		applyConversationStateEvent(t, m, "waiting", tt.held, tt.pending, tt.budget)
		label := m.activity.label
		if label == "" || !strings.Contains(label, string(rune('0'+tt.pending))) {
			t.Errorf("%s: label %q does not carry the pending count", tt.name, label)
		}
		if seen[label] {
			t.Errorf("%s: label %q duplicates another state's label", tt.name, label)
		}
		seen[label] = true
		if tt.held || tt.budget {
			if m.activity.busy() {
				t.Errorf("%s: held/budget label must not spin", tt.name)
			}
		} else if !m.activity.busy() {
			t.Errorf("%s: ordinary waiting label must spin", tt.name)
		}
	}
}

func TestConversationWaitingSpinnerRestoresAfterRunTerminalEvents(t *testing.T) {
	t.Parallel()
	for _, event := range []output.Event{
		output.NewRunFinishedEvent(1, "complete", "", "", nil),
		output.NewStopReasonEvent(1, "cancelled", nil),
	} {
		m := newModel(Config{}, nil)
		applyConversationStateEvent(t, m, conversationStateWaiting, false, 2, false)
		m.activity = m.activity.static("stopped", "")
		m.applyEvent(event)
		if !m.activity.busy() || m.activity.label != conversationWaitingLabel(m.convState) {
			t.Errorf("after %s: activity = %+v, want restored conversation spinner", event.Type, m.activity)
		}
	}
}

func TestConversationWaitingKeepsInputChromeIdle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	applyConversationStateEvent(t, m, conversationStateWaiting, false, 1, false)
	if m.status.streaming {
		t.Fatal("status.streaming = true while only waiting on sub-agents")
	}
	if got, want := m.input.Placeholder, "ask steiner — / for commands, @ for files"; got != want {
		t.Fatalf("placeholder = %q, want %q", got, want)
	}
}

func TestConversationStateLeavingWaitingClearsLabel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	applyConversationStateEvent(t, m, "waiting", false, 2, false)
	if m.activity.label == "" {
		t.Fatal("waiting did not set a label")
	}
	applyConversationStateEvent(t, m, "idle", false, 0, false)
	if m.activity.busy() {
		t.Error("activity still spinning after idle")
	}
	if m.activity.label != "" {
		t.Errorf("label after idle = %q, want empty", m.activity.label)
	}
}

func TestChildAPIResponseDoesNotChangeParentActivity(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	applyConversationStateEvent(t, m, "waiting", false, 1, false)
	want := m.activity
	m.applyEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "stop", nil), "child-1"))
	m.applyEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "stop", nil), "child-1"))
	if m.activity.label != want.label || m.activity.detail != want.detail || m.activity.spinning != want.spinning {
		t.Errorf("activity = %+v, want unchanged %+v", m.activity, want)
	}
}

func TestQueuedDelegationOpensModalOnEsc(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.applyEvent(output.NewDelegationQueuedEvent("agent-1", "call-1", "explore", "look around"))
	if !m.content.HasActiveDelegations() {
		t.Fatal("queued delegation is not counted as active")
	}
	rows := m.content.ActiveDelegateRows()
	if len(rows) != 1 || rows[0].agentID != "agent-1" || !rows[0].queued {
		t.Fatalf("rows = %+v, want one queued agent-1 row", rows)
	}
	if !m.sessionBusy() {
		t.Error("queued delegation must make the session busy")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.delegateCancelModal.IsOpen() {
		t.Fatal("Esc did not open the delegate cancel modal for a queued segment")
	}

	m.applyEvent(output.NewDelegationStartedEventWithType("agent-1", "look around", "call-1", "", "explore"))
	rows = m.content.ActiveDelegateRows()
	if len(rows) != 1 || rows[0].queued {
		t.Fatalf("rows after start = %+v, want one non-queued row", rows)
	}
	segments := 0
	for _, seg := range m.content.segments {
		if seg.kind == segmentDelegation || seg.kind == segmentDelegationGroup {
			segments++
		}
	}
	if segments != 1 {
		t.Errorf("delegation segments = %d, want 1 (started must bind the queued segment)", segments)
	}
}

func TestDelegationStallMarking(t *testing.T) {
	saved := timeNow
	defer func() { timeNow = saved }()
	now := time.Unix(1_800_000_000, 0)
	timeNow = func() time.Time { return now }

	m := newModel(Config{}, nil)
	m.applyEvent(output.NewDelegationStartedEventWithType("agent-1", "task", "call-1", "", "explore"))
	stalled := func() int { return m.content.ActiveDelegateRows()[0].stalledMin }

	now = now.Add(delegationStallThreshold - time.Second)
	if got := stalled(); got != 0 {
		t.Errorf("stalledMin just under threshold = %d, want 0", got)
	}
	now = now.Add(time.Second)
	if got := stalled(); got != 5 {
		t.Errorf("stalledMin at threshold = %d, want 5", got)
	}
	now = now.Add(2 * time.Minute)
	if got := stalled(); got != 7 {
		t.Errorf("stalledMin = %d, want 7", got)
	}
	m.applyEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "stop", nil), "agent-1"))
	if got := stalled(); got != 0 {
		t.Errorf("stalledMin after child activity = %d, want 0", got)
	}
}

func TestParentCancelledStopFinalisesDelegationsOnlyInSyncMode(t *testing.T) {
	t.Parallel()
	cancelled := output.NewStopReasonEvent(1, "cancelled", nil)
	tests := []struct {
		name       string
		async      bool
		scoped     bool
		wantActive bool
	}{
		{name: "sync parent cancel finalises", wantActive: false},
		{name: "async parent cancel keeps delegations", async: true, wantActive: true},
		{name: "scoped cancel never finalises", scoped: true, wantActive: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			if tt.async {
				applyConversationStateEvent(t, m, "generating", false, 1, false)
			}
			m.applyEvent(output.NewDelegationStartedEventWithType("agent-1", "task", "call-1", "", "explore"))
			ev := cancelled
			if tt.scoped {
				ev = output.WithAgentScope(ev, "other-agent")
			}
			m.applyEvent(ev)
			if got := m.content.HasActiveDelegations(); got != tt.wantActive {
				t.Errorf("HasActiveDelegations = %v, want %v", got, tt.wantActive)
			}
			if tt.wantActive {
				m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "agent-1", Status: "completed"}))
				if m.content.HasActiveDelegations() {
					t.Error("the delegation's own complete event must still finish it")
				}
			}
		})
	}
}

func TestInterruptedFilterKeepsAsyncDelegateEventsFlowing(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	applyConversationStateEvent(t, m, "generating", false, 1, false)
	m.applyEvent(output.NewDelegationStartedEventWithType("agent-1", "task", "call-1", "", "explore"))
	m.interruptPending = true
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "agent-1", Status: "completed"}))
	if m.content.HasActiveDelegations() {
		t.Error("delegate completion was swallowed by the interrupt filter")
	}
	applyConversationStateEvent(t, m, "waiting", true, 1, false)
	if !m.convState.Held {
		t.Error("conversation state was swallowed by the interrupt filter")
	}
}

func TestCompactGuardUsesDriverState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state       string
		wantRefused bool
	}{
		{state: "waiting"},
		{state: "generating", wantRefused: true},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()
			ctrl := &testController{}
			m := newModel(Config{}, nil)
			m.controller = ctrl
			m.applyEvent(output.NewDelegationStartedEventWithType("agent-1", "task", "call-1", "", "explore"))
			applyConversationStateEvent(t, m, tt.state, false, 1, false)
			m.executeCompactAction(inputAction{})
			ctrl.mu.Lock()
			triggered := 0
			for _, a := range ctrl.actions {
				if _, ok := a.(interactive.TriggerManualCompaction); ok {
					triggered++
				}
			}
			ctrl.mu.Unlock()
			if refused := triggered == 0; refused != tt.wantRefused {
				t.Errorf("compact refused = %v, want %v", refused, tt.wantRefused)
			}
		})
	}
}

func TestSessionMutationsRefusedWhileResultsPending(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	applyConversationStateEvent(t, m, "waiting", true, 2, false)
	if !m.sessionBusy() {
		t.Error("sessionBusy = false while results are pending")
	}
	applyConversationStateEvent(t, m, "idle", false, 0, false)
	if m.sessionBusy() {
		t.Error("sessionBusy = true when the driver is idle")
	}
}
