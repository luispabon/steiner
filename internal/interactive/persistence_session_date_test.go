package interactive

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

func sessionDateTestConfig() config.Config {
	return config.Config{
		Models: config.ModelsConfig{
			Effective:   config.EffectiveModelAssignments{DefaultModel: "test", ActiveOrchestratorModel: "test"},
			Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
		},
	}
}

func setSessionNow(t *testing.T, s *Session, now time.Time) {
	t.Helper()
	s.mu.Lock()
	s.now = func() time.Time { return now }
	s.mu.Unlock()
}

func TestLoadSessionRestoresStoredSessionDate(t *testing.T) {
	t.Parallel()

	stored := &session.StartDate{Date: "2026-09-13", Zone: "BST", Offset: "+01:00"}
	record := savedUserSession("dated", "hello")
	record.SessionDate = stored
	store := newMockSessionStore()
	store.loadedSessions["dated"] = record
	s := testNewSession(t, Dependencies{SessionStore: store})
	// The resume happens on the next day, in a different zone.
	setSessionNow(t, s, time.Date(2026, 9, 14, 9, 15, 0, 0, time.FixedZone("CEST", 7200)))

	if err := s.Handle(context.Background(), LoadSession{SessionID: "dated"}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	date, zone, offset := s.SessionDate().Parts()
	if date != "2026-09-13" || zone != "BST" || offset != "+01:00" {
		t.Fatalf("SessionDate parts = (%q, %q, %q), want (2026-09-13, BST, +01:00)", date, zone, offset)
	}
	if got := store.loadedSessions["dated"].SessionDate; got == nil || *got != *stored {
		t.Fatalf("stored SessionDate = %#v, want the original %#v", got, stored)
	}
}

func TestLoadLegacySessionCapturesAndPersistsSessionDate(t *testing.T) {
	t.Parallel()

	firstResume := time.Date(2026, 9, 14, 9, 15, 0, 0, time.FixedZone("CEST", 7200))
	record := savedUserSession("legacy-date", "hello")
	store := newMockSessionStore()
	store.loadedSessions["legacy-date"] = record
	s := testNewSession(t, Dependencies{SessionStore: store})
	setSessionNow(t, s, firstResume)

	if err := s.Handle(context.Background(), LoadSession{SessionID: "legacy-date"}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	want := prompt.NewSessionDate(firstResume)
	if got := s.SessionDate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionDate = %v, want the capture-time date %v", got, want)
	}
	stored := store.loadedSessions["legacy-date"].SessionDate
	if stored == nil {
		t.Fatal("legacy resume did not persist the captured date")
	}
	wantDate, wantZone, wantOffset := want.Parts()
	if stored.Date != wantDate || stored.Zone != wantZone || stored.Offset != wantOffset {
		t.Fatalf("persisted SessionDate = %#v, want (%q, %q, %q)", stored, wantDate, wantZone, wantOffset)
	}

	// A second resume on a later day keeps the date captured by the first one.
	setSessionNow(t, s, firstResume.Add(48*time.Hour))
	if err := s.Handle(context.Background(), LoadSession{SessionID: "legacy-date"}); err != nil {
		t.Fatalf("Handle(second LoadSession) = %v, want nil", err)
	}
	if got := s.SessionDate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionDate after second resume = %v, want %v", got, want)
	}
}

func TestForkSessionPreservesSessionDate(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 13, 23, 55, 0, 0, time.FixedZone("BST", 3600))
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: sessionDateTestConfig()})
	s.mu.Lock()
	s.now = func() time.Time { return started }
	s.sessionDate = prompt.NewSessionDate(started)
	s.sessionTitle = "Original"
	s.mu.Unlock()

	// The fork is created on a later day, in a different zone.
	setSessionNow(t, s, started.Add(48*time.Hour))
	if err := s.Handle(context.Background(), ForkSession{}); err != nil {
		t.Fatalf("Handle(ForkSession) = %v, want nil", err)
	}

	date, zone, offset := s.SessionDate().Parts()
	if date != "2026-09-13" || zone != "BST" || offset != "+01:00" {
		t.Fatalf("SessionDate parts after fork = (%q, %q, %q), want (2026-09-13, BST, +01:00)", date, zone, offset)
	}
	stored := store.savedSessions[s.SessionID()].SessionDate
	if stored == nil || stored.Date != date || stored.Zone != zone || stored.Offset != offset {
		t.Fatalf("forked record SessionDate = %#v, want (%q, %q, %q)", stored, date, zone, offset)
	}
}

func TestManualCompactionKeepsSessionDate(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 13, 23, 55, 0, 0, time.FixedZone("BST", 3600))
	runner := &runExecutorFunc{compact: func(context.Context, []agent.Message, []provider.ToolSpec) ([]agent.Message, error) {
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner, Config: sessionDateTestConfig()})
	s.mu.Lock()
	s.sessionDate = prompt.NewSessionDate(started)
	s.mu.Unlock()
	conversation := twoTurnConversation()
	seedConversation(s, conversation, agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: cloneMessages(conversation)}},
		NextGenerationID: 2,
	})

	compactAndWait(t, s, "")

	if got, want := s.SessionDate(), prompt.NewSessionDate(started); !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionDate after compaction = %v, want %v", got, want)
	}
}

func TestClearConversationCapturesNewSessionDate(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 13, 23, 55, 0, 0, time.FixedZone("BST", 3600))
	later := base.Add(48 * time.Hour)
	s := testNewSession(t, Dependencies{SessionStore: newMockSessionStore(), Config: sessionDateTestConfig()})
	s.mu.Lock()
	s.now = func() time.Time { return base }
	s.sessionDate = prompt.NewSessionDate(base)
	s.mu.Unlock()

	setSessionNow(t, s, later)
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("Handle(ClearConversation) = %v, want nil", err)
	}

	if got, want := s.SessionDate(), prompt.NewSessionDate(later); !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionDate after clear = %v, want the clear-time date %v", got, want)
	}
}
