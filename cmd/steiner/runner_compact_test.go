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
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
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

// TestCLIRunnerCompactDoesNotBecomeCacheBaseline establishes a normal-turn
// baseline, runs manual compaction, then proves the next normal request still
// compares against the pre-compaction baseline rather than the compaction
// request. The compaction conversation deliberately starts with a different
// first message, so a compaction-promoted baseline would yield a shorter
// shared prefix than the normal one.
func TestCLIRunnerCompactDoesNotBecomeCacheBaseline(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "first answer"}, FinishReason: "stop", Usage: &provider.UsageStats{PromptTokens: 10, CompletionTokens: 2}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop", Usage: &provider.UsageStats{PromptTokens: 20, CompletionTokens: 3}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "second answer"}, FinishReason: "stop", Usage: &provider.UsageStats{PromptTokens: 30, CompletionTokens: 4}},
	}}

	diagDir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Cache: true}, RunID: "compact-baseline-test"})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	r := cliRunner{
		runtime: cliRuntime{
			cfg:           testRuntimeConfig("test-model"),
			provider:      prov,
			registry:      tool.NewRegistry(),
			workDir:       t.TempDir(),
			homeDir:       t.TempDir(),
			events:        output.NoopSink{},
			diagnostics:   writer,
			usageRecorder: usagestats.New(nil),
		},
		runMode:          "interactive",
		promptCacheKeyFn: func() string { return "session-cache-key" },
		cacheBaseline:    agent.NewCacheBaselineStore(),
	}

	// Normal turn 1 promotes the parent baseline.
	if _, err := r.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "normal turn"}}, nil, nil); err != nil {
		t.Fatalf("Run() turn 1 error = %v", err)
	}

	// Manual compaction runs with a conversation whose first message differs
	// from the normal turn, so its outbound prefix diverges early.
	if _, err := r.Compact(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "different first"},
		{Role: agent.MessageRoleAssistant, Content: "different answer"},
		{Role: agent.MessageRoleUser, Content: "compact me"},
		{Role: agent.MessageRoleAssistant, Content: "ok"},
	}, nil, nil, ""); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	// Normal turn 2 continues from turn 1; its comparison must be against the
	// pre-compaction baseline, not the compaction request.
	if _, err := r.Run(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "normal turn"},
		{Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "normal turn 2"},
	}, nil, nil); err != nil {
		t.Fatalf("Run() turn 2 error = %v", err)
	}

	if len(prov.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (normal, compaction, normal)", len(prov.requests))
	}
	records := readCompactCacheRecords(t, diagDir)
	if len(records) != 3 {
		t.Fatalf("cache records = %d, want 3", len(records))
	}
	if records[1].Payload.CallKind != "compaction" {
		t.Fatalf("record[1] call_kind = %q, want %q", records[1].Payload.CallKind, "compaction")
	}
	if !records[1].Payload.PrefixComparisonEnabled {
		t.Fatal("compaction prefix_comparison_enabled = false, want true")
	}
	if !records[1].Payload.PrefixPredecessorKnown {
		t.Fatal("compaction prefix_predecessor_known = false, want the normal-turn baseline")
	}

	normalShared := lcpMessageCount(prov.requests[0].Messages, prov.requests[2].Messages)
	compactionShared := lcpMessageCount(prov.requests[1].Messages, prov.requests[2].Messages)
	if compactionShared >= normalShared {
		t.Fatalf("test setup: compaction prefix (%d) not shorter than the normal prefix (%d)", compactionShared, normalShared)
	}

	last := records[2].Payload
	if !last.PrefixComparisonEnabled {
		t.Fatal("post-compaction normal turn prefix_comparison_enabled = false, want true")
	}
	if !last.PrefixPredecessorKnown {
		t.Fatal("post-compaction normal turn prefix_predecessor_known = false, want the pre-compaction baseline")
	}
	if last.SharedPrefixMessages != normalShared {
		t.Fatalf("post-compaction shared prefix = %d, want %d (pre-compaction baseline), not %d (compaction request)", last.SharedPrefixMessages, normalShared, compactionShared)
	}
}

// lcpMessageCount returns the number of leading provider messages a and b share,
// matching the per-message hash comparison cache diagnostics use.
func lcpMessageCount(a, b []provider.Message) int {
	n := min(len(a), len(b))
	count := 0
	for count < n && provider.MessageHashInput(a[count]) == provider.MessageHashInput(b[count]) {
		count++
	}
	return count
}

// compactCacheRecord captures the cache payload fields the wiring test asserts.
type compactCacheRecord struct {
	Payload struct {
		CallKind                string `json:"call_kind"`
		SharedPrefixMessages    int    `json:"shared_prefix_messages"`
		PrefixComparisonEnabled bool   `json:"prefix_comparison_enabled"`
		PrefixPredecessorKnown  bool   `json:"prefix_predecessor_known"`
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
