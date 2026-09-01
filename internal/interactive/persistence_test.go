package interactive

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/session"
)

func TestSaveSessionPersistsEnabledSkills(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		SkillNames:   []string{"review", "test", "optimize"},
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
		},
	})

	s.skills.Set("review", true)
	s.skills.Set("optimize", true)

	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession() = %v, want nil", err)
	}

	saved, ok := mockStore.savedSessions[s.SessionID()]
	if !ok {
		t.Fatalf("savedSessions = %#v, want session %q to be saved", mockStore.savedSessions, s.SessionID())
	}

	if got, want := saved.Skills, []string{"review", "optimize"}; !equal(got, want) {
		t.Fatalf("saved Skills = %v, want %v", got, want)
	}
}

func TestLoadSessionRestoresEnabledSkills(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()

	mockSession := session.Session{
		ID:     "restore-test",
		Title:  "Restore Test",
		Model:  "test-model",
		Skills: []string{"review", "optimize"},
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID:       1,
					Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "test"}},
				},
			},
			NextGenerationID: 2,
		},
	}
	mockStore.loadedSessions["restore-test"] = mockSession

	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		SkillNames:   []string{"review", "test", "optimize"},
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	})

	if err := s.loadSession(context.Background(), "restore-test"); err != nil {
		t.Fatalf("loadSession() = %v, want nil", err)
	}

	got := s.Skills()
	want := []string{"review", "optimize"}
	if !equal(got, want) {
		t.Fatalf("Skills() after load = %v, want %v", got, want)
	}
}

func TestLoadSessionSilentlyDropsUnknownSkills(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()

	mockSession := session.Session{
		ID:     "unknown-skills-test",
		Title:  "Unknown Skills Test",
		Model:  "test-model",
		Skills: []string{"review", "unknown-skill", "optimize"},
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID:       1,
					Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "test"}},
				},
			},
			NextGenerationID: 2,
		},
	}
	mockStore.loadedSessions["unknown-skills-test"] = mockSession

	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		SkillNames:   []string{"review", "optimize"},
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	})

	if err := s.loadSession(context.Background(), "unknown-skills-test"); err != nil {
		t.Fatalf("loadSession() = %v, want nil", err)
	}

	got := s.Skills()
	want := []string{"review", "optimize"}
	if !equal(got, want) {
		t.Fatalf("Skills() after load with unknown skills dropped = %v, want %v", got, want)
	}
}

func TestForkSessionCarriesEnabledSkills(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		SkillNames:   []string{"review", "test", "optimize"},
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	})

	s.mu.Lock()
	s.sessionTitle = "Original Session"
	s.lineage = agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "test message"}},
			},
		},
		NextGenerationID: 2,
	}
	s.mu.Unlock()

	s.skills.Set("review", true)
	s.skills.Set("optimize", true)

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v, want nil", err)
	}

	if got := s.Skills(); !equal(got, []string{"review", "optimize"}) {
		t.Fatalf("Skills() after fork = %v, want [review optimize]", got)
	}

	if len(mockStore.savedSessions) < 2 {
		t.Fatalf("expected at least 2 sessions saved (original + fork), got %d", len(mockStore.savedSessions))
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
