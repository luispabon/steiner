package interactive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

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
	waitForClearSession(t, s)

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

func TestClearConversationRefusesRunningOrQueuedBeforeHook(t *testing.T) {
	for _, tc := range []struct {
		state agent.SubAgentState
		want  string
	}{
		{state: agent.SubAgentRunning, want: "1 running"},
		{state: agent.SubAgentQueued, want: "1 queued"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			bg := &stubBackground{}
			hookCalled := false
			hook := func(string) int {
				hookCalled = true
				return 0
			}
			s := newClearGuardSession(t, bg, hook)
			bg.setPendingStates(tc.state)
			waitForClearSession(t, s)

			oldID, oldDriver, oldConv := s.SessionID(), s.currentDriver(), s.Conversation()
			oldState, oldHeld := oldDriver.State()
			err := s.Handle(context.Background(), ClearConversation{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ClearConversation = %v, want refusal mentioning %q", err, tc.want)
			}
			if hookCalled {
				t.Fatal("clear hook ran with non-discardable work")
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

func TestClearConversationScopeHookRefusalKeepsSession(t *testing.T) {
	bg := &stubBackground{}
	hookCalled := false
	hook := func(string) int {
		hookCalled = true
		return 1
	}
	s := newClearGuardSession(t, bg, hook)
	bg.setPendingStates(agent.SubAgentFinished)
	waitForClearSession(t, s)

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
}

func TestClearConversationBusyRefusedBeforeHook(t *testing.T) {
	bg := &stubBackground{}
	hookCalled := false
	hook := func(string) int {
		hookCalled = true
		return 0
	}
	s := newClearGuardSession(t, bg, hook)
	release := startBlockedRun(t, s)
	defer close(release)

	if err := s.Handle(context.Background(), ClearConversation{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("ClearConversation during run = %v, want errRunInProgress", err)
	}
	if hookCalled {
		t.Fatal("clear hook ran while the driver was busy")
	}
}
