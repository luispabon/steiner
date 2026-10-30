package main

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// compactFallbackProviders serves main-model and fallback-model builds from
// separate fake providers, so a test can see which one summarizes.
type compactFallbackProviders struct {
	mainModel     *fakeProvider
	fallbackModel *fakeProvider
}

func (p *compactFallbackProviders) factory(rm provider.ResolvedModel, _ string) (provider.Provider, error) {
	if rm.BackendModelID == "fallback-model" {
		return p.fallbackModel, nil
	}
	return p.mainModel, nil
}

// compactFallbackTestRunner returns an interactive runner on main-model whose
// provider builds go to providers. ref is the static compaction_fallback.
func compactFallbackTestRunner(t *testing.T, ref string, providers *compactFallbackProviders) cliRunner {
	t.Helper()
	r := compactionFallbackRunner(compactionFallbackRuntimeConfig(ref), providers.factory, nil)
	r.runMode = "interactive"
	r.runtime.registry = tool.NewRegistry()
	r.runtime.workDir = t.TempDir()
	r.runtime.homeDir = t.TempDir()
	r.runtime.projectRoot = t.TempDir()
	r.runtime.events = output.NoopSink{}
	return r
}

// TestSessionRunnerCompactWithLineageFallbackColdDecision proves manual
// compaction reaches the same cold decision as a normal run. A last main
// request with a matching identity keeps the summary on main. Any other seed,
// or a cold one, moves the summary to the fallback when one is configured.
func TestSessionRunnerCompactWithLineageFallbackColdDecision(t *testing.T) {
	summary := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"},
		FinishReason: "stop",
	}
	tests := []struct {
		name string
		ref  string
		// change edits the matching warm seed. nil keeps it.
		change       func(*agent.LastMainRequest)
		wantFallback bool
	}{
		{name: "matching warm identity keeps main", ref: "fallback-model"},
		{
			name:         "mismatched cache key is cold",
			ref:          "fallback-model",
			change:       func(s *agent.LastMainRequest) { s.PromptCacheKey = "other-cache-key" },
			wantFallback: true,
		},
		{
			name:         "mismatched backend model is cold",
			ref:          "fallback-model",
			change:       func(s *agent.LastMainRequest) { s.BackendModelID = "other-model" },
			wantFallback: true,
		},
		{
			name:   "empty compaction_fallback never selects fallback",
			ref:    "",
			change: func(s *agent.LastMainRequest) { s.At = time.Time{} },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			providers := &compactFallbackProviders{
				mainModel:     &fakeProvider{responses: []provider.ChatResponse{summary}},
				fallbackModel: &fakeProvider{responses: []provider.ChatResponse{summary}},
			}
			r := compactFallbackTestRunner(t, tc.ref, providers)
			seed := lastMainRequestSeedFor(t, r, time.Now())
			if tc.change != nil {
				tc.change(&seed)
			}

			_, _, _, err := sessionRunner{runner: r}.CompactWithLineage(context.Background(), []agent.Message{
				{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
				{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
			}, agent.ConversationLineage{}, seed, nil, "")
			if err != nil {
				t.Fatalf("CompactWithLineage() error = %v", err)
			}
			if got := len(providers.fallbackModel.requests) > 0; got != tc.wantFallback {
				t.Errorf("fallback model served compaction = %t, want %t", got, tc.wantFallback)
			}
			if got := len(providers.mainModel.requests) > 0; got == tc.wantFallback {
				t.Errorf("main model served compaction = %t, want %t", got, !tc.wantFallback)
			}
		})
	}
}
