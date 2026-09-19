package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

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
func startBlockedRun(t *testing.T, s *Session) (started chan struct{}, release chan struct{}) {
	t.Helper()
	started = make(chan struct{})
	release = make(chan struct{})
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message, _ []string) (RunResult, error) {
		close(started)
		<-release
		return RunResult{Conversation: append(conv, agent.Message{Role: agent.MessageRoleAssistant, Content: "old answer"})}, nil
	}))
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hi"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	<-started
	return started, release
}

func TestSessionMutationsRefusedDuringRun(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Lineage: lineageOf(userMsg("other"))}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	origID := s.SessionID()
	_, release := startBlockedRun(t, s)

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

func TestApplyRunResultSkipsWhenSessionChanged(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["new"] = session.Session{ID: "new", Lineage: lineageOf(userMsg("new convo"))}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	startID := s.SessionID()
	// Bypass the dispatch guard: no run is registered, so loadSession succeeds.
	if err := s.loadSession(context.Background(), "new"); err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if s.sessionChanged(startID) != true {
		t.Fatal("sessionChanged = false after load")
	}
	if s.applyRunResult(startID, RunResult{Conversation: []agent.Message{userMsg("old convo")}}) {
		t.Fatal("applyRunResult applied a stale result")
	}
	conv := s.Conversation()
	if len(conv) != 1 || conv[0].Content != "new convo" {
		t.Fatalf("conversation = %+v, want the loaded session's", conv)
	}
	if got := s.lineage.FullMessages(); len(got) != 1 || got[0].Content != "new convo" {
		t.Fatalf("lineage = %+v, want the loaded session's", got)
	}
	if _, ok := store.savedSessions["new"]; ok {
		t.Fatal("new session was saved by the stale run")
	}
}

func TestSubmitPromptDoesNotSaveUnderChangedSession(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["new"] = session.Session{ID: "new", Lineage: lineageOf(userMsg("new convo"))}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	startID := s.SessionID()
	started := make(chan struct{})
	release := make(chan struct{})
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message, _ []string) (RunResult, error) {
		close(started)
		<-release
		return RunResult{Conversation: conv}, nil
	}))
	done := make(chan struct{})
	go func() { defer close(done); s.submitPrompt(context.Background(), "hi", nil) }()
	<-started
	if err := s.loadSession(context.Background(), "new"); err != nil { // guard bypassed: not via dispatch
		// beginRun was not called by submitPrompt directly, so this succeeds.
		t.Fatalf("loadSession: %v", err)
	}
	close(release)
	<-done
	if _, ok := store.savedSessions["new"]; ok {
		t.Fatal("stale run saved under the new session ID")
	}
	if _, ok := store.savedSessions[startID]; !ok {
		t.Fatal("stale run's result was not saved under its original session ID")
	}
	if conv := s.Conversation(); len(conv) != 1 || conv[0].Content != "new convo" {
		t.Fatalf("conversation = %+v", conv)
	}
}

func TestClearConversationResetsLineage(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	s.mu.Lock()
	s.conversation = []agent.Message{userMsg("secret")}
	s.lineage = lineageOf(userMsg("secret"))
	s.mu.Unlock()

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession: %v", err)
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

func TestActiveRunControllerClearRequiresOwnerToken(t *testing.T) {
	t.Parallel()
	c := NewActiveRunController()
	cancelledA, cancelledB := false, false
	tokA := c.Set(func() { cancelledA = true })
	tokB := c.Set(func() { cancelledB = true })
	if tokA == tokB {
		t.Fatal("tokens must differ")
	}
	c.SteerQueue().Add(agent.SteerMessage{Text: "keep"})
	c.Clear(tokA)
	if !c.HasCancel() {
		t.Fatal("stale Clear removed the newer cancel func")
	}
	if len(c.SteerQueue().Drain()) != 1 {
		t.Fatal("stale Clear dropped steer queue")
	}
	c.Interrupt()
	if cancelledA || !cancelledB {
		t.Fatalf("cancelled A=%v B=%v, want only B", cancelledA, cancelledB)
	}
	c.Clear(tokB)
	if c.HasCancel() {
		t.Fatal("owner Clear did not release cancel")
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
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conv []agent.Message, _ []string) (RunResult, error) {
		close(started)
		<-release // blocked in the workflow handoff responder
		final := append(append([]agent.Message{}, conv...), agent.Message{Role: agent.MessageRoleAssistant, Content: "final plan turn"})
		return RunResult{Conversation: final}, nil
	}))
	done := make(chan struct{})
	go func() { defer close(done); s.submitPrompt(context.Background(), handoffPrompt, nil) }()
	<-started

	// The TUI's accept path: clear the conversation, then rotate the session.
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	newID := s.SessionID()
	if newID == startID {
		t.Fatal("session ID did not rotate")
	}
	close(release)
	<-done

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
