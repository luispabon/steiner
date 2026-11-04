package tui

import (
	"errors"
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

	// The real normal sequence: run started, driver generating, stop reason,
	// run finished, then the driver settles to idle.
	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "hello", 0, 0))
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)
	m.applyEvent(output.NewStopReasonEvent(1, "complete", nil))
	if !m.sessionWorkActive() {
		t.Fatal("a stop reason while the driver still generates ended the runtime early")
	}
	m.applyEvent(output.NewRunFinishedEvent(1, "complete", "", "", nil))
	if !m.sessionWorkActive() {
		t.Fatal("a run finished event while the driver still generates ended the runtime early")
	}

	applyConversationStateForTest(m, conversationStateIdle, false, 0)
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the idle state did not close the runtime")
	}
}

func TestSessionRuntimeGeneratingAuthoritativeAcrossChainedRuns(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())
	applyConversationStateForTest(m, conversationStateGenerating, false, 0)

	// Chained runs emit their own started, stop reason and finished events with
	// no intervening state change; the driver stays generating throughout.
	for run := 0; run < 2; run++ {
		m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))
		m.applyEvent(output.NewStopReasonEvent(run+1, "complete", nil))
		m.applyEvent(output.NewRunFinishedEvent(run+1, "complete", "", "", nil))
		if !m.sessionWorkActive() {
			t.Fatalf("run %d boundary ended the runtime while the driver was generating", run)
		}
		if m.sessionActiveSince == nil {
			t.Fatalf("run %d boundary closed the runtime span", run)
		}
	}

	applyConversationStateForTest(m, conversationStateWaiting, false, 0)
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the waiting state did not end the chained turn")
	}
}

func TestSessionRuntimeFallbackRootRunLifecycle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	// The CLI runner's normal events, with no driver state to consult.
	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "hello", 0, 0))
	if !m.sessionWorkActive() || m.sessionActiveSince == nil {
		t.Fatal("run started did not activate the fallback runtime")
	}
	m.applyEvent(output.NewStopReasonEvent(1, "complete", nil))
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the fallback runtime stayed active after a stop reason")
	}

	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))
	m.applyEvent(output.NewRunFinishedEvent(1, "complete", "", "", nil))
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the fallback runtime stayed active after run finished")
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

	// An interrupt emits a root stop; the runtime ends when the driver settles
	// out of generating, not on the stop alone.
	m.applyEvent(output.NewStopReasonEvent(2, "cancelled", nil))
	if !m.sessionWorkActive() {
		t.Fatal("a stop reason ended chained generation before the driver settled")
	}
	applyConversationStateForTest(m, conversationStateWaiting, true, 0)
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the driver settling after an interrupt did not end the runtime")
	}
}

func TestSessionRuntimeCompactionRunWhileDriverIdle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())
	applyConversationStateForTest(m, conversationStateIdle, false, 0)
	if m.sessionWorkActive() {
		t.Fatal("the idle state started the runtime before compaction")
	}

	// Compaction emits its own run pair without moving the driver out of idle.
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

// TestReplayedOrphanDelegationDoesNotActivateRuntime covers a saved-session
// replay of a delegate call with ledger evidence but no persisted result:
// replaySessionMessages emits the start and no terminal event, so the display
// roster keeps a running row. That historical row must not count as live work,
// or the runtime would advance while the resumed session sits idle.
func TestReplayedOrphanDelegationDoesNotActivateRuntime(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	m.applyEvent(output.NewReplayStartedEvent())
	occ := agentOcc("agent-orphan")
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, "grp"))
	m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "task", "", "explore"), "agent-orphan"))
	m.applyEvent(output.NewReplayFinishedEvent())

	entry, ok := m.roster.entries["agent-orphan"]
	if !ok || entry.status != rosterRunning {
		t.Fatalf("replayed orphan row = %#v, want a running display row", entry)
	}
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("a replayed delegation start without a terminal event activated the runtime")
	}

	// A live start for the same agent is real work and reactivates the runtime.
	m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "next task", "", "explore"), "agent-orphan"))
	if !m.sessionWorkActive() {
		t.Fatal("a live delegation start after replay did not activate the runtime")
	}
}

// TestSessionRuntimeCompactionSurvivesWaitingChildFinish reproduces the manual
// compaction undercount: compaction starts while the driver waits, a sub-agent
// finishes mid-compaction, and the driver's waiting state event must not end the
// compaction span.
func TestSessionRuntimeCompactionSurvivesWaitingChildFinish(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.startSessionClock(time.Now())

	occ := agentOcc("child-1")
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, ""))
	m.applyEvent(output.WithAgentScope(output.NewDelegationStartedEvent(occ, "task", "", "review"), "child-1"))
	applyConversationStateForTest(m, conversationStateWaiting, false, 1)
	if !m.sessionWorkActive() {
		t.Fatal("waiting on a running sub-agent did not activate the runtime")
	}

	// Manual compaction is allowed while the driver waits and emits its own run
	// pair without moving the driver state.
	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))
	m.applyEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind: "compaction", Scope: "conversation", Severity: "compacting", Notes: []string{"starting compaction"},
	}))
	if !m.compaction.Active() {
		t.Fatal("the compaction start event did not mark compaction active")
	}

	// The sub-agent finishes while compaction is still in flight, and the
	// driver emits a waiting state event for the changed pending count.
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: occ, AgentType: "review", Status: "completed",
	}))
	applyConversationStateForTest(m, conversationStateWaiting, false, 0)
	if !m.sessionWorkActive() || m.sessionActiveSince == nil {
		t.Fatal("a waiting state event ended the runtime while compaction was in flight")
	}

	m.applyEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind: "compaction", Scope: "conversation", Severity: "completed",
	}))
	m.applyEvent(output.NewRunFinishedEvent(0, "complete", "", "", nil))
	if m.sessionWorkActive() || m.sessionActiveSince != nil {
		t.Fatal("the finished compaction run left the runtime active")
	}
}

func TestFailedSubmitRestoresSessionClock(t *testing.T) {
	t.Parallel()

	t.Run("first prompt", func(t *testing.T) {
		t.Parallel()
		m := newModel(Config{}, nil)
		m.controller = &testController{err: errors.New("submit failed")}

		m.executeSubmitAction("hello", "hello")

		if m.sessionStartedAt != nil || m.sessionRuntime != 0 || m.sessionActiveSince != nil {
			t.Fatalf("failed first submit left the clock set: started=%v runtime=%v active=%v",
				m.sessionStartedAt, m.sessionRuntime, m.sessionActiveSince)
		}
		if m.rootTurnActive {
			t.Error("failed first submit left the root turn active")
		}
		if m.sidebar.runtimeActive {
			t.Error("failed first submit left the sidebar runtime active")
		}
	})

	t.Run("later prompt", func(t *testing.T) {
		t.Parallel()
		started := time.Now().Add(-time.Minute)
		m := newModel(Config{}, nil)
		m.controller = &testController{err: errors.New("submit failed")}
		m.sessionStartedAt = &started
		m.sessionRuntime = 2 * time.Second

		m.executeSubmitAction("hello", "hello")

		if m.sessionStartedAt == nil || !m.sessionStartedAt.Equal(started) {
			t.Fatalf("failed later submit replaced the start time: %v", m.sessionStartedAt)
		}
		if got := m.sessionRuntime; got < 2*time.Second || got > 3*time.Second {
			t.Errorf("runtime after a failed later submit = %v, want the 2s already accumulated", got)
		}
		if m.sessionActiveSince != nil {
			t.Error("failed later submit left an open runtime span")
		}
		if m.rootTurnActive {
			t.Error("failed later submit left the root turn active")
		}
	})
}

// TestDriverWorkBeforeFirstPromptStaysUncounted documents the first-prompt rule:
// driver work after a reset but before the next prompt has no start time to
// measure from, so it does not advance the runtime. The next prompt starts the
// clock.
func TestDriverWorkBeforeFirstPromptStaysUncounted(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = &testController{}
	m.startSessionClock(sessionTestStart)
	m.resetSessionClock()

	applyConversationStateForTest(m, conversationStateGenerating, false, 0)
	if m.sessionStartedAt != nil || m.sessionActiveSince != nil {
		t.Fatal("driver work before the first prompt started the session clock")
	}

	m.executeSubmitAction("hello", "hello")
	if m.sessionStartedAt == nil {
		t.Fatal("the first prompt after a reset did not start the session clock")
	}
	if !m.sessionWorkActive() {
		t.Error("the first prompt after a reset did not activate the runtime")
	}
}
