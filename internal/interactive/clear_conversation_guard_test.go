package interactive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// setPendingStates replaces the stub's pending jobs with one job per state.
func (b *stubBackground) setPendingStates(states ...agent.SubAgentState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = nil
	for i, state := range states {
		b.pending = append(b.pending, agent.PendingSubAgent{
			AgentID:   fmt.Sprintf("agent-%d", i+1),
			AgentType: "code",
			State:     state,
		})
	}
}

// newClearGuardSession builds a session whose drivers receive deterministic
// group scopes and, when hook is non-nil, a wired supervisor clear hook.
func newClearGuardSession(t *testing.T, bg *stubBackground, hook func(string) int) *Session {
	t.Helper()
	return newClearGuardSessionWithEvents(t, bg, hook, nil)
}

func newClearGuardSessionWithEvents(t *testing.T, bg *stubBackground, hook func(string) int, baseEvents output.EventSink) *Session {
	t.Helper()
	return newClearGuardSessionWithScopes(t, bg, hook, baseEvents, nil)
}

// newClearGuardSessionWithScopes is newClearGuardSessionWithEvents with a
// callback that observes group scope releases.
func newClearGuardSessionWithScopes(t *testing.T, bg *stubBackground, hook func(string) int, baseEvents output.EventSink, onRelease func(string)) *Session {
	t.Helper()
	var scopeMu sync.Mutex
	nextScope := 0
	return testNewSession(t, Dependencies{
		BaseEvents:   baseEvents,
		SessionStore: newMockSessionStore(),
		Config:       guardTestConfig(),
		Runner: newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
			return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
		}),
		Background: bg,
		OpenGroupScope: func(agent.DelegationGroupLedger) (string, func()) {
			scopeMu.Lock()
			defer scopeMu.Unlock()
			nextScope++
			scope := fmt.Sprintf("scope-%d", nextScope)
			return scope, func() {
				if onRelease != nil {
					onRelease(scope)
				}
			}
		},
		ClearFinishedSubAgents: hook,
	})
}

func waitForClearSession(t *testing.T, s *Session) {
	t.Helper()
	submitAndWait(t, s, "hi", nil)
	waitForWaiting(t, s)
}

// holdClearSession waits for a waiting driver and then holds its completions,
// the state in which clear may discard finished sub-agents.
func holdClearSession(t *testing.T, s *Session) {
	t.Helper()
	waitForClearSession(t, s)
	s.currentDriver().StopTurn()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.currentDriver().WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	state, held := s.currentDriver().State()
	if state != agent.DriverWaiting || !held {
		t.Fatalf("driver state = %q held=%v, want waiting held", state, held)
	}
}

func TestClearConversationDiscardsFinishedViaScopeHook(t *testing.T) {
	bg := &stubBackground{}
	var gotScope string
	hook := func(scope string) int {
		gotScope = scope
		bg.setPending()
		return 0
	}
	s := newClearGuardSession(t, bg, hook)
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)

	oldID, oldScope := s.SessionID(), s.driver.groupScope
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if gotScope != oldScope {
		t.Fatalf("hook scope = %q, want current scope %q", gotScope, oldScope)
	}
	if s.driver.groupScope == oldScope {
		t.Fatalf("driver scope = %q, want a scope rotated after discard", s.driver.groupScope)
	}
	if s.SessionID() == oldID {
		t.Fatalf("session ID = %q, want a new identity", s.SessionID())
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("conversation = %#v, want empty", got)
	}
}

func TestClearConversationNilHookKeepsFinishedRefusal(t *testing.T) {
	bg := &stubBackground{}
	s := newClearGuardSession(t, bg, nil)
	bg.setPendingStates(agent.SubAgentFinished)
	waitForClearSession(t, s)

	oldID, oldDriver, oldConv := s.SessionID(), s.currentDriver(), s.Conversation()
	oldState, oldHeld := oldDriver.State()
	err := s.Handle(context.Background(), ClearConversation{})
	if err == nil || !strings.Contains(err.Error(), "1 sub-agents still running; wait for them or stop them first") {
		t.Fatalf("ClearConversation = %v, want the shared pending refusal", err)
	}
	if s.SessionID() != oldID || s.currentDriver() != oldDriver || !reflect.DeepEqual(s.Conversation(), oldConv) {
		t.Fatal("refused clear changed the session")
	}
	if state, held := oldDriver.State(); state != oldState || held != oldHeld {
		t.Fatalf("driver state = %q held=%v, want %q held=%v", state, held, oldState, oldHeld)
	}
}

func TestClearConversationUnheldPendingKeepsSharedRefusal(t *testing.T) {
	for _, state := range []agent.SubAgentState{
		agent.SubAgentRunning,
		agent.SubAgentQueued,
		agent.SubAgentFinished,
	} {
		t.Run(string(state), func(t *testing.T) {
			bg := &stubBackground{}
			hookCalled := false
			hook := func(string) int {
				hookCalled = true
				return 0
			}
			s := newClearGuardSession(t, bg, hook)
			bg.setPendingStates(state)
			waitForClearSession(t, s)

			oldID, oldDriver, oldConv := s.SessionID(), s.currentDriver(), s.Conversation()
			oldState, oldHeld := oldDriver.State()
			err := s.Handle(context.Background(), ClearConversation{})
			if err == nil || !strings.Contains(err.Error(), "wait for them or stop them first") {
				t.Fatalf("ClearConversation = %v, want the shared pending refusal", err)
			}
			if hookCalled {
				t.Fatal("clear hook ran for an unheld driver")
			}
			if s.SessionID() != oldID || s.currentDriver() != oldDriver || !reflect.DeepEqual(s.Conversation(), oldConv) {
				t.Fatal("refused clear changed the session")
			}
			if state, held := oldDriver.State(); state != oldState || held != oldHeld {
				t.Fatalf("driver state = %q held=%v, want %q held=%v", state, held, oldState, oldHeld)
			}
		})
	}
}

func TestClearConversationHeldHookRefusalKeepsHold(t *testing.T) {
	bg := &stubBackground{}
	var runs atomic.Int64
	hookCalled := false
	hook := func(string) int {
		hookCalled = true
		return 1
	}
	s := newClearGuardSession(t, bg, hook)
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		runs.Add(1)
		return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
	}))
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)

	oldID, oldDriver, oldConv := s.SessionID(), s.currentDriver(), s.Conversation()
	err := s.Handle(context.Background(), ClearConversation{})
	if err == nil || !strings.Contains(err.Error(), "another scope") {
		t.Fatalf("ClearConversation = %v, want a scope refusal", err)
	}
	if !hookCalled {
		t.Fatal("clear hook did not run")
	}
	if s.SessionID() != oldID || s.currentDriver() != oldDriver || !reflect.DeepEqual(s.Conversation(), oldConv) {
		t.Fatal("refused clear changed the session")
	}
	if state, held := oldDriver.State(); state != agent.DriverWaiting || !held {
		t.Fatalf("driver state = %q held=%v, want waiting held", state, held)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1 (refusal must not start work)", got)
	}
}

func TestClearConversationRefusalDoesNotCancelRun(t *testing.T) {
	bg := &stubBackground{}
	hookCalled := false
	hook := func(string) int {
		hookCalled = true
		return 0
	}
	s := newClearGuardSession(t, bg, hook)
	started := make(chan struct{})
	runCtx := make(chan context.Context, 1)
	release := make(chan struct{})
	s.SetRunner(newRunExecutorFunc(func(ctx context.Context, conv []agent.Message) (RunResult, error) {
		runCtx <- ctx
		close(started)
		<-release
		return RunResult{Conversation: conv}, nil
	}))
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hi"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	<-started
	ctx := <-runCtx

	if err := s.Handle(context.Background(), ClearConversation{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("ClearConversation during run = %v, want errRunInProgress", err)
	}
	if hookCalled {
		t.Fatal("clear hook ran while the driver was busy")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("refused clear cancelled the run: %v", err)
	}
	if state, _ := s.currentDriver().State(); state != agent.DriverGenerating {
		t.Fatalf("driver state = %q, want generating", state)
	}
	close(release)
	waitSettled(t, s)
}

func TestClearConversationInFlightCompletionCannotWake(t *testing.T) {
	bg := &stubBackground{}
	var runs atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	hook := func(string) int {
		close(entered)
		<-release
		bg.setPending()
		return 0
	}
	s := newClearGuardSession(t, bg, hook)
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		runs.Add(1)
		return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
	}))
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)
	old := s.currentDriver()

	done := make(chan error, 1)
	go func() { done <- s.Handle(context.Background(), ClearConversation{}) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("clear hook did not run")
	}
	delivered := make(chan struct{})
	go func() {
		old.DeliverCompletions([]agent.SubAgentCompletion{{
			Seq: 1, ParentCallID: "call-agent-1", AgentID: "agent-1", AgentType: "code",
			Status: "complete", Body: `{"output":"late"}`,
		}})
		close(delivered)
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight completion delivery did not finish")
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1: in-flight completion woke the driver", got)
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("conversation = %#v, want empty", got)
	}
	if state, held := s.currentDriver().State(); state != agent.DriverIdle || held {
		t.Fatalf("final driver state = %q held=%v, want idle", state, held)
	}
	if got := len(bg.Pending()); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}

func TestRetiredDriverEventSinkDropsStateEvents(t *testing.T) {
	h := &driverHandle{epoch: 7}
	var got []output.Event
	sink := driverEventSink{sink: output.SinkFunc(func(event output.Event) { got = append(got, event) }), h: h}
	state := output.NewConversationStateEvent(string(agent.DriverWaiting), true, 1, false)
	sink.Emit(state)
	if len(got) != 1 {
		t.Fatalf("live driver state events = %d, want 1", len(got))
	}
	if payload, ok := got[0].Payload.(output.ConversationStateEvent); !ok || payload.Epoch != 7 {
		t.Fatalf("forwarded state payload = %#v, want epoch 7", got[0].Payload)
	}
	h.stale.Store(true)
	sink.Emit(state)
	if len(got) != 1 {
		t.Fatal("retired driver state event was forwarded")
	}
	warning := output.NewConversationWarningEvent("test", "kept")
	sink.Emit(warning)
	if len(got) != 2 || got[1].Type != warning.Type {
		t.Fatalf("retired driver non-state events = %+v, want the warning forwarded", got[1:])
	}
}

type conversationStateRecorder struct {
	mu     sync.Mutex
	events []output.ConversationStateEvent
}

func (r *conversationStateRecorder) Emit(event output.Event) {
	payload, ok := event.Payload.(output.ConversationStateEvent)
	if !ok {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, payload)
	r.mu.Unlock()
}

func (r *conversationStateRecorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

func (r *conversationStateRecorder) snapshot() []output.ConversationStateEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]output.ConversationStateEvent(nil), r.events...)
}

func TestClearConversationDropsRetiredStateEvents(t *testing.T) {
	bg := &stubBackground{}
	recorder := &conversationStateRecorder{}
	hook := func(string) int {
		bg.setPending()
		return 0
	}
	s := newClearGuardSessionWithEvents(t, bg, hook, recorder)
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)

	recorder.reset()
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	// The retired driver closes on a tracked goroutine; wait for its final
	// state events so a late emission cannot slip past the assertions.
	waitSettled(t, s)
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("post-clear state events = %+v, want none from the retired driver", events)
	}
}

func TestConversationEpochTracksLiveDriver(t *testing.T) {
	s := testNewSession(t, Dependencies{SessionStore: newMockSessionStore(), Config: guardTestConfig()})
	first := s.ConversationEpoch()
	if first == 0 {
		t.Fatal("initial conversation epoch = 0, want a nonzero epoch")
	}
	old := s.driver
	if old.epoch != first {
		t.Fatalf("handle epoch = %d, want the live epoch %d", old.epoch, first)
	}

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	second := s.ConversationEpoch()
	if second <= first {
		t.Fatalf("epoch after clear = %d, want above %d", second, first)
	}
	if got := s.driver.epoch; got != second {
		t.Fatalf("live handle epoch = %d, want %d", got, second)
	}
	if s.driver == old {
		t.Fatal("clear did not replace the driver")
	}
	waitSettled(t, s)
}

func TestLiveDriverStateEventsCarryLiveEpoch(t *testing.T) {
	bg := &stubBackground{}
	recorder := &conversationStateRecorder{}
	s := newClearGuardSessionWithEvents(t, bg, nil, recorder)
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)

	events := recorder.snapshot()
	if len(events) == 0 {
		t.Fatal("no conversation state events recorded")
	}
	want := s.ConversationEpoch()
	for _, event := range events {
		if event.Epoch != want {
			t.Fatalf("state event = %+v, want the live epoch %d", event, want)
		}
	}
}

func waitForHeldWaitingDriver(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if state, held := s.currentDriver().State(); state == agent.DriverWaiting && held {
			return
		}
		time.Sleep(time.Millisecond)
	}
	state, held := s.currentDriver().State()
	t.Fatalf("driver state = %q held=%v, want waiting held", state, held)
}

func waitClearDriverIdle(t *testing.T, s *Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.currentDriver().WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
}

func TestClearConversationAfterInterruptAndCompletionEndsIdle(t *testing.T) {
	bg := &stubBackground{}
	var runs atomic.Int64
	hook := func(string) int {
		bg.setPending()
		return 0
	}
	s := newClearGuardSession(t, bg, hook)
	started := make(chan struct{}, 4)
	s.SetRunner(newRunExecutorFunc(func(ctx context.Context, conv []agent.Message) (RunResult, error) {
		runs.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return RunResult{Conversation: conv}, ctx.Err()
	}))
	bg.setPendingStates(agent.SubAgentRunning)
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hi"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not start")
	}

	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("InterruptActiveRun: %v", err)
	}
	waitForHeldWaitingDriver(t, s)

	bg.setPendingStates(agent.SubAgentFinished)
	s.currentDriver().DeliverCompletions([]agent.SubAgentCompletion{{
		Seq: 1, ParentCallID: "call-agent-1", AgentID: "agent-1", AgentType: "code",
		Status: "complete", Body: `{"output":"done"}`,
	}})
	waitClearDriverIdle(t, s)

	old := s.currentDriver()
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if s.currentDriver() == old {
		t.Fatal("clear did not replace the driver")
	}
	if state, held := s.currentDriver().State(); state != agent.DriverIdle || held {
		t.Fatalf("final driver state = %q held=%v, want idle", state, held)
	}
	if got := len(bg.Pending()); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("conversation = %#v, want empty", got)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1: clear started a run", got)
	}
}

type eventRecorder struct {
	mu     sync.Mutex
	events []output.Event
}

func (r *eventRecorder) Emit(event output.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *eventRecorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

func (r *eventRecorder) countType(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, event := range r.events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func discardCompletion(marker string) agent.SubAgentCompletion {
	return agent.SubAgentCompletion{
		Seq: 1, ParentCallID: "call-agent-1", AgentID: "agent-1", AgentType: "code",
		Status: "complete", Body: fmt.Sprintf(`{"output":"%s"}`, marker),
	}
}

func lineageContains(lin agent.ConversationLineage, marker string) bool {
	for _, generation := range lin.Generations {
		for _, message := range generation.Messages {
			if strings.Contains(message.Content, marker) {
				return true
			}
		}
	}
	return false
}

func TestClearConversationDiscardsBufferedAndLateCompletions(t *testing.T) {
	bg := &stubBackground{}
	recorder := &eventRecorder{}
	entered := make(chan struct{})
	release := make(chan struct{})
	hook := func(string) int {
		close(entered)
		<-release
		bg.setPending()
		return 0
	}
	s := newClearGuardSessionWithEvents(t, bg, hook, recorder)
	store := s.deps.SessionStore.(*mockSessionStore)
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)
	old := s.currentDriver()
	oldID := s.SessionID()

	old.DeliverCompletions([]agent.SubAgentCompletion{discardCompletion("buffered-marker")})
	recorder.reset()

	done := make(chan error, 1)
	go func() { done <- s.Handle(context.Background(), ClearConversation{}) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("clear hook did not run")
	}
	lateDelivered := make(chan struct{})
	go func() {
		old.DeliverCompletions([]agent.SubAgentCompletion{discardCompletion("late-marker")})
		close(lateDelivered)
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	select {
	case <-lateDelivered:
	case <-time.After(10 * time.Second):
		t.Fatal("late completion delivery did not finish")
	}
	// The retired driver closes on a tracked goroutine; wait for its final
	// save before reading the store.
	waitSettled(t, s)
	// A delivery after the clear has closed the old inbox is dropped too.
	old.DeliverCompletions([]agent.SubAgentCompletion{discardCompletion("after-marker")})

	if got := recorder.countType(output.EventTypeSubAgentsDelivered); got != 0 {
		t.Fatalf("sub_agents_delivered events = %d, want 0", got)
	}
	store.mu.Lock()
	saved := store.savedSessions[oldID]
	store.mu.Unlock()
	for _, marker := range []string{"buffered-marker", "late-marker", "after-marker"} {
		if lineageContains(saved.Lineage, marker) {
			t.Fatalf("old saved transcript contains discarded completion %q", marker)
		}
	}
}

type blockingStateSink struct {
	mu      sync.Mutex
	events  []output.ConversationStateEvent
	blockMu sync.Mutex
	block   bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingStateSink) Emit(event output.Event) {
	payload, ok := event.Payload.(output.ConversationStateEvent)
	if !ok {
		return
	}
	s.mu.Lock()
	s.events = append(s.events, payload)
	s.mu.Unlock()
	s.blockMu.Lock()
	block := s.block
	s.blockMu.Unlock()
	if block && payload.State == string(agent.DriverWaiting) && payload.Held {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
}

func (s *blockingStateSink) arm() {
	s.blockMu.Lock()
	s.block = true
	s.blockMu.Unlock()
}

func TestClearConversationReturnsWhileRetiredSinkBlocked(t *testing.T) {
	bg := &stubBackground{}
	sink := &blockingStateSink{entered: make(chan struct{}), release: make(chan struct{})}
	released := make(chan string, 1)
	hook := func(string) int {
		bg.setPending()
		return 0
	}
	s := newClearGuardSessionWithScopes(t, bg, hook, sink, func(scope string) { released <- scope })
	bg.setPendingStates(agent.SubAgentFinished)
	holdClearSession(t, s)
	old := s.currentDriver()
	oldScope := s.driver.groupScope

	// Park the retired driver inside the sink, past the stale check, the way a
	// full event bridge holds an event that is already in flight.
	sink.arm()
	bg.setPendingStates(agent.SubAgentFinished, agent.SubAgentFinished)
	go old.StopTurn()
	select {
	case <-sink.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("old driver state emission did not block")
	}

	cleared := make(chan error, 1)
	go func() { cleared <- s.Handle(context.Background(), ClearConversation{}) }()
	select {
	case err := <-cleared:
		if err != nil {
			t.Fatalf("ClearConversation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("clear waited on the retired driver's blocked sink")
	}

	settled := make(chan bool, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		settled <- s.WaitRuns(ctx)
	}()
	select {
	case <-settled:
		t.Fatal("retirement finished while the old sink was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(sink.release)
	select {
	case ok := <-settled:
		if !ok {
			t.Fatal("session did not settle after the sink released")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retirement did not finish after the sink released")
	}
	select {
	case got := <-released:
		if got != oldScope {
			t.Fatalf("released scope = %q, want the retired driver's %q", got, oldScope)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retired driver's group scope was never released")
	}
	if state, held := s.currentDriver().State(); state != agent.DriverIdle || held {
		t.Fatalf("live driver state = %q held=%v, want idle", state, held)
	}
}

func TestClearConversationDropsParkedSteers(t *testing.T) {
	firstStarted := make(chan struct{})
	secondRun := make(chan []agent.Message, 1)
	var runs atomic.Int32
	runner := newRunExecutorFunc(func(ctx context.Context, conv []agent.Message) (RunResult, error) {
		if runs.Add(1) == 1 {
			close(firstStarted)
			<-ctx.Done()
			return RunResult{Conversation: conv}, ctx.Err()
		}
		secondRun <- append([]agent.Message(nil), conv...)
		return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "second-ok"})}, nil
	})
	s := testNewSession(t, Dependencies{SessionStore: newMockSessionStore(), Config: guardTestConfig(), Runner: runner})
	queue := s.runController.SteerQueue()
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "first"}); err != nil {
		t.Fatalf("SubmitPrompt first: %v", err)
	}
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("first run did not start")
	}
	queue.Add(agent.SteerMessage{Text: "stale-steer"})
	if err := s.Handle(context.Background(), NotifySteer{}); err != nil {
		t.Fatalf("NotifySteer: %v", err)
	}
	// Stop before the run finishes, as ESC does. Direct StopTurn makes the
	// ordering deterministic: the steer is parked before the run settles.
	s.currentDriver().StopTurn()
	waitClearDriverIdle(t, s)
	if got := queue.Len(); got != 1 {
		t.Fatalf("steer queue len before clear = %d, want 1", got)
	}

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if got := queue.Len(); got != 0 {
		t.Fatalf("steer queue len after clear = %d, want 0", got)
	}

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "second"}); err != nil {
		t.Fatalf("SubmitPrompt second: %v", err)
	}
	select {
	case conv := <-secondRun:
		for _, msg := range conv {
			if strings.Contains(msg.Content, "stale-steer") {
				t.Fatalf("new conversation contains parked steer: %+v", conv)
			}
		}
		found := false
		for _, msg := range conv {
			if msg.Content == "second" {
				found = true
			}
		}
		if !found {
			t.Fatalf("second prompt missing from conversation: %+v", conv)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second run did not start")
	}
}

type clearPhaseStub struct{}

func (clearPhaseStub) Submit(string, []agent.ImageBlock) {}
func (clearPhaseStub) NotifySteer()                      {}
func (clearPhaseStub) StopTurn()                         {}
func (clearPhaseStub) CancelAgent(string, bool) error    { return nil }
func (clearPhaseStub) CancelAll() error                  { return nil }

func TestClearConversationPreservesOneshotSteers(t *testing.T) {
	s := testNewSession(t, Dependencies{SessionStore: newMockSessionStore(), Config: guardTestConfig()})
	queue := s.runController.SteerQueue()
	queue.Add(agent.SteerMessage{Text: "oneshot-steer"})
	release := s.SetActivePhaseControl(clearPhaseStub{})
	defer release()

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if got := queue.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want 1 (oneshot owns it)", got)
	}
}
