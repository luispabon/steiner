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

// applyConversationStateForTest feeds the driver's real state event.
func applyConversationStateForTest(m *Model, state string, held bool, pending int) {
	m.applyEvent(output.NewConversationStateEvent(state, held, pending, false))
}

func TestSessionRuntimeCountsUnionOfParentAndSubAgents(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)

	m.rootTurnActive = true
	m.syncSessionClockAt(sessionTestStart.Add(10 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(10 * time.Second)); got != 10*time.Second {
		t.Errorf("runtime while the root turn is active = %v, want 10s", got)
	}

	// The root turn ends but two sub-agents are queued or running: the span
	// stays open.
	m.rootTurnActive = false
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

	// A later root turn opens a new span at its start.
	m.rootTurnActive = true
	m.syncSessionClockAt(sessionTestStart.Add(90 * time.Second))
	if got := m.sessionRuntimeAt(sessionTestStart.Add(95 * time.Second)); got != 25*time.Second {
		t.Errorf("runtime after the next root turn = %v, want 25s", got)
	}
}

func TestSessionRuntimeFreezesOnParkedResults(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	m.rootTurnActive = true
	m.syncSessionClockAt(sessionTestStart.Add(5 * time.Second))

	// Finished sub-agent results waiting for the next message are not work, even
	// while the driver reports a waiting state with held results.
	m.rootTurnActive = false
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

func TestSessionRuntimeNormalSubmitAndStop(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = &testController{}

	m.executeSubmitAction("hello", "hello")
	if !m.sessionWorkActive() {
		t.Fatal("a normal submit did not activate the root turn")
	}
	if m.sessionActiveSince == nil {
		t.Fatal("a normal submit left the runtime span closed")
	}

	applyConversationStateForTest(m, conversationStateGenerating, false, 0)
	if !m.sessionWorkActive() {
		t.Fatal("the driver's generating state deactivated the root turn")
	}

	// A root stop ends the turn; ordinary turns emit a stop reason, not a run
	// finished event.
	m.applyEvent(output.NewStopReasonEvent(1, "completed", nil))
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("a root stop did not close the runtime")
	}

	// A repeated generating state must not revive the turn after the stop.
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("a stale generating state revived the runtime after a stop")
	}

	applyConversationStateForTest(m, conversationStateIdle, false, 0)
	if m.sessionWorkActive() {
		t.Error("the idle state left the runtime active")
	}
}

func TestSessionRuntimeContinuationAfterSubAgentResults(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = &testController{}
	m.executeSubmitAction("hello", "hello")
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)

	occ := agentOcc("child-1")
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, ""))
	m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "task", "", "review"), "child-1"))
	applyConversationStateForTest(m, conversationStateWaiting, false, 1)
	if !m.sessionWorkActive() {
		t.Fatal("waiting on a running sub-agent stopped the runtime")
	}

	// The finished result is held: the runtime freezes until the driver resumes.
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: occ, AgentType: "review", Status: "completed",
	}))
	applyConversationStateForTest(m, conversationStateWaiting, true, 1)
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("held sub-agent results kept the runtime active")
	}

	// The driver resumes automatically: waiting to generating restarts the turn
	// without another submit.
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)
	if !m.sessionWorkActive() || m.sessionActiveSince == nil {
		t.Fatal("an automatic continuation did not restart the runtime")
	}

	m.applyEvent(output.NewStopReasonEvent(2, "completed", nil))
	if m.sessionWorkActive() {
		t.Error("the continuation's stop did not end the runtime")
	}
}

func TestSessionRuntimeIgnoresStaleGeneratingWithoutRootTurn(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(sessionTestStart)
	m.convState = output.ConversationStateEvent{State: conversationStateGenerating}
	m.convStateSeen = true

	m.syncSessionClockAt(sessionTestStart)
	if got := m.sessionRuntimeAt(sessionTestStart.Add(time.Minute)); got != 0 {
		t.Errorf("runtime under a stale generating state = %v, want 0", got)
	}
}

func TestSessionRuntimeCompactionRunLifecycle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	// Run started and finished bracket compaction runs, not ordinary turns.
	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))
	if !m.sessionWorkActive() || m.sessionActiveSince == nil {
		t.Fatal("a compaction run start did not activate the runtime")
	}

	m.applyEvent(output.NewRunFinishedEvent(1, "completed", "", "", nil))
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("a compaction run finish left the runtime active")
	}
}

func TestSessionRuntimeTracksGroupedAndHeldCompletions(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)

	for _, id := range []string{"child-1", "child-2"} {
		occ := agentOcc(id)
		m.applyEvent(output.NewDelegationAcceptedEvent(occ, "grp"))
		m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "task", "", "review"), id))
	}
	if !m.sessionWorkActive() {
		t.Fatal("running grouped sub-agents did not activate the runtime")
	}

	// The parent run hands off and waits; the roster now carries the activity.
	applyConversationStateForTest(m, conversationStateWaiting, false, 2)
	if !m.sessionWorkActive() {
		t.Fatal("the waiting state dropped the runtime while grouped sub-agents ran")
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
	applyConversationStateForTest(m, conversationStateWaiting, true, 2)
	if m.sessionWorkActive() {
		t.Error("held results reactivated the runtime")
	}
}

func TestSessionClockResetsOnClear(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(sessionTestStart)
	m.rootTurnActive = true
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
	m.rootTurnActive = true

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
	m.rootTurnActive = true
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
	if m.rootTurnActive {
		t.Error("rootTurnActive = true after reset, want false")
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
