package interactive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

type compactionTestProvider struct {
	requests []provider.ChatRequest
}

func (p *compactionTestProvider) ChatCompletion(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, request)
	return provider.ChatResponse{
		Message: provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: "summary",
		},
		FinishReason: "stop",
	}, nil
}

func (p *compactionTestProvider) StreamChatCompletion(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("not implemented")
}

func (p *compactionTestProvider) SupportsUsageStats() bool {
	return false
}

func TestRunManualCompactionEmitsLifecycleAndClearsControllerOnSuccess(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	result, err := s.runManualCompaction(context.Background(), "test-model", func(_ context.Context) ([]agent.Message, error) {
		s.events.Emit(output.NewAssistantChunkEventWithSource(1, "streamed chunk", output.ChunkSourceAssistant))
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	})
	if err != nil {
		t.Fatalf("runManualCompaction() error = %v, want nil", err)
	}
	if got, want := len(result), 1; got != want {
		t.Fatalf("result len = %d, want %d", got, want)
	}

	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after successful compaction")
	}

	if got, want := len(events), 4; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeAssistantChunk; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}
	if got, want := events[3].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[3].Type = %q, want %q", got, want)
	}

	started, ok := events[0].Payload.(output.RunStartedEvent)
	if !ok {
		t.Fatalf("events[0].Payload type = %T, want output.RunStartedEvent", events[0].Payload)
	}
	if got, want := started.Mode, "interactive"; got != want {
		t.Fatalf("run started mode = %q, want %q", got, want)
	}
	if got, want := started.Model, "test-model"; got != want {
		t.Fatalf("run started model = %q, want %q", got, want)
	}

	compacting, ok := events[1].Payload.(output.ContextDiagnosticsEvent)
	if !ok {
		t.Fatalf("events[1].Payload type = %T, want output.ContextDiagnosticsEvent", events[1].Payload)
	}
	if got, want := compacting.Kind, "compaction"; got != want {
		t.Fatalf("compaction kind = %q, want %q", got, want)
	}
	if got, want := compacting.Severity, "compacting"; got != want {
		t.Fatalf("compaction severity = %q, want %q", got, want)
	}

	finished, ok := events[3].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[3].Payload type = %T, want output.RunFinishedEvent", events[3].Payload)
	}
	if got, want := finished.Reason, "complete"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, ""; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionEmitsRunFinishedAndClearsControllerOnError(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	_, compactErr := s.runManualCompaction(context.Background(), "test-model", func(context.Context) ([]agent.Message, error) {
		return nil, errors.New("boom")
	})
	if compactErr == nil {
		t.Fatal("runManualCompaction() error = nil, want non-nil")
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after failed compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "error"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, "boom"; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionCancelsAndClearsController(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-started
		ctrl.Interrupt()
		close(done)
	}()

	_, compactErr := s.runManualCompaction(context.Background(), "test-model", func(ctx context.Context) ([]agent.Message, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-done
	if !errors.Is(compactErr, context.Canceled) {
		t.Fatalf("runManualCompaction() error = %v, want context.Canceled", compactErr)
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after cancelled compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "cancelled"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, context.Canceled.Error(); got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestManualCompactionSkipsSingleTurnConversation(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	s.SetConversation([]agent.Message{
		{Role: agent.MessageRoleUser, Content: "investigate the bug"},
		{Role: agent.MessageRoleAssistant, Content: "looking into it"},
	})

	s.manualCompaction(context.Background())

	if got, want := len(events), 1; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeContextReport; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	report, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("events[0].Payload type = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if got, want := report.Content, "Nothing to compact yet; need at least two conversation turns."; got != want {
		t.Fatalf("context report = %q, want %q", got, want)
	}
}

func TestManualCompactionUsesDiscoveryResolvedLimits(t *testing.T) {
	t.Parallel()

	const discoveredContextWindow = 262144
	const discoveredMaxTokens = 8192

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openrouter/test-model",
					"context_length": discoveredContextWindow,
					"top_provider": map[string]any{
						"max_completion_tokens": discoveredMaxTokens,
					},
				},
			},
		})
	}))
	defer srv.Close()

	prov := &compactionTestProvider{}
	var captured provider.ResolvedModel

	s := testNewSession(t, Dependencies{
		Config: config.Config{
			DefaultModel: "test",
			Providers: map[string]config.ProviderConfig{
				"openrouter": {
					Type:    config.ProviderTypeOpenRouter,
					BaseURL: srv.URL,
				},
			},
			Models: map[string]config.ModelConfig{
				"test": {
					Provider: "openrouter",
					ID:       "openrouter/test-model",
				},
			},
		},
		HTTPClient: srv.Client(),
		ProviderFactory: func(rm provider.ResolvedModel) (provider.Provider, error) {
			captured = rm
			return prov, nil
		},
	})

	s.SetConversation([]agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"},
		{Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"},
		{Role: agent.MessageRoleAssistant, Content: "second answer"},
	})

	s.manualCompaction(context.Background())

	if got, want := captured.EffectiveLimits.ContextWindow, discoveredContextWindow; got != want {
		t.Fatalf("resolved context window = %d, want %d", got, want)
	}
	if got, want := captured.EffectiveLimits.MaxOutputTokens, discoveredMaxTokens; got != want {
		t.Fatalf("resolved max output tokens = %d, want %d", got, want)
	}
	if got, want := len(prov.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
}

func TestManualCompactionPersistsCompactSessionWithoutFollowupPrompt(t *testing.T) {
	t.Parallel()

	const sessionID = "persisted-compaction-session"
	const modelID = "openrouter/test-model"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             modelID,
					"context_length": 262144,
					"top_provider": map[string]any{
						"max_completion_tokens": 8192,
					},
				},
			},
		})
	}))
	defer srv.Close()

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	originalConversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first turn"},
		{Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second turn"},
		{Role: agent.MessageRoleAssistant, Content: "second answer"},
	}
	originalLineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: cloneMessages(originalConversation),
			},
		},
		NextGenerationID: 2,
	}
	initialSession, err := session.NewSession(modelID, originalLineage)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	initialSession.ID = sessionID
	initialSession.Title = "Persist me"
	if err := store.Save(initialSession); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	prov := &compactionTestProvider{}
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			DefaultModel: "test",
			Providers: map[string]config.ProviderConfig{
				"openrouter": {
					Type:    config.ProviderTypeOpenRouter,
					BaseURL: srv.URL,
				},
			},
			Models: map[string]config.ModelConfig{
				"test": {
					Provider: "openrouter",
					ID:       modelID,
				},
			},
		},
		SessionStore: store,
		HTTPClient:   srv.Client(),
		ProviderFactory: func(provider.ResolvedModel) (provider.Provider, error) {
			return prov, nil
		},
	})

	s.mu.Lock()
	s.sessionID = sessionID
	s.sessionTitle = initialSession.Title
	s.lineage = originalLineage
	s.conversation = cloneMessages(originalConversation)
	s.mu.Unlock()

	s.manualCompaction(context.Background())

	loaded, err := store.Load(sessionID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got, want := loaded.ID, sessionID; got != want {
		t.Fatalf("loaded session ID = %q, want %q", got, want)
	}
	if got, want := loaded.Model, modelID; got != want {
		t.Fatalf("loaded session model = %q, want %q", got, want)
	}
	if got, want := loaded.Title, "Persist me"; got != want {
		t.Fatalf("loaded session title = %q, want %q", got, want)
	}
	got := loaded.Lineage.FullMessages()
	want := s.Conversation()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted conversation = %#v, want %#v", got, want)
	}
}
