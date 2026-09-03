package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// errStreamOnceProvider streams a single error chunk, then closes.
type errStreamOnceProvider struct{}

func (errStreamOnceProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("chat completion not used")
}

func (errStreamOnceProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	out := make(chan provider.ChatChunk, 1)
	out <- provider.ChatChunk{Error: "boom"}
	close(out)
	return out, nil
}

func (errStreamOnceProvider) SupportsUsageStats() bool { return true }

// canceledChatProvider returns a context.Canceled error from ChatCompletion.
type canceledChatProvider struct{}

func (canceledChatProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("chat completion: %w", context.Canceled)
}

func (canceledChatProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, fmt.Errorf("stream chat completion not used")
}

func (canceledChatProvider) SupportsUsageStats() bool { return true }

// canceledStreamOnceProvider streams a single canceled-context error chunk, then closes.
type canceledStreamOnceProvider struct{}

func (canceledStreamOnceProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("chat completion not used")
}

func (canceledStreamOnceProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	out := make(chan provider.ChatChunk, 1)
	err := fmt.Errorf("stream chunk: %w", context.Canceled)
	out <- provider.ChatChunk{Error: err.Error(), OriginalError: err}
	close(out)
	return out, nil
}

func (canceledStreamOnceProvider) SupportsUsageStats() bool { return true }

func setupCodexAuthFixture(t *testing.T) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(`{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","account_id":"acct-123"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
}

func stubCodexWSConstructors(t *testing.T, newProvider func() provider.Provider) *int {
	t.Helper()
	oldWS := newCodexResponsesWS
	t.Cleanup(func() {
		newCodexResponsesWS = oldWS
	})
	count := 0
	newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
		count++
		return newProvider(), nil
	}
	return &count
}

func codexResolvedModel(alias string) provider.ResolvedModel {
	return provider.ResolvedModel{
		Alias:                 alias,
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex, Codex: config.CodexConfig{Transport: config.CodexTransportWebSocket}},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
}

func newCodexRunner(t *testing.T, cacheKey string) cliRunner {
	t.Helper()
	rt := cliRuntime{
		providerFactory: buildRuntimeProviderFactory(config.Config{}, nil, nil),
		codexWSCache:    &codexWSCache{instances: make(map[string]provider.Provider)},
	}
	return cliRunner{
		runtime:          rt,
		promptCacheKeyFn: func() string { return cacheKey },
	}
}

func TestRuntimeProviderCachesCodexWSAcrossCalls(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return &fakeProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")

	first, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	second, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if first != second {
		t.Fatalf("runtimeProvider() returned different instances on repeated calls")
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls = %d, want 1", *callCount)
	}
}

func TestRuntimeProviderCacheMissOnAliasOrKeyChange(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return &fakeProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rmA := codexResolvedModel("codex-a")

	if _, err := r.runtimeProvider(rmA); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}

	rmB := codexResolvedModel("codex-b")
	if _, err := r.runtimeProvider(rmB); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 2 {
		t.Fatalf("constructor calls after alias change = %d, want 2", *callCount)
	}

	r2 := newCodexRunner(t, "cache-key-2")
	r2.runtime.codexWSCache = r.runtime.codexWSCache
	if _, err := r2.runtimeProvider(rmA); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 3 {
		t.Fatalf("constructor calls after prompt cache key change = %d, want 3", *callCount)
	}
}

func TestProviderFactoryBypassesCacheForDirectCalls(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return &fakeProvider{} })

	factory := buildRuntimeProviderFactory(config.Config{}, nil, nil)
	rm := codexResolvedModel("codex")

	for i := 0; i < 3; i++ {
		if _, err := factory(rm, "test-session"); err != nil {
			t.Fatalf("factory() error = %v", err)
		}
	}
	if *callCount != 3 {
		t.Fatalf("constructor calls = %d, want 3 (no caching on direct providerFactory calls)", *callCount)
	}
}

func TestRuntimeProviderEvictsOnStreamError(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return errStreamOnceProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls = %d, want 1", *callCount)
	}

	chunks, err := prov.StreamChatCompletion(context.Background(), provider.ChatRequest{})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	for range chunks {
	}

	if _, err := r.runtimeProvider(rm); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 2 {
		t.Fatalf("constructor calls after stream error = %d, want 2 (cache evicted)", *callCount)
	}
}

func TestRuntimeProviderEvictsOnChatCompletionError(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return &fakeProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls = %d, want 1", *callCount)
	}

	// fakeProvider with no configured responses returns an error.
	if _, err := prov.ChatCompletion(context.Background(), provider.ChatRequest{}); err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}

	if _, err := r.runtimeProvider(rm); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 2 {
		t.Fatalf("constructor calls after ChatCompletion error = %d, want 2 (cache evicted)", *callCount)
	}
}

func TestRuntimeProviderKeepsCacheOnChatCompletionContextCanceled(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return canceledChatProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls = %d, want 1", *callCount)
	}

	if _, err := prov.ChatCompletion(context.Background(), provider.ChatRequest{}); err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}

	if _, err := r.runtimeProvider(rm); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls after context-canceled ChatCompletion error = %d, want 1 (cache not evicted)", *callCount)
	}
}

func TestRuntimeProviderKeepsCacheOnStreamContextCanceled(t *testing.T) {
	setupCodexAuthFixture(t)
	callCount := stubCodexWSConstructors(t, func() provider.Provider { return canceledStreamOnceProvider{} })

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls = %d, want 1", *callCount)
	}

	chunks, err := prov.StreamChatCompletion(context.Background(), provider.ChatRequest{})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	for range chunks {
	}

	if _, err := r.runtimeProvider(rm); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	if *callCount != 1 {
		t.Fatalf("constructor calls after context-canceled stream error = %d, want 1 (cache not evicted)", *callCount)
	}
}

func TestRuntimeProviderCodexHTTPNeverCached(t *testing.T) {
	setupCodexAuthFixture(t)
	oldHTTP := newCodexResponses
	t.Cleanup(func() { newCodexResponses = oldHTTP })
	httpCallCount := 0
	newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
		httpCallCount++
		return &fakeProvider{}, nil
	}

	r := newCodexRunner(t, "cache-key-1")
	rm := codexResolvedModel("codex")
	rm.ProviderConfig.Codex = config.CodexConfig{Transport: config.CodexTransportHTTP}

	for i := 0; i < 3; i++ {
		if _, err := r.runtimeProvider(rm); err != nil {
			t.Fatalf("runtimeProvider() error = %v", err)
		}
	}
	if httpCallCount != 3 {
		t.Fatalf("HTTP constructor calls = %d, want 3 (no caching for HTTP transport)", httpCallCount)
	}
}
