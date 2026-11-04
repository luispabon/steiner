package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// sessionTestStart is a fixed past instant so explicit-time assertions cannot
// pick up real elapsed time.
var sessionTestStart = time.Date(2026, 11, 4, 9, 30, 0, 0, time.Local)

// setConversationWork sets the driver state the runtime accounting reads.
func setConversationWork(m *Model, state string, activeSubAgents int) {
	m.convState = output.ConversationStateEvent{State: state, ActiveSubAgents: activeSubAgents}
	m.convStateSeen = true
}

// applyConversationWorkState applies a conversation state event with an
// explicit queued plus running sub-agent count.
func applyConversationWorkState(t *testing.T, m *Model, state string, activeSubAgents int) {
	t.Helper()
	event := output.NewConversationStateEvent(state, false, 0, false)
	payload := event.Payload.(output.ConversationStateEvent)
	payload.ActiveSubAgents = activeSubAgents
	event.Payload = payload
	m.applyEvent(event)
}

func TestSessionRuntimeCountsUnionOfParentAndSubAgents(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)

	setConversationWork(m, conversationStateGenerating, 0)
	m.syncSessionClockAt(sessionTestStart.Add(10 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(10 * time.Second)); got != 10*time.Second {
		t.Errorf("runtime while generating = %v, want 10s", got)
	}

	// The parent stops generating but two sub-agents are queued or running: the
	// span stays open.
	setConversationWork(m, conversationStateWaiting, 2)
	m.syncSessionClockAt(sessionTestStart.Add(10 * time.Second))
	if m.sessionActiveSince == nil {
		t.Fatal("runtime span closed while queued or running sub-agents remained")
	}

	// The parent generating again overlaps the sub-agents; the union counts once.
	setConversationWork(m, conversationStateGenerating, 2)
	m.syncSessionClockAt(sessionTestStart.Add(20 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(20 * time.Second)); got != 20*time.Second {
		t.Errorf("overlapping runtime = %v, want 20s", got)
	}

	setConversationWork(m, conversationStateIdle, 0)
	m.syncSessionClockAt(sessionTestStart.Add(20 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(90 * time.Second)); got != 20*time.Second {
		t.Errorf("idle runtime = %v, want the 20s already accumulated", got)
	}

	// Work resuming after an idle stretch opens a new span at the resume time.
	setConversationWork(m, conversationStateGenerating, 0)
	m.syncSessionClockAt(sessionTestStart.Add(90 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(95 * time.Second)); got != 25*time.Second {
		t.Errorf("runtime after resuming = %v, want 25s", got)
	}
}

func TestSessionRuntimeFreezesOnParkedResults(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	setConversationWork(m, conversationStateGenerating, 0)
	m.syncSessionClockAt(sessionTestStart.Add(5 * time.Second))

	// Finished sub-agent results waiting for the next message are not work.
	setConversationWork(m, conversationStateWaiting, 0)
	m.convState.Held = true
	m.convState.Pending = 2
	m.syncSessionClockAt(sessionTestStart.Add(5 * time.Second))
	if m.sessionActiveSince != nil {
		t.Fatal("runtime span stayed open on parked results")
	}
	if got := m.sessionRuntimeAt(sessionTestStart.Add(time.Minute)); got != 5*time.Second {
		t.Errorf("parked runtime = %v, want the 5s already accumulated", got)
	}
}

func TestSessionRuntimeFallsBackWithoutDriverState(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	t.Run("active delegation", func(t *testing.T) {
		t.Parallel()
		m := &Model{content: contentBuffer{styles: styles}}
		m.content.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "task", "", "explore"))
		m.startSessionClock(sessionTestStart)
		m.syncSessionClockAt(sessionTestStart.Add(time.Second))
		if got := m.sessionRuntimeAt(sessionTestStart.Add(6 * time.Second)); got != 6*time.Second {
			t.Errorf("fallback runtime = %v, want 6s", got)
		}
	})

	t.Run("streaming parent", func(t *testing.T) {
		t.Parallel()
		m := &Model{status: statusState{streaming: true}}
		m.startSessionClock(sessionTestStart)
		m.syncSessionClockAt(sessionTestStart.Add(time.Second))
		if got := m.sessionRuntimeAt(sessionTestStart.Add(4 * time.Second)); got != 4*time.Second {
			t.Errorf("fallback runtime = %v, want 4s", got)
		}
	})
}

func TestConversationStateEventDrivesRuntime(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	applyConversationWorkState(t, m, conversationStateGenerating, 0)
	if m.sessionActiveSince == nil {
		t.Fatal("generating state closed the runtime span")
	}

	applyConversationWorkState(t, m, conversationStateWaiting, 2)
	if m.sessionActiveSince == nil {
		t.Fatal("waiting with queued or running sub-agents closed the runtime span")
	}

	applyConversationWorkState(t, m, conversationStateWaiting, 0)
	if m.sessionActiveSince != nil {
		t.Fatal("waiting on finished sub-agent results left the runtime span open")
	}
}

func TestStaleConversationStateDoesNotAdvanceRuntime(t *testing.T) {
	t.Parallel()
	m := &Model{controller: &epochController{testController: &testController{}, epoch: 2}}
	m.startSessionClock(sessionTestStart)
	setConversationWork(m, conversationStateIdle, 0)
	m.syncSessionClockAt(sessionTestStart)

	stale := output.NewConversationStateEvent(conversationStateGenerating, false, 0, false)
	payload := stale.Payload.(output.ConversationStateEvent)
	payload.Epoch = 1
	payload.ActiveSubAgents = 3
	stale.Payload = payload
	m.applyEvent(stale)

	if m.convState.State != conversationStateIdle {
		t.Fatalf("stale state event changed convState to %q", m.convState.State)
	}
	m.syncSessionClockAt(sessionTestStart.Add(time.Minute))
	if m.sessionActiveSince != nil {
		t.Fatal("stale state event opened a runtime span")
	}
	if got := m.sessionRuntimeAt(sessionTestStart.Add(time.Minute)); got != 0 {
		t.Errorf("runtime after stale event = %v, want 0", got)
	}
}

func TestSessionClockResetsOnClear(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(sessionTestStart)
	setConversationWork(m, conversationStateGenerating, 0)
	m.syncSessionClockAt(sessionTestStart.Add(30 * time.Second))

	m.resetConversationUI()

	assertSessionClockReset(t, m)
	if m.sidebar.startedLocal != "" {
		t.Errorf("sidebar.startedLocal = %q after clear, want empty", m.sidebar.startedLocal)
	}
}

func TestSessionClockResetsOnFork(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.content.AppendLine("hello")
	m.startSessionClock(sessionTestStart)

	if _, _ = m.executeForkSessionAction(); m.sessionStartedAt != nil {
		t.Fatal("fork kept the previous session clock")
	}
	assertSessionClockReset(t, m)
}

func TestSessionClockResetsOnResume(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = &testController{}
	m.startSessionClock(sessionTestStart)
	m.sessionPicker.candidates = []session.IndexEntry{{ID: "sess-1"}}
	m.sessionPicker.selection = 0

	cmd, ok := m.dispatchSelectedSessionAction(func(id string) interactive.Action {
		return interactive.LoadSession{SessionID: id}
	})
	if !ok || cmd == nil {
		t.Fatalf("resume dispatch = (%v, %t), want a command", cmd, ok)
	}
	assertSessionClockReset(t, m)
}

func assertSessionClockReset(t *testing.T, m *Model) {
	t.Helper()
	if m.sessionStartedAt != nil {
		t.Errorf("sessionStartedAt = %v after reset, want nil", *m.sessionStartedAt)
	}
	if m.sessionRuntime != 0 {
		t.Errorf("sessionRuntime = %v after reset, want 0", m.sessionRuntime)
	}
	if m.sessionActiveSince != nil {
		t.Errorf("sessionActiveSince = %v after reset, want nil", *m.sessionActiveSince)
	}
	if m.sidebar.runtimeActive {
		t.Error("sidebar.runtimeActive = true after reset, want false")
	}
}

func TestLeftoverSessionTimerChainsExpire(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	firstGen := m.sessionTickGen
	if _, cmd := m.handleSessionTickMsg(sessionTickMsg{gen: firstGen}); cmd == nil {
		t.Fatal("the live timer chain did not re-arm")
	}

	m.resetSessionClock()
	if _, cmd := m.handleSessionTickMsg(sessionTickMsg{gen: firstGen}); cmd != nil {
		t.Fatal("a timer chain from the replaced session re-armed")
	}
	m.startSessionClock(sessionTestStart)
	if _, cmd := m.handleSessionTickMsg(sessionTickMsg{gen: firstGen}); cmd != nil {
		t.Fatal("a leftover timer chain re-armed against the new session")
	}
	if _, cmd := m.handleSessionTickMsg(sessionTickMsg{gen: m.sessionTickGen}); cmd == nil {
		t.Fatal("the new session timer chain did not re-arm")
	}
}

func TestRenderSidebarTracksStartedAndRuntime(t *testing.T) {
	t.Parallel()
	m := newSidebarTestModel(t)
	m.renderSidebar(m.width, m.height)

	m.sidebar.startedLocal = "2026-11-04 09:30"
	m.sidebar.runtimeActive = true
	m.sidebar.runtimeElapsedSec = 4054
	first := m.renderSidebar(m.width, m.height)
	if want := m.sidebar.View(m.width, m.height); first != want {
		t.Fatalf("renderSidebar did not invalidate after the start and runtime changed")
	}
	plain := stripANSI(first)
	if !strings.Contains(plain, "2026-11-04 09:30") {
		t.Error("rendered sidebar is missing the session start time")
	}
	if !strings.Contains(plain, "runtime") || !strings.Contains(plain, "01:07:34") {
		t.Error("rendered sidebar is missing the runtime row")
	}

	m.sidebar.runtimeElapsedSec = 4055
	if second := m.renderSidebar(m.width, m.height); second == first {
		t.Error("sidebar render cache served a stale frame after the runtime changed")
	}
}
