package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	compactTestModelID         = "openrouter/test-model"
	compactTestContextWindow   = 262144
	compactTestMaxTokens       = 8192
	compactTestProviderBaseURL = "https://openrouter.example/api/v1"
)

// newCompactTestRunner builds a cliRunner wired to a fake provider and a
// discovered model catalog, so Compact resolves the discovered context window
// and max output tokens. The returned provider starts with no queued responses;
// callers append one per expected model call. The captured model pointer is set
// on the first providerFactory call.
func newCompactTestRunner(t *testing.T) (cliRunner, *fakeProvider, *provider.ResolvedModel) {
	t.Helper()

	// Isolate the models.dev cache so this test never touches the real
	// on-disk cache or network: catalog answers context/max output, but
	// modelsDevSource still runs for the remaining unconfigured facts
	// (vision, reasoning efforts, echo-back).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mdCache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(mdCache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(mdCache.CachePath(), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(mdCache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cacheDir := t.TempDir()
	cache := modelcatalog.NewCache(cacheDir)
	envelope := modelcatalog.CacheEnvelope{
		Fingerprint: modelcatalog.CacheFingerprint{ProviderType: string(config.ProviderTypeOpenRouter), BaseURL: compactTestProviderBaseURL},
		FetchedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
		Models: []modelcatalog.DiscoveredModel{
			{ID: compactTestModelID, ContextLength: compactTestContextWindow, MaxOutputTokens: compactTestMaxTokens},
		},
	}
	if err := cache.SaveAtomic("openrouter", envelope); err != nil {
		t.Fatalf("SaveAtomic() error = %v", err)
	}
	modelCatalog := modelcatalog.NewService(nil, cache, modelcatalog.NewStore(cacheDir+"/popularity.json"), nil, true)

	projectRoot := t.TempDir()
	prov := &fakeProvider{}
	captured := new(provider.ResolvedModel)
	r := cliRunner{
		runtime: cliRuntime{
			cfg: config.Config{
				Providers: map[string]config.ProviderConfig{"openrouter": {Type: config.ProviderTypeOpenRouter, BaseURL: compactTestProviderBaseURL}},
				Models: config.ModelsConfig{
					Effective: config.EffectiveModelAssignments{
						DefaultModel:            "test",
						ActiveOrchestratorModel: "test",
					},
					Definitions: map[string]config.ModelConfig{"test": {Provider: "openrouter", ID: compactTestModelID}},
				},
				SubAgent: config.SubAgentConfig{Enabled: true}, Advisor: config.AdvisorConfig{Enabled: true},
			},
			providerFactory: func(rm provider.ResolvedModel, _ string) (provider.Provider, error) { *captured = rm; return prov, nil },
			modelCatalog:    modelCatalog, projectRoot: projectRoot, workDir: projectRoot, homeDir: projectRoot,
		},
		currentAlias: func() string { return "test" },
	}
	return r, prov, captured
}

func TestCLIRunnerCompactUsesResolvedLimitsAndAssembly(t *testing.T) {
	r, prov, captured := newCompactTestRunner(t)
	prov.responses = []provider.ChatResponse{{
		Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop",
	}}

	conversation, err := r.Compact(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(conversation) == 0 {
		t.Fatal("Compact() returned empty conversation")
	}
	if got := captured.EffectiveLimits.ContextWindow; got != compactTestContextWindow {
		t.Fatalf("context window = %d, want %d", got, compactTestContextWindow)
	}
	if got := captured.EffectiveLimits.MaxOutputTokens; got != compactTestMaxTokens {
		t.Fatalf("max output tokens = %d, want %d", got, compactTestMaxTokens)
	}
	if len(prov.requests) == 0 {
		t.Fatal("Compact() made no provider request")
	}
	preamble := prov.requests[0].Messages[0].Content
	if !strings.Contains(preamble, "## Your role") {
		t.Fatalf("preamble missing delegation role section:\n%s", preamble)
	}
	if !strings.Contains(preamble, "## Advisor") {
		t.Fatalf("preamble missing advisor section:\n%s", preamble)
	}
}

// TestCLIRunnerCompactCarriesCacheBaselineAndUsageRecorder proves manual
// compaction reuses the session's cache identity and usage accounting: its
// cache diagnostics report a live prefix comparison, it never promotes the
// baseline, and the summariser response is recorded exactly once.
func TestCLIRunnerCompactCarriesCacheBaselineAndUsageRecorder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r, prov, _ := newCompactTestRunner(t)
	prov.responses = []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"},
		FinishReason: "stop",
		Usage:        &provider.UsageStats{PromptTokens: 100, CompletionTokens: 20, CacheReadInputTokens: 40},
	}}

	diagDir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Cache: true}, RunID: "compact-test"})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	recorder := usagestats.New(nil)
	r.runtime.diagnostics = writer
	r.runtime.usageRecorder = recorder
	r.cacheBaseline = agent.NewCacheBaselineStore()
	r.promptCacheKeyFn = func() string { return "session-cache-key" }

	conversation, err := r.Compact(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(conversation) == 0 {
		t.Fatal("Compact() returned empty conversation")
	}

	if got := recorder.SessionReportFor(usagestats.SourceParent).Requests; got != 1 {
		t.Fatalf("recorded usage requests = %d, want exactly 1", got)
	}

	records := readCompactCacheRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("cache records = %d, want 1", len(records))
	}
	if !records[0].Payload.PrefixComparisonEnabled {
		t.Fatal("prefix_comparison_enabled = false, want true for manual compaction")
	}
	if records[0].Payload.PrefixPredecessorKnown {
		t.Fatal("prefix_predecessor_known = true, want false: compaction must not promote the baseline")
	}

	// The compaction request reuses the session prompt cache key, like a normal
	// turn, so its tools-and-messages prefix can hit the provider's cache.
	if got := prov.requests[0].PromptCacheKey; got != "session-cache-key" {
		t.Fatalf("PromptCacheKey = %q, want %q", got, "session-cache-key")
	}
}

// compactCacheRecord captures the cache payload fields the wiring test asserts.
type compactCacheRecord struct {
	Payload struct {
		PrefixComparisonEnabled bool `json:"prefix_comparison_enabled"`
		PrefixPredecessorKnown  bool `json:"prefix_predecessor_known"`
	} `json:"payload"`
}

func readCompactCacheRecords(t *testing.T, dir string) []compactCacheRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "cache.jsonl"))
	if err != nil {
		t.Fatalf("read cache.jsonl: %v", err)
	}
	var out []compactCacheRecord
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec compactCacheRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}
