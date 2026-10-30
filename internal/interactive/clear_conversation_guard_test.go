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
	var scopeMu sync.Mutex
	nextScope := 0
	return testNewSession(t, Dependencies{
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
			return fmt.Sprintf("scope-%d", nextScope), func() {}
		},
		ClearFinishedSubAgents: hook,
	})
}

func waitForClearSession(t *testing.T, s *Session) {
	t.Helper()
	submitAndWait(t, s, "hi", nil)
	waitForState(t, s, agent.DriverWaiting)
}

// holdClearSession waits for a waiting driver and then holds its completions,
// the state in which clear may discard finished sub-agents.
func holdClearSession(t *testing.T, s *Session) {
	t.Helper()
	waitForClearSession(t, s)
	s.currentDriver().StopTurn()
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
}
