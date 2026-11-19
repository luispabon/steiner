package interactive

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// lastRequestFixture returns a main request recorded at at for cacheKey.
func lastRequestFixture(at time.Time, cacheKey string) agent.LastMainRequest {
	return agent.LastMainRequest{At: at, ProviderAlias: "local", BackendModelID: "main-model", PromptCacheKey: cacheKey}
}

// TestRestoreLastMainRequestValidatesStoredRecord proves a persisted request is
// dropped when it cannot describe a real request for this session, and kept
// when it can.
func TestRestoreLastMainRequestValidatesStoredRecord(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	const cacheKey = "session-cache-key"
	valid := func() *session.LastMainRequest {
		return &session.LastMainRequest{
			At:             now.Add(-time.Minute),
			ProviderAlias:  "local",
			BackendModelID: "main-model",
			PromptCacheKey: cacheKey,
		}
	}
	tests := []struct {
		name   string
		record *session.LastMainRequest
		want   agent.LastMainRequest
	}{
		{name: "absent"},
		{name: "valid", record: valid(), want: lastRequestFixture(now.Add(-time.Minute), cacheKey)},
		{name: "zero start time", record: func() *session.LastMainRequest {
			r := valid()
			r.At = time.Time{}
			return r
		}()},
		{name: "future start time", record: func() *session.LastMainRequest {
			r := valid()
			r.At = now.Add(time.Second)
			return r
		}()},
		{name: "missing provider", record: func() *session.LastMainRequest {
			r := valid()
			r.ProviderAlias = ""
			return r
		}()},
		{name: "missing backend model", record: func() *session.LastMainRequest {
			r := valid()
			r.BackendModelID = ""
			return r
		}()},
		{name: "missing cache key", record: func() *session.LastMainRequest {
			r := valid()
			r.PromptCacheKey = ""
			return r
		}()},
		{name: "other cache key", record: func() *session.LastMainRequest {
			r := valid()
			r.PromptCacheKey = "another-session-key"
			return r
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := restoreLastMainRequest(tt.record, cacheKey, now); got != tt.want {
				t.Fatalf("restoreLastMainRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestSessionPersistsLastMainRequestAcrossResume runs a turn that records a
// main request, then resumes the saved session in a new session and proves the
// next run is seeded with it.
func TestSessionPersistsLastMainRequestAcrossResume(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	var firstReq agent.LastMainRequest
	first := testNewSession(t, Dependencies{
		SessionStore: store,
		Runner: &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
			conv := append(append([]agent.Message(nil), in.Conversation...), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
			return RunResult{Conversation: conv, LastMainRequest: firstReq}, nil
		}},
	})
	firstReq = lastRequestFixture(time.Now().Add(-time.Minute), first.PromptCacheKey())
	submitAndWait(t, first, "first", nil)
	id := first.SessionID()

	stored, ok := store.savedSessions[id]
	if !ok {
		t.Fatalf("session %q was not saved", id)
	}
	if stored.LastMainRequest == nil {
		t.Fatal("saved session carries no last main request")
	}
	if got := stored.LastMainRequest; *got != (session.LastMainRequest{
		At:             firstReq.At,
		ProviderAlias:  firstReq.ProviderAlias,
		BackendModelID: firstReq.BackendModelID,
		PromptCacheKey: firstReq.PromptCacheKey,
	}) {
		t.Fatalf("saved last main request = %+v, want %+v", *got, firstReq)
	}

	var resumed []RunInput
	second := testNewSession(t, Dependencies{
		SessionStore: store,
		Runner: &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
			resumed = append(resumed, in)
			conv := append(append([]agent.Message(nil), in.Conversation...), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
			return RunResult{Conversation: conv, LastMainRequest: in.LastMainRequest}, nil
		}},
	})
	if err := second.Handle(context.Background(), LoadSession{SessionID: id}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}
	submitAndWait(t, second, "second", nil)
	if len(resumed) != 1 {
		t.Fatalf("resumed runs = %d, want 1", len(resumed))
	}
	if got := resumed[0].LastMainRequest; got != firstReq {
		t.Fatalf("resumed run LastMainRequest = %+v, want the persisted %+v", got, firstReq)
	}
}

// TestLoadDropsLastMainRequestForAnotherCacheKey proves a stored request that
// names another prompt cache key is discarded rather than seeded.
func TestLoadDropsLastMainRequestForAnotherCacheKey(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["stale"] = session.Session{
		ID:    "stale",
		Model: "test-model",
		Lineage: agent.ConversationLineage{Generations: []agent.ConversationGeneration{{
			ID:       1,
			Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "earlier"}},
		}}, NextGenerationID: 2},
		LastMainRequest: &session.LastMainRequest{
			At:             time.Now().Add(-time.Minute),
			ProviderAlias:  "local",
			BackendModelID: "main-model",
			PromptCacheKey: "another-session-key",
		},
	}
	var inputs []RunInput
	s := testNewSession(t, Dependencies{
		SessionStore: store,
		Runner: &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
			inputs = append(inputs, in)
			conv := append(append([]agent.Message(nil), in.Conversation...), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
			return RunResult{Conversation: conv}, nil
		}},
	})
	if err := s.Handle(context.Background(), LoadSession{SessionID: "stale"}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}
	submitAndWait(t, s, "next", nil)
	if len(inputs) != 1 {
		t.Fatalf("runs = %d, want 1", len(inputs))
	}
	if got := inputs[0].LastMainRequest; got != (agent.LastMainRequest{}) {
		t.Fatalf("run LastMainRequest = %+v, want none for a stale cache key", got)
	}
}

// TestRetiredDriverSavePersistsLastMainRequest proves the retired-driver save
// path writes the snapshot's last main request under the retired identity.
func TestRetiredDriverSavePersistsLastMainRequest(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	conversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "earlier"}}
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{ID: 1, Messages: conversation}}, NextGenerationID: 2}
	store.loadedSessions["retired"] = session.Session{ID: "retired", Model: "test-model", Lineage: lineage}
	s := testNewSession(t, Dependencies{SessionStore: store})
	req := lastRequestFixture(time.Now().Add(-time.Minute), "retired")
	if err := s.saveSnapshotAs(runSessionMeta{id: "retired", cacheKey: "retired", modelID: "test-model"}, agent.DriverSnapshot{Lineage: lineage, LastMainRequest: req}); err != nil {
		t.Fatalf("saveSnapshotAs() = %v, want nil", err)
	}
	saved, err := store.Load("retired")
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if saved.LastMainRequest == nil {
		t.Fatal("retired save dropped the last main request")
	}
	if got := saved.LastMainRequest; *got != (session.LastMainRequest{At: req.At, ProviderAlias: req.ProviderAlias, BackendModelID: req.BackendModelID, PromptCacheKey: req.PromptCacheKey}) {
		t.Fatalf("saved last main request = %+v, want %+v", *got, req)
	}
}

// TestSnapshotUnchangedComparesLastMainRequest proves a snapshot that only
// changes the last main request is still saved.
func TestSnapshotUnchangedComparesLastMainRequest(t *testing.T) {
	t.Parallel()
	req := lastRequestFixture(time.Now().Add(-time.Minute), "cache-key")
	before := &agent.DriverSnapshot{LastMainRequest: req}
	if !snapshotUnchanged(before, *before) {
		t.Fatal("an identical snapshot reported as changed")
	}
	changed := *before
	changed.LastMainRequest = lastRequestFixture(req.At.Add(time.Second), "cache-key")
	if snapshotUnchanged(before, changed) {
		t.Fatal("a last-main-request-only change on an empty conversation reported as unchanged")
	}
}

// TestClearAndRotateResetLastMainRequest proves both identity changes drop the
// carried request, which no longer describes the session's cache key.
func TestClearAndRotateResetLastMainRequest(t *testing.T) {
	t.Parallel()
	for _, action := range []Action{ClearConversation{}, RotateSession{}} {
		t.Run(actionName(action), func(t *testing.T) {
			t.Parallel()
			store := newMockSessionStore()
			var req agent.LastMainRequest
			s := testNewSession(t, Dependencies{
				SessionStore: store,
				Runner: &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
					conv := append(append([]agent.Message(nil), in.Conversation...), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
					return RunResult{Conversation: conv, LastMainRequest: req}, nil
				}},
			})
			req = lastRequestFixture(time.Now().Add(-time.Minute), s.PromptCacheKey())
			submitAndWait(t, s, "first", nil)

			if err := s.Handle(context.Background(), action); err != nil {
				t.Fatalf("Handle(%T) = %v, want nil", action, err)
			}
			if got := s.currentDriver().LastMainRequest(); got != (agent.LastMainRequest{}) {
				t.Fatalf("driver after %T carries %+v, want none", action, got)
			}
			if err := s.saveSession(); err != nil {
				t.Fatalf("saveSession() = %v", err)
			}
			saved, err := store.Load(s.SessionID())
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			if saved.LastMainRequest != nil {
				t.Fatalf("saved last main request after %T = %+v, want none", action, *saved.LastMainRequest)
			}
		})
	}
}

func actionName(action Action) string {
	switch action.(type) {
	case ClearConversation:
		return "clear"
	case RotateSession:
		return "rotate"
	default:
		return "unknown"
	}
}

// TestForkSessionCopiesLastMainRequest proves a fork keeps the parent's request
// so the fork's first resume can still judge the cache age.
func TestForkSessionCopiesLastMainRequest(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store})
	req := lastRequestFixture(time.Now().Add(-time.Minute), "fork-key")
	s.mu.Lock()
	s.lastMainRequest = req
	s.mu.Unlock()
	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v, want nil", err)
	}
	saved, err := store.Load(s.SessionID())
	if err != nil {
		t.Fatalf("Load(fork) = %v", err)
	}
	if saved.LastMainRequest == nil {
		t.Fatal("fork dropped the parent's last main request")
	}
	if got := saved.LastMainRequest; *got != (session.LastMainRequest{At: req.At, ProviderAlias: req.ProviderAlias, BackendModelID: req.BackendModelID, PromptCacheKey: req.PromptCacheKey}) {
		t.Fatalf("fork last main request = %+v, want %+v", *got, req)
	}
}
