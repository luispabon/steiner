package interactive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/session"
)

func guardTestConfig() config.Config {
	return config.Config{Models: config.ModelsConfig{
		Effective:   config.EffectiveModelAssignments{DefaultModel: "test", ActiveOrchestratorModel: "test"},
		Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
	}}
}

func lineageOf(msgs ...agent.Message) agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: msgs}},
		NextGenerationID: 2,
	}
}

func userMsg(text string) agent.Message {
	return agent.Message{Role: agent.MessageRoleUser, Content: text}
}

// startBlockedRun dispatches a prompt whose runner blocks until release is closed.
func startBlockedRun(t *testing.T, s *Session) (release chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release = make(chan struct{})
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		close(started)
		<-release
		return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "old answer"})}, nil
	}))
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hi"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	<-started
	return release
}

func TestSessionMutationsRefusedDuringRun(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	origID := s.SessionID()
	release := startBlockedRun(t, s)

	actions := []Action{
		LoadSession{SessionID: "other"},
		ForkSession{},
		ForkSavedSession{SessionID: "other"},
		TriggerManualCompaction{},
	}
	for _, a := range actions {
		err := s.Handle(context.Background(), a)
		if !errors.Is(err, errRunInProgress) {
			t.Errorf("Handle(%T) = %v, want errRunInProgress", a, err)
		}
	}
	if s.SessionID() != origID {
		t.Errorf("session ID changed to %q during run", s.SessionID())
	}
	close(release)
	if !s.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns failed")
	}
	if err := s.Handle(context.Background(), LoadSession{SessionID: "other"}); err != nil {
		t.Fatalf("LoadSession after run finished: %v", err)
	}
	if s.SessionID() != "other" {
		t.Errorf("session ID = %q, want other", s.SessionID())
	}
}

func TestBusyRotationRefusedUntilRunSettles(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	startID := s.SessionID()
	release := startBlockedRun(t, s)
	originalDriver := s.currentDriver()
	originalConversation := s.Conversation()

	if err := s.Handle(context.Background(), RotateSession{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("RotateSession: %v, want errRunInProgress", err)
	}
	if s.SessionID() != startID || s.currentDriver() != originalDriver {
		t.Fatal("refused rotation changed session identity or driver")
	}
	if got := s.Conversation(); !reflect.DeepEqual(got, originalConversation) {
		t.Fatalf("conversation after refused rotation = %+v, want %+v", got, originalConversation)
	}
	close(release)
	waitSettled(t, s)
	completedConversation := s.Conversation()
	saved, ok := store.savedSessions[startID]
	if !ok {
		t.Fatal("completed run was not saved under original session")
	}
	if got := saved.Lineage.FullMessages(); !reflect.DeepEqual(got, completedConversation) {
		t.Fatalf("saved original history = %+v, want completed run %+v", got, completedConversation)
	}
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession after settlement: %v", err)
	}
	if s.SessionID() == startID {
		t.Fatal("session ID did not rotate after settlement")
	}
	if got := s.Conversation(); !reflect.DeepEqual(got, completedConversation) {
		t.Fatalf("successor history = %+v, want retained conversation %+v", got, completedConversation)
	}
	if got := store.savedSessions[startID].Lineage.FullMessages(); !reflect.DeepEqual(got, completedConversation) {
		t.Fatalf("original saved history after rotation = %+v, want %+v", got, completedConversation)
	}
}

func TestBusyConversationReplacementRefusedUntilRunSettles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action func(*Session) error
	}{
		{name: "clear", action: func(s *Session) error { return s.Handle(context.Background(), ClearConversation{}) }},
		{name: "rotate", action: func(s *Session) error { return s.Handle(context.Background(), RotateSession{}) }},
		{name: "set conversation", action: func(s *Session) error { s.SetConversation([]agent.Message{userMsg("replacement")}); return nil }},
		{name: "load", action: func(s *Session) error { return s.Handle(context.Background(), LoadSession{SessionID: "other"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockSessionStore()
			store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
			s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
			seedConversation(s, []agent.Message{userMsg("seed")}, lineageOf(userMsg("seed")))
			id, driver := s.SessionID(), s.currentDriver()
			release := startBlockedRun(t, s)
			conversation := s.Conversation()
			if err := tc.action(s); !errors.Is(err, errRunInProgress) && tc.name != "set conversation" {
				t.Fatalf("replacement error = %v, want errRunInProgress", err)
			}
			if s.SessionID() != id || s.currentDriver() != driver {
				t.Fatal("refused replacement changed identity or driver")
			}
			if got := s.Conversation(); !reflect.DeepEqual(got, conversation) {
				t.Fatalf("conversation changed: got %+v, want %+v", got, conversation)
			}
			close(release)
			waitSettled(t, s)
			if err := tc.action(s); err != nil {
				t.Fatalf("replacement after settlement: %v", err)
			}
			if tc.name == "set conversation" {
				if s.currentDriver() == driver {
					t.Fatal("SetConversation after settlement did not replace driver")
				}
				want := []agent.Message{userMsg("replacement")}
				if got := s.Conversation(); !reflect.DeepEqual(got, want) {
					t.Fatalf("SetConversation after settlement = %+v, want %+v", got, want)
				}
			}
		})
	}
}

func TestClearConversationResetsLineage(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	seedConversation(s, []agent.Message{userMsg("secret")}, lineageOf(userMsg("secret")))
	oldID := s.SessionID()
	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession before clear: %v", err)
	}

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if s.SessionID() == oldID {
		t.Fatal("session ID did not rotate")
	}
	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if got := store.savedSessions[oldID].Lineage.FullMessages(); len(got) != 1 || got[0].Content != "secret" {
		t.Fatalf("original saved lineage = %+v, want secret", got)
	}
	saved := store.savedSessions[s.SessionID()]
	if got := saved.Lineage.FullMessages(); len(got) != 0 {
		t.Fatalf("saved lineage = %+v, want empty", got)
	}
	if err := s.Handle(context.Background(), ForkSession{}); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if got := s.lineage.FullMessages(); len(got) != 0 {
		t.Fatalf("fork lineage = %+v, want empty", got)
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("fork conversation = %+v, want empty", got)
	}
}

func TestActiveRunControllerReleaseRequiresOwnerToken(t *testing.T) {
	t.Parallel()
	c := NewActiveRunController()
	cancelledA, cancelledB := false, false
	tokA := c.Set(func() { cancelledA = true })
	tokB := c.Set(func() { cancelledB = true })
	if tokA == tokB {
		t.Fatal("tokens must differ")
	}
	c.SteerQueue().Add(agent.SteerMessage{Text: "keep"})
	c.Release(tokA)
	if !c.HasCancel() {
		t.Fatal("stale Release removed the newer cancel func")
	}
	if len(c.SteerQueue().Drain()) != 1 {
		t.Fatal("stale Release dropped steer queue")
	}
	c.Interrupt()
	if cancelledA || !cancelledB {
		t.Fatalf("cancelled A=%v B=%v, want only B", cancelledA, cancelledB)
	}
	c.Release(tokB)
	if c.HasCancel() {
		t.Fatal("owner Release did not release cancel")
	}
}

type finalGroupSnapshotBarrier struct {
	mu          sync.Mutex
	ledger      agent.DelegationGroupLedger
	block       bool
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (b *finalGroupSnapshotBarrier) arm(ledger agent.DelegationGroupLedger) {
	b.mu.Lock()
	b.ledger = ledger.Clone()
	b.block = true
	b.mu.Unlock()
}

func (b *finalGroupSnapshotBarrier) snapshot() agent.DelegationGroupLedger {
	b.mu.Lock()
	ledger := b.ledger.Clone()
	block := b.block
	b.block = false
	b.mu.Unlock()
	if block {
		close(b.entered)
		<-b.release
	}
	return ledger
}

func (b *finalGroupSnapshotBarrier) releaseSave() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func TestRotationRefusedThroughFinalGroupSnapshotSave(t *testing.T) {
	barrier := &finalGroupSnapshotBarrier{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	store := newMockSessionStore()
	var scopeMu sync.Mutex
	nextScope := 0
	newScope := func(agent.DelegationGroupLedger) string {
		scopeMu.Lock()
		defer scopeMu.Unlock()
		nextScope++
		return string(rune('a' + nextScope - 1))
	}
	s := testNewSession(t, Dependencies{
		SessionStore:        store,
		NewGroupScope:       newScope,
		SnapshotGroupLedger: func(string) agent.DelegationGroupLedger { return barrier.snapshot() },
		Config:              guardTestConfig(),
	})
	t.Cleanup(barrier.releaseSave)
	startID := s.SessionID()
	startDriver := s.currentDriver()
	startScope := s.driver.groupScope
	finalLedger := agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion, Names: []string{"final-name"}}
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		barrier.arm(finalLedger)
		return withAnswer(in, "final answer"), nil
	}})
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "final prompt"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("final group snapshot did not reach barrier")
	}

	wantID, wantDriver, wantScope, wantHistory := s.SessionID(), s.currentDriver(), s.driver.groupScope, s.Conversation()
	if wantID != startID || wantDriver != startDriver || wantScope != startScope {
		t.Fatal("final save changed identity, driver, or group scope before rotation")
	}
	if err := s.Handle(context.Background(), RotateSession{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("RotateSession during final group save: %v, want errRunInProgress", err)
	}
	gotID, gotDriver, gotScope, gotHistory := s.SessionID(), s.currentDriver(), s.driver.groupScope, s.Conversation()
	if gotID != wantID || gotDriver != wantDriver || gotScope != wantScope || !reflect.DeepEqual(gotHistory, wantHistory) {
		t.Fatal("refused rotation changed identity, driver, history, or group scope")
	}

	barrier.releaseSave()
	waitSettled(t, s)
	completedHistory := s.Conversation()
	if len(completedHistory) != 2 || completedHistory[len(completedHistory)-1].Content != "final answer" {
		t.Fatalf("completed history = %+v, want final answer", completedHistory)
	}
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession after final group save: %v", err)
	}
	if s.SessionID() == startID || s.driver.groupScope == startScope {
		t.Fatalf("successor identity/scope = %q/%q, want both rotated", s.SessionID(), s.driver.groupScope)
	}
	if got := s.Conversation(); !reflect.DeepEqual(got, completedHistory) {
		t.Fatalf("successor history = %+v, want %+v", got, completedHistory)
	}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, finalLedger.Names) {
		t.Fatalf("successor group names = %v, want %v", got, finalLedger.Names)
	}
}

func TestHandoffClearRotateStillSavesFinalTurnUnderOriginalSession(t *testing.T) {
	t.Parallel()
	// Multi-line and longer than 80 characters: the title must be normalised.
	handoffPrompt := "plan it\n\n\twith detail: " + strings.Repeat("more words ", 12)
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	startID := s.SessionID()
	started := make(chan struct{})
	release := make(chan struct{})
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message) (RunResult, error) {
		close(started)
		<-release // blocked in the workflow handoff responder
		final := append(append([]agent.Message{}, conv...), agent.Message{Role: agent.MessageRoleAssistant, Content: "final plan turn"})
		return RunResult{Conversation: final}, nil
	}))
	s.submitPrompt(context.Background(), handoffPrompt, nil)
	<-started

	// A busy handoff cannot clear or rotate before its result settles.
	if err := s.Handle(context.Background(), ClearConversation{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("ClearConversation: %v, want errRunInProgress", err)
	}
	if err := s.Handle(context.Background(), RotateSession{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("RotateSession: %v, want errRunInProgress", err)
	}
	if s.SessionID() != startID || len(s.Conversation()) == 0 {
		t.Fatal("refused replacement changed the live session")
	}
	close(release)
	waitSettled(t, s)
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation after settlement: %v", err)
	}
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession after settlement: %v", err)
	}
	newID := s.SessionID()
	if newID == startID {
		t.Fatal("session ID did not rotate")
	}

	saved, ok := store.savedSessions[startID]
	if !ok {
		t.Fatal("final run was not saved under the original session ID")
	}
	msgs := saved.Lineage.FullMessages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Content != "final plan turn" {
		t.Fatalf("saved lineage = %+v, want it to end with the final turn", msgs)
	}
	if want := session.TitleFromPrompt(handoffPrompt); saved.Title != want || len([]rune(want)) != 80 {
		t.Errorf("saved title = %q, want %q (80 runes, whitespace collapsed)", saved.Title, want)
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("live conversation = %+v, want untouched (empty)", got)
	}
	if got := s.lineage.FullMessages(); len(got) != 0 {
		t.Fatalf("live lineage = %+v, want untouched (empty)", got)
	}
	if _, ok := store.savedSessions[newID]; ok {
		t.Fatal("run result leaked into the rotated session")
	}
}
