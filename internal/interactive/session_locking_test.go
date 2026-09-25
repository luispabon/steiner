package interactive

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// sessionLockFree reports whether the session mutex is currently available. A
// callback under test uses it to fail deterministically instead of deadlocking
// when the session lock is still held.
func sessionLockFree(s *Session) bool {
	if s.mu.TryLock() {
		s.mu.Unlock()
		return true
	}
	return false
}

func TestSetModeListenerRunsOutsideSessionLock(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{Modes: config.ModesConfig{Default: config.ExecutionModePlan}},
	})

	var (
		listenerCalls int
		lockHeld      bool
		observed      config.ExecutionMode
	)
	s.SetModeListener(func(_ config.ExecutionMode) {
		listenerCalls++
		if !sessionLockFree(s) {
			lockHeld = true
			return
		}
		// Re-enter the session: this must not deadlock.
		observed = s.Mode()
	})

	s.SetMode(config.ExecutionModeBuild)

	if lockHeld {
		t.Fatal("mode listener ran while the session lock was held")
	}
	if listenerCalls != 1 {
		t.Fatalf("listener calls = %d, want 1", listenerCalls)
	}
	if got, want := observed, config.ExecutionModeBuild; got != want {
		t.Fatalf("re-entrant Mode() from listener = %q, want %q", got, want)
	}
}

func TestSetModeEventSinkRunsOutsideSessionLock(t *testing.T) {
	t.Parallel()
	var (
		s        *Session
		order    []string
		lockHeld bool
	)
	deps := Dependencies{
		Config: config.Config{Modes: config.ModesConfig{Default: config.ExecutionModePlan}},
		BaseEvents: output.SinkFunc(func(event output.Event) {
			if event.Type != output.EventTypeModeChanged {
				return
			}
			if !sessionLockFree(s) {
				lockHeld = true
				return
			}
			order = append(order, "event")
		}),
	}
	s = testNewSession(t, deps)
	s.SetModeListener(func(config.ExecutionMode) {
		if !sessionLockFree(s) {
			lockHeld = true
			return
		}
		order = append(order, "listener")
	})

	s.SetMode(config.ExecutionModeBuild)

	if lockHeld {
		t.Fatal("mode change callback ran while the session lock was held")
	}
	if want := []string{"event", "listener"}; !equal(order, want) {
		t.Fatalf("callback order = %v, want %v", order, want)
	}
}

func TestLoadSessionListenerRunsOutsideSessionLock(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	mockStore.loadedSessions["lock-scope-session"] = session.Session{
		ID:    "lock-scope-session",
		Model: "test-model",
		Mode:  string(config.ExecutionModeBuild),
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "previous"}}},
			},
			NextGenerationID: 2,
		},
	}

	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
			Modes: config.ModesConfig{Default: config.ExecutionModePlan},
		},
	})

	var (
		listenerCalls int
		lockHeld      bool
		observed      config.ExecutionMode
	)
	s.SetModeListener(func(_ config.ExecutionMode) {
		listenerCalls++
		if !sessionLockFree(s) {
			lockHeld = true
			return
		}
		observed = s.Mode()
	})

	if err := s.LoadSessionByID(context.Background(), "lock-scope-session"); err != nil {
		t.Fatalf("LoadSessionByID() = %v, want nil", err)
	}

	if lockHeld {
		t.Fatal("mode listener ran while the session lock was held during loadSession")
	}
	if listenerCalls != 1 {
		t.Fatalf("listener calls = %d, want 1", listenerCalls)
	}
	if got, want := observed, config.ExecutionModeBuild; got != want {
		t.Fatalf("re-entrant Mode() from listener = %q, want %q", got, want)
	}
}

func TestSetRunnerConcurrentWithRunAndCompaction(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{
					DefaultModel:            "test-model",
					ActiveOrchestratorModel: "test-model",
				},
			},
			Modes: config.ModesConfig{Default: config.ExecutionModePlan},
		},
	})
	s.SetConversation([]agent.Message{
		{Role: agent.MessageRoleUser, Content: "one"},
		{Role: agent.MessageRoleAssistant, Content: "two"},
		{Role: agent.MessageRoleUser, Content: "three"},
		{Role: agent.MessageRoleAssistant, Content: "four"},
		{Role: agent.MessageRoleUser, Content: "five"},
		{Role: agent.MessageRoleAssistant, Content: "six"},
	})
	if !manualCompactionHasSource(s.Conversation()) {
		t.Fatal("test conversation has no compaction source")
	}

	var sharedCalls atomic.Int64
	shared := &concurrentRunExecutor{calls: &sharedCalls}
	s.SetRunner(shared)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.SetRunner(shared)
		}()
		go func() {
			defer wg.Done()
			s.submitPrompt(context.Background(), "prompt", nil)
		}()
		go func() {
			defer wg.Done()
			s.manualCompaction(context.Background())
		}()
	}
	wg.Wait()

	// The last swap must be the executor the next turn uses.
	var used atomic.Int64
	marker := &concurrentRunExecutor{calls: &used}
	s.SetRunner(marker)
	s.submitPrompt(context.Background(), "after swap", nil)
	if got := used.Load(); got != 1 {
		t.Fatalf("runner invocations after SetRunner = %d, want 1", got)
	}
}

// concurrentRunExecutor is a runExecutor that counts invocations and is safe for
// concurrent use.
type concurrentRunExecutor struct {
	calls *atomic.Int64
}

func (e *concurrentRunExecutor) Run(context.Context, []agent.Message, func() []agent.SteerMessage) (RunResult, error) {
	e.calls.Add(1)
	return RunResult{}, nil
}

func (e *concurrentRunExecutor) Compact(_ context.Context, conversation []agent.Message, _ []provider.ToolSpec, _ string) ([]agent.Message, error) {
	e.calls.Add(1)
	return conversation, nil
}
