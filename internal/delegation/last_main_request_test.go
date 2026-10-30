package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

func TestSaveChildSessionStoresLastMainRequest(t *testing.T) {
	t.Parallel()
	want := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}
	store := NewSessionStore()

	if !saveChildSession(store, Spec{AgentID: "child-1"}, agent.RunRequest{}, agent.RunState{LastMainRequest: want}, TokenUsage{}, nil) {
		t.Fatal("saveChildSession() = false, want true")
	}
	got, ok := store.Get("child-1")
	if !ok {
		t.Fatal("Get(child-1) returned false after saveChildSession")
	}
	if got.LastMainRequest != want {
		t.Errorf("LastMainRequest = %+v, want %+v", got.LastMainRequest, want)
	}
}

func TestSessionStoreUpdateReplacesLastMainRequest(t *testing.T) {
	t.Parallel()
	first := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}
	second := agent.LastMainRequest{At: time.Unix(200, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "child-1"}, LastMainRequest: first})

	if !store.Update("child-1", SessionUpdateParams{LastMainRequest: second}) {
		t.Fatal("Update(child-1) = false, want true")
	}
	got, ok := store.Get("child-1")
	if !ok {
		t.Fatal("Get(child-1) returned false after Update")
	}
	if got.LastMainRequest != second {
		t.Errorf("LastMainRequest = %+v, want %+v", got.LastMainRequest, second)
	}
}

// TestFollowUpSeedsFromSessionLastMainRequest checks that each follow-up seeds
// from the session's last main request rather than the seed stored on the
// original request, and that the value returned by each run is stored for the
// next follow-up. The stored request keeps its compaction fallback.
func TestFollowUpSeedsFromSessionLastMainRequest(t *testing.T) {
	t.Parallel()
	errFallback := errors.New("fallback resolver")
	stale := agent.LastMainRequest{At: time.Unix(50, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-0"}
	stored := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}
	updated := agent.LastMainRequest{At: time.Unix(200, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}

	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-1", AgentType: AgentTypeReview, Task: "inspect code"},
		Request: agent.RunRequest{
			SeedLastMainRequest: stale,
			CompactionFallback: agent.CompactionFallback{
				Ref:   "fallback",
				After: time.Minute,
				Resolve: func(context.Context) (agent.CompactionTarget, error) {
					return agent.CompactionTarget{}, errFallback
				},
			},
		},
		Conversation:    []agent.Message{{Role: agent.MessageRoleUser, Content: "initial task"}},
		TurnCount:       1,
		LastMainRequest: stored,
	})

	var seeds []agent.LastMainRequest
	var fallback agent.CompactionFallback
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		seeds = append(seeds, req.SeedLastMainRequest)
		fallback = req.CompactionFallback
		state := successRunState()
		state.LastMainRequest = updated
		return state, nil
	}}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 7, MaxTokens: 77, MaxFollowUps: 100},
		Events:       output.NoopSink{},
		SessionStore: store,
		Runner:       runner,
	})

	for i := range 2 {
		if _, err := handler(context.Background(), map[string]any{"agent_id": "child-1", "message": "continue"}); err != nil {
			t.Fatalf("follow-up %d returned error: %v", i+1, err)
		}
	}

	if len(seeds) != 2 {
		t.Fatalf("runner calls = %d, want 2", len(seeds))
	}
	if seeds[0] != stored {
		t.Errorf("first follow-up seed = %+v, want session LastMainRequest %+v", seeds[0], stored)
	}
	if seeds[1] != updated {
		t.Errorf("second follow-up seed = %+v, want %+v stored by the first follow-up", seeds[1], updated)
	}
	session, ok := store.Get("child-1")
	if !ok {
		t.Fatal("session missing after follow-ups")
	}
	if session.LastMainRequest != updated {
		t.Errorf("stored LastMainRequest = %+v, want %+v", session.LastMainRequest, updated)
	}
	if fallback.Ref != "fallback" || fallback.After != time.Minute || fallback.Resolve == nil {
		t.Fatalf("follow-up CompactionFallback = {Ref: %q, After: %v, Resolve nil: %t}, want stored fallback", fallback.Ref, fallback.After, fallback.Resolve == nil)
	}
	if _, err := fallback.Resolve(context.Background()); !errors.Is(err, errFallback) {
		t.Errorf("follow-up fallback Resolve error = %v, want the stored resolver", err)
	}
}

// TestSpawnDelegateRemediationSeedsFromRunState checks that the remediation run
// seeds from the main run's last main request and that the state returned after
// remediation carries the value the remediation run reports.
func TestSpawnDelegateRemediationSeedsFromRunState(t *testing.T) {
	t.Parallel()
	last := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "main", BackendModelID: "gpt-main", PromptCacheKey: "key-1"}
	remediationCalls := 0
	var remediationSeed agent.LastMainRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		state := successRunState()
		if len(req.SourceConversation) == 0 || !strings.Contains(req.SourceConversation[len(req.SourceConversation)-1].Content, "Pre-remediation HEAD") {
			state.LastMainRequest = last
			return state, nil
		}
		remediationCalls++
		remediationSeed = req.SeedLastMainRequest
		// Report the seed back unchanged, as a remediation run that makes no main call would.
		state.LastMainRequest = req.SeedLastMainRequest
		return state, nil
	}}
	cfg := remediationConfigForDirty([]string{"changed.go"}, nil, nil)

	_, state, _, err := SpawnDelegate(context.Background(), remediationTestSpec(), remediationTestRequest(), runner, noopEventSink{}, nil, WithRemediation(cfg))
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if remediationCalls != 1 {
		t.Fatalf("remediation calls = %d, want 1", remediationCalls)
	}
	if remediationSeed != last {
		t.Errorf("remediation SeedLastMainRequest = %+v, want %+v", remediationSeed, last)
	}
	if state.LastMainRequest != last {
		t.Errorf("returned LastMainRequest = %+v, want %+v", state.LastMainRequest, last)
	}
}
