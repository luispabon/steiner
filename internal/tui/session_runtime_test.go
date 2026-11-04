package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// sessionTestStart is a fixed instant so explicit-time assertions cannot pick
// up real elapsed time.
var sessionTestStart = time.Date(2026, 11, 4, 9, 30, 0, 0, time.Local)

// setRosterStatus forces a roster row's lifecycle status.
func setRosterStatus(m *Model, agentID, status string) {
	e := m.roster.upsert(agentID)
	e.status = status
	e.startTime = sessionTestStart.UnixNano()
}

func TestSessionRuntimeCountsUnionOfParentAndSubAgents(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)

	m.rootRunActive = true
	m.syncSessionClockAt(sessionTestStart.Add(10 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(10 * time.Second)); got != 10*time.Second {
		t.Errorf("runtime while the root run is active = %v, want 10s", got)
	}

	// The root run finishes but two sub-agents are queued or running: the span
	// stays open.
	m.rootRunActive = false
	setRosterStatus(m, "child-1", rosterRunning)
	setRosterStatus(m, "child-2", rosterQueued)
	m.syncSessionClockAt(sessionTestStart.Add(10 * time.Second))
	if m.sessionActiveSince == nil {
		t.Fatal("runtime span closed while queued or running sub-agents remained")
	}
	if got := m.sessionRuntimeAt(sessionTestStart.Add(20 * time.Second)); got != 20*time.Second {
		t.Errorf("runtime with sub-agents = %v, want 20s", got)
	}

	// Every sub-agent finishing closes the span; the overlap counted once.
	setRosterStatus(m, "child-1", rosterDone)
	setRosterStatus(m, "child-2", rosterDone)
	m.syncSessionClockAt(sessionTestStart.Add(20 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(90 * time.Second)); got != 20*time.Second {
		t.Errorf("idle runtime = %v, want the 20s already accumulated", got)
	}

	// A later root run opens a new span at its start.
	m.rootRunActive = true
	m.syncSessionClockAt(sessionTestStart.Add(90 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(95 * time.Second)); got != 25*time.Second {
		t.Errorf("runtime after the next root run = %v, want 25s", got)
	}
}

func TestSessionRuntimeFreezesOnParkedResults(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	m.rootRunActive = true
	m.syncSessionClockAt(sessionTestStart.Add(5 * time.Second))

	// Finished sub-agent results waiting for the next message are not work, even
	// while the driver still reports a waiting state with held results.
	m.rootRunActive = false
	setRosterStatus(m, "child-1", rosterDone)
	m.convState = output.ConversationStateEvent{State: conversationStateWaiting, Held: true, Pending: 2}
	m.convStateSeen = true
	m.syncSessionClockAt(sessionTestStart.Add(5 * time.Second))
	if m.sessionActiveSince != nil {
		t.Fatal("runtime span stayed open on parked results")
	}
	if got := m.sessionRuntimeAt(sessionTestStart.Add(time.Minute)); got != 5*time.Second {
		t.Errorf("parked runtime = %v, want the 5s already accumulated", got)
	}
}

func TestSessionRuntimeRootRunLifecycleWithoutDriverState(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))
	if m.convStateSeen {
		t.Fatal("the run lifecycle test must not receive conversation state")
	}
	if !m.sessionWorkActive() {
		t.Fatal("a root run in progress did not activate the runtime")
	}
	if m.sessionActiveSince == nil {
		t.Fatal("root run start left the runtime span closed")
	}

	m.applyEvent(output.NewRunFinishedEvent(1, "completed", "", "", nil))
	if m.sessionWorkActive() {
		t.Error("a finished root run kept the runtime active")
	}
	if m.sessionActiveSince != nil {
		t.Error("root run finish left the runtime span open")
	}
}

func TestSessionRuntimeRootStopOverridesStaleGenerating(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())
	m.convState = output.ConversationStateEvent{State: conversationStateGenerating}
	m.convStateSeen = true
	m.rootRunActive = true

	m.applyEvent(output.NewStopReasonEvent(1, "cancelled", nil))

	if m.rootRunActive {
		t.Error("root stop left the root run active")
	}
	if m.sessionWorkActive() {
		t.Error("stale generating state kept the runtime active after a root stop")
	}
	if m.sessionActiveSince != nil {
		t.Error("stale generating state kept the runtime span open after a root stop")
	}
}

func TestSessionRuntimeIgnoresStaleGeneratingWithoutRootRun(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	m.convState = output.ConversationStateEvent{State: conversationStateGenerating}
	m.convStateSeen = true

	m.syncSessionClockAt(sessionTestStart)
	if got := m.sessionRuntimeAt(sessionTestStart.Add(time.Minute)); got != 0 {
		t.Errorf("runtime under stale generating state = %v, want 0", got)
	}
}

func TestSessionRuntimeTracksGroupedAndHeldCompletions(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	for _, id := range []string{"child-1", "child-2"} {
		occ := agentOcc(id)
		m.applyEvent(output.NewDelegationAcceptedEvent(occ, "grp"))
		m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "task", "", "review"), id))
	}
	if !m.sessionWorkActive() {
		t.Fatal("running grouped sub-agents did not activate the runtime")
	}

	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: agentOcc("child-1"), AgentType: "review", Status: "completed",
	}))
	if !m.sessionWorkActive() {
		t.Error("one grouped sub-agent finishing stopped the runtime while its peer ran")
	}

	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: agentOcc("child-2"), AgentType: "review", Status: "completed",
	}))
	if m.sessionWorkActive() {
		t.Error("the last grouped completion kept the runtime active")
	}
	if m.sessionActiveSince != nil {
		t.Error("the last grouped completion left the runtime span open")
	}

	// Held results arrive after the completions: the driver waits while the
	// roster has no queued or running work, so the runtime stays frozen.
	m.convState = output.ConversationStateEvent{State: conversationStateWaiting, Held: true, Pending: 2}
	m.convStateSeen = true
	if m.sessionWorkActive() {
		t.Error("held results reactivated the runtime")
	}
}

func TestSessionClockResetsOnClear(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(sessionTestStart)
	m.rootRunActive = true
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
	m.rootRunActive = true

	_, _ = m.executeForkSessionAction()
	if m.sessionStartedAt != nil {
		t.Fatal("fork kept the previous session clock")
	}
	assertSessionClockReset(t, m)
}

func TestSessionClockResetsOnResume(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = &testController{}
	m.startSessionClock(sessionTestStart)
	m.rootRunActive = true
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
	if m.rootRunActive {
		t.Error("rootRunActive = true after reset, want false")
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
		t.Fatal("renderSidebar did not invalidate after the start and runtime changed")
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
