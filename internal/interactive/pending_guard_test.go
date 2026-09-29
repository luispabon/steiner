package interactive

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

type stubBackground struct {
	mu      sync.Mutex
	pending []agent.PendingSubAgent
}

func (b *stubBackground) Pending() []agent.PendingSubAgent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.pending)
}

func (b *stubBackground) HasPending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) > 0
}

func (b *stubBackground) MarkDelivered(ids []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = slices.DeleteFunc(b.pending, func(p agent.PendingSubAgent) bool {
		return slices.Contains(ids, "call-"+p.AgentID)
	})
}

func (b *stubBackground) Ledger() []agent.SubAgentLedgerEntry { return nil }
func (b *stubBackground) SealBatch(string)                    {}

func (b *stubBackground) setPending(ids ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = nil
	for _, id := range ids {
		b.pending = append(b.pending, agent.PendingSubAgent{AgentID: id, AgentType: "code"})
	}
}

func waitForState(t *testing.T, s *Session, want agent.DriverState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := s.currentDriver().State(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := s.currentDriver().State()
	t.Fatalf("driver state = %q, want %q", got, want)
}

func newWaitingSession(t *testing.T, runner runExecutor, bg *stubBackground) *Session {
	t.Helper()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig(), Runner: runner, Background: bg})
	bg.setPending("a")
	submitAndWait(t, s, "hi", nil)
	waitForState(t, s, agent.DriverWaiting)
	return s
}

func TestGuardedActionsRefusedWhilePending(t *testing.T) {
	t.Parallel()
	bg := &stubBackground{}
	runner := newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		return RunResult{Conversation: append(slices.Clone(conv), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
	})
	s := newWaitingSession(t, runner, bg)
	origID := s.SessionID()

	actions := []Action{
		LoadSession{SessionID: "other"},
		ForkSession{},
		ForkSavedSession{SessionID: "other"},
		ClearConversation{},
		RotateSession{},
		RotateSessionWithGroup{Group: "g"},
	}
	for _, a := range actions {
		err := s.Handle(context.Background(), a)
		if err == nil || !strings.Contains(err.Error(), "1 sub-agents still running; wait for them or stop them first") {
			t.Errorf("Handle(%T) = %v, want pending refusal", a, err)
		}
	}
	if s.SessionID() != origID {
		t.Errorf("session ID changed to %q while pending", s.SessionID())
	}

	bg.setPending()
	for _, a := range actions {
		if err := s.Handle(context.Background(), a); err != nil {
			t.Errorf("Handle(%T) without pending = %v", a, err)
		}
	}
}

type blockingLoadStore struct {
	sessionStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingLoadStore) Load(id string) (session.Session, error) {
	close(s.entered)
	<-s.release
	return s.sessionStore.Load(id)
}

func TestLoadSessionByIDPendingTransitionBeforeReplacement(t *testing.T) {
	t.Parallel()
	base := newMockSessionStore()
	base.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	store := &blockingLoadStore{
		sessionStore: base,
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	bg := &stubBackground{}
	s := testNewSession(t, Dependencies{SessionStore: store, Background: bg})
	origID := s.SessionID()
	origDriver := s.currentDriver()
	origConversation := s.Conversation()

	loaded := make(chan error, 1)
	go func() { loaded <- s.LoadSessionByID(context.Background(), "other") }()
	<-store.entered
	bg.setPending("late")
	close(store.release)

	err := <-loaded
	if err == nil || !strings.Contains(err.Error(), "1 sub-agents still running; wait for them or stop them first") {
		t.Fatalf("LoadSessionByID() = %v, want pending refusal", err)
	}
	if got := s.SessionID(); got != origID {
		t.Fatalf("session ID changed to %q while pending", got)
	}
	if got := s.currentDriver(); got != origDriver {
		t.Fatal("driver changed while pending")
	}
	if got := s.Conversation(); !reflect.DeepEqual(got, origConversation) {
		t.Fatalf("conversation changed while pending: got %#v, want %#v", got, origConversation)
	}
}

func TestSubmitSelectedBeforeLoadCannotAdmitToRetiredDriver(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	runner := newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		return RunResult{Conversation: conv}, nil
	})
	s := testNewSession(t, Dependencies{SessionStore: store, Runner: runner})
	selected := make(chan struct{})
	release := make(chan struct{})
	s.mu.Lock()
	s.submitSelectionHook = func() {
		close(selected)
		<-release
	}
	s.mu.Unlock()

	submitted := make(chan struct{})
	go func() {
		s.submitPrompt(context.Background(), "late prompt", nil)
		close(submitted)
	}()
	<-selected

	loaded := make(chan error, 1)
	go func() { loaded <- s.LoadSessionByID(context.Background(), "other") }()
	err := <-loaded
	if err != nil {
		t.Fatalf("LoadSessionByID() = %v, want load to replace before admission", err)
	}
	close(release)
	<-submitted
	if got := s.SessionID(); got != "other" {
		t.Fatalf("session ID = %q, want loaded session", got)
	}
	if got := s.Conversation(); len(got) != 1 || got[0].Content != "other" {
		t.Fatalf("conversation admitted late prompt: %#v", got)
	}
}

func TestSubmitAdmissionMakesConcurrentLoadRefuse(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	runner := newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		return RunResult{Conversation: conv}, nil
	})
	s := testNewSession(t, Dependencies{SessionStore: store, Runner: runner})
	admitted := make(chan struct{})
	release := make(chan struct{})
	s.mu.Lock()
	s.submitAdmissionHook = func() {
		close(admitted)
		<-release
	}
	s.mu.Unlock()

	submitted := make(chan struct{})
	go func() {
		s.submitPrompt(context.Background(), "prompt", nil)
		close(submitted)
	}()
	<-admitted

	err := s.LoadSessionByID(context.Background(), "other")
	if err == nil || !strings.Contains(err.Error(), errRunInProgress.Error()) {
		t.Fatalf("LoadSessionByID() = %v, want admission refusal", err)
	}
	close(release)
	<-submitted
}

func TestLoadSessionByIDRefusedWhilePending(t *testing.T) {
	t.Parallel()
	bg := &stubBackground{}
	runner := newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		return RunResult{Conversation: append(slices.Clone(conv), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
	})
	s := newWaitingSession(t, runner, bg)
	origID := s.SessionID()
	origDriver := s.currentDriver()
	origConversation := s.Conversation()

	err := s.LoadSessionByID(context.Background(), "other")
	if err == nil || !strings.Contains(err.Error(), "1 sub-agents still running; wait for them or stop them first") {
		t.Fatalf("LoadSessionByID() = %v, want pending refusal", err)
	}
	if got := s.SessionID(); got != origID {
		t.Fatalf("session ID changed to %q while pending", got)
	}
	if got := s.currentDriver(); got != origDriver {
		t.Fatal("driver changed while pending")
	}
	if got := s.Conversation(); !reflect.DeepEqual(got, origConversation) {
		t.Fatalf("conversation changed while pending: got %#v, want %#v", got, origConversation)
	}
}

func TestCompactionWhileWaitingRunsAndCompletionFollows(t *testing.T) {
	t.Parallel()
	bg := &stubBackground{}
	var mu sync.Mutex
	var runs [][]agent.Message
	runner := &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		mu.Lock()
		runs = append(runs, slices.Clone(in.Conversation))
		mu.Unlock()
		return withAnswer(in, "ok"), nil
	}}
	compacted := make(chan struct{})
	release := make(chan struct{})
	execer := &compactingRunner{inputRunner: runner, compact: func() []agent.Message {
		close(compacted)
		<-release
		return []agent.Message{{Role: agent.MessageRoleSummary, Content: "summary"}}
	}}
	s := newWaitingSession(t, execer, bg)
	s.SetConversation(twoTurnConversation())

	if err := s.Handle(context.Background(), TriggerManualCompaction{}); err != nil {
		t.Fatalf("compaction while waiting: %v", err)
	}
	<-compacted
	s.currentDriver().DeliverCompletions([]agent.SubAgentCompletion{{
		Seq: 1, ParentCallID: "call-a", AgentID: "a", AgentType: "code", Status: "complete", Body: `{"output":"done"}`,
	}})
	mu.Lock()
	n := len(runs)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("runs during compaction = %d, want 1", n)
	}
	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n = len(runs)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	waitSettled(t, s)

	conv := s.Conversation()
	if conv[0].Content != "summary" {
		t.Fatalf("conversation[0] = %q, want summary", conv[0].Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(runs) != 2 || runs[1][0].Content != "summary" {
		t.Fatalf("completion run did not follow the compaction: %d runs", len(runs))
	}
}

type compactingRunner struct {
	*inputRunner
	compact func() []agent.Message
}

func (c *compactingRunner) Compact(context.Context, []agent.Message, []provider.ToolSpec, string) ([]agent.Message, error) {
	return c.compact(), nil
}

func TestCompactionWhileGeneratingRefused(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{Config: guardTestConfig()})
	release := startBlockedRun(t, s)
	defer close(release)
	err := s.Handle(context.Background(), TriggerManualCompaction{})
	if !errors.Is(err, errRunInProgress) {
		t.Fatalf("compaction while generating = %v, want errRunInProgress", err)
	}
}
