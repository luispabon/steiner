package interactive

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

func savedUserSession(id, content string) session.Session {
	return session.Session{
		ID:    id,
		Title: id,
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: content}}},
			},
			NextGenerationID: 2,
		},
	}
}

func indexOfType(events []output.Event, typ string) int {
	for i, event := range events {
		if event.Type == typ {
			return i
		}
	}
	return -1
}

func TestLoadSessionEmitsConversationResetBeforeReplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action func(id string) Action
	}{
		{"load", func(id string) Action { return LoadSession{SessionID: id} }},
		{"fork saved", func(id string) Action { return ForkSavedSession{SessionID: id} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			store := newMockSessionStore()
			store.loadedSessions["old"] = savedUserSession("old", "previous message")
			s := testNewSession(t, Dependencies{
				BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
				SessionStore: store,
			})

			if err := s.Handle(context.Background(), tt.action("old")); err != nil {
				t.Fatalf("Handle() = %v, want nil", err)
			}

			reset := indexOfType(events, output.EventTypeConversationReset)
			replayed := indexOfType(events, output.EventTypeUserInput)
			if reset < 0 || replayed < 0 {
				t.Fatalf("event types = %v, want conversation_reset and user_input", eventTypes(events))
			}
			if reset > replayed {
				t.Errorf("conversation_reset at %d after first replayed user_input at %d", reset, replayed)
			}
			if got := len(eventsOfType(events, output.EventTypeConversationReset)); got != 1 {
				t.Errorf("conversation_reset count = %d, want 1", got)
			}
		})
	}
}

func TestLoadSessionResetPrecedesImageStoreWarning(t *testing.T) {
	t.Parallel()
	var events []output.Event
	store := newMockSessionStore()
	store.loadedSessions["old"] = savedUserSession("old", "previous message")
	s := testNewSession(t, Dependencies{
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore: store,
		ImageStore:   &fakeImageSessionStore{},
	})
	// Fail only the bind performed by the load, not the one at construction.
	s.deps.ImageStore = &fakeImageSessionStore{bindErr: errors.New("boom")}
	events = nil

	if err := s.Handle(context.Background(), LoadSession{SessionID: "old"}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	reset := indexOfType(events, output.EventTypeConversationReset)
	warning := indexOfType(events, output.EventTypeContextDiagnostics)
	if reset < 0 || warning < 0 {
		t.Fatalf("event types = %v, want conversation_reset and a bind warning", eventTypes(events))
	}
	if reset > warning {
		t.Errorf("conversation_reset at %d after image-store warning at %d: the reset would wipe the warning", reset, warning)
	}
}

func TestLoadSessionRefusedLoadEmitsNoConversationReset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(store *mockSessionStore)
		id    string
	}{
		{"missing session", func(*mockSessionStore) {}, "does-not-exist"},
		{"unknown mode", func(store *mockSessionStore) {
			bad := savedUserSession("bad", "x")
			bad.Mode = "readwrite"
			store.loadedSessions["bad"] = bad
		}, "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			store := newMockSessionStore()
			tt.setup(store)
			s := testNewSession(t, Dependencies{
				BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
				SessionStore: store,
				Config:       config.Config{Modes: config.ModesConfig{Default: config.ExecutionModePlan}},
			})

			if err := s.Handle(context.Background(), LoadSession{SessionID: tt.id}); err == nil {
				t.Fatal("Handle(LoadSession) = nil, want error")
			}
			if got := len(eventsOfType(events, output.EventTypeConversationReset)); got != 0 {
				t.Errorf("conversation_reset count = %d, want 0: a refused load must leave the transcript alone", got)
			}
		})
	}
}
