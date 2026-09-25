package interactive

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/session"
)

type bindCall struct {
	sessionID string
	minNext   int
}

type copyCall struct {
	fromID string
	toID   string
}

type fakeImageSessionStore struct {
	binds   []bindCall
	copies  []copyCall
	bindErr error
	copyErr error
}

func (f *fakeImageSessionStore) BindSession(sessionID string, minNext int) error {
	f.binds = append(f.binds, bindCall{sessionID: sessionID, minNext: minNext})
	return f.bindErr
}

func (f *fakeImageSessionStore) CopySession(fromID, toID string) error {
	f.copies = append(f.copies, copyCall{fromID: fromID, toID: toID})
	return f.copyErr
}

func (f *fakeImageSessionStore) lastBind() bindCall {
	if len(f.binds) == 0 {
		return bindCall{}
	}
	return f.binds[len(f.binds)-1]
}

func TestNewSessionBindsImageStore(t *testing.T) {
	t.Parallel()
	store := &fakeImageSessionStore{}
	s := testNewSession(t, Dependencies{ImageStore: store, Config: config.Config{}})

	if len(store.binds) != 1 {
		t.Fatalf("BindSession calls = %d, want 1", len(store.binds))
	}
	got := store.binds[0]
	if got.sessionID != s.SessionID() || got.minNext != 1 {
		t.Errorf("bind = %+v, want session %q minNext 1", got, s.SessionID())
	}
}

func TestNewSessionImageStoreBindErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	store := &fakeImageSessionStore{bindErr: errors.New("boom")}
	s := testNewSession(t, Dependencies{ImageStore: store, Config: config.Config{}})
	if s == nil {
		t.Fatal("NewSession returned nil session despite non-fatal bind error")
	}
}

func TestRotateSessionBindsNewID(t *testing.T) {
	t.Parallel()
	store := &fakeImageSessionStore{}
	s := testNewSession(t, Dependencies{ImageStore: store, SessionStore: newMockSessionStore(), Config: config.Config{}})
	first := s.SessionID()

	if err := s.rotateSession("", false); err != nil {
		t.Fatalf("rotateSession: %v", err)
	}

	got := store.lastBind()
	if got.sessionID != s.SessionID() || got.sessionID == first || got.minNext != 1 {
		t.Errorf("rotate bind = %+v, want new session %q minNext 1 (was %q)", got, s.SessionID(), first)
	}
}

func TestLoadSessionBindsWithFloorFromLineage(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	mockStore.loadedSessions["resume"] = session.Session{
		ID: "resume",
		Lineage: agent.ConversationLineage{Generations: []agent.ConversationGeneration{
			{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "look at [image img-4: /x.png 8x8 png 1KB]"}}},
		}},
	}
	store := &fakeImageSessionStore{}
	s := testNewSession(t, Dependencies{ImageStore: store, SessionStore: mockStore, Config: config.Config{}})

	if err := s.loadSession(context.Background(), "resume"); err != nil {
		t.Fatalf("loadSession: %v", err)
	}

	got := store.lastBind()
	if got.sessionID != "resume" || got.minNext != 5 {
		t.Errorf("load bind = %+v, want session resume minNext 5", got)
	}
}

func TestForkSessionCopiesImagesBeforeBinding(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	store := &fakeImageSessionStore{}
	s := testNewSession(t, Dependencies{ImageStore: store, SessionStore: mockStore, Config: config.Config{}})
	s.mu.Lock()
	s.sessionTitle = "Original"
	s.lineage = agent.ConversationLineage{Generations: []agent.ConversationGeneration{
		{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}},
	}, NextGenerationID: 2}
	s.mu.Unlock()
	originalID := s.SessionID()

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession: %v", err)
	}

	if len(store.copies) != 1 {
		t.Fatalf("CopySession calls = %d, want 1", len(store.copies))
	}
	if store.copies[0].fromID != originalID {
		t.Errorf("copy from = %q, want %q", store.copies[0].fromID, originalID)
	}
	if got := store.lastBind(); got.sessionID != store.copies[0].toID {
		t.Errorf("final bind %q, want fork %q", got.sessionID, store.copies[0].toID)
	}
}

func TestForkSavedSessionCopiesImages(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	mockStore.loadedSessions["saved-1"] = session.Session{
		ID:    "saved-1",
		Title: "Saved",
		Lineage: agent.ConversationLineage{Generations: []agent.ConversationGeneration{
			{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}},
		}},
	}
	store := &fakeImageSessionStore{}
	s := testNewSession(t, Dependencies{ImageStore: store, SessionStore: mockStore, Config: config.Config{}})

	if err := s.handleForkSavedSession(context.Background(), "saved-1"); err != nil {
		t.Fatalf("handleForkSavedSession: %v", err)
	}

	if len(store.copies) != 1 || store.copies[0].fromID != "saved-1" {
		t.Fatalf("copy calls = %+v, want one from saved-1", store.copies)
	}
	if got := store.lastBind(); got.sessionID != store.copies[0].toID {
		t.Errorf("final bind %q, want fork %q", got.sessionID, store.copies[0].toID)
	}
}

func TestForkSessionCopyErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	store := &fakeImageSessionStore{copyErr: errors.New("disk full")}
	s := testNewSession(t, Dependencies{ImageStore: store, SessionStore: mockStore, Config: config.Config{}})
	s.mu.Lock()
	s.lineage = agent.ConversationLineage{Generations: []agent.ConversationGeneration{
		{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}},
	}, NextGenerationID: 2}
	s.mu.Unlock()

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession with copy error = %v, want nil", err)
	}
}
