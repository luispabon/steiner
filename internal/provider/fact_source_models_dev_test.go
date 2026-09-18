package provider

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// countingFailTransport records how many times RoundTrip is invoked while
// always failing, so tests can assert on whether a network load attempt
// happened at all (as opposed to alwaysFailTransport, which only proves
// resolution survives a failure, not whether the failure ever occurred).
type countingFailTransport struct {
	calls atomic.Int32
}

func (t *countingFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, fmt.Errorf("offline")
}

// TestResolveReferenceNeverLoadsModelsDevWhenFullyConfigured proves end-to-end
// laziness: a fully-configured model resolved via resolveReference must never
// trigger a models.dev cache load attempt (file or network), unlike the
// field-level laziness already covered by TestResolveFactsLaziness's fakes.
func TestResolveReferenceNeverLoadsModelsDevWhenFullyConfigured(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	transport := &countingFailTransport{}

	vision := true
	echoBack := false
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"full": {
				Provider: "local",
				ID:       "some-model",
				Vision:   &vision,
				Advanced: config.AdvancedConfig{
					Limits:            config.AdvancedLimitsConfig{ContextWindow: 128000, MaxOutputTokens: 8192},
					Reasoning:         config.ReasoningConfig{SupportedEfforts: []string{"low", "high"}},
					ReasoningEchoBack: &echoBack,
					Transport:         config.ModelTransportOpenAICompat,
				},
			},
		}},
	}

	rm, err := resolveReference(&cfg, "full", true, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("resolveReference() error = %v", err)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", rm.Warnings)
	}
	if got := transport.calls.Load(); got != 0 {
		t.Fatalf("transport.calls = %d, want 0 (models.dev must never be loaded)", got)
	}
}

// fixtureLoader returns a modelsDevLoader backed by a fixed JSON fixture, for
// tests that inject models.dev data directly.
func fixtureLoader(t *testing.T, data []byte) *modelsDevLoader {
	t.Helper()
	return newModelsDevLoaderWithFunc(func(context.Context) metadata.LoadResult {
		return metadata.LoadResult{Data: data}
	})
}

// TestModelsDevSource_GenericProviderMergesAcrossProviders proves a generic
// (openai_compat/ollama/litellm) provider profile whose alias isn't itself a
// models.dev provider key falls back to a cross-provider merge, taking the
// most conservative limits at "low" confidence and never overriding transport.
func TestModelsDevSource_GenericProviderMergesAcrossProviders(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte(`{
		"openai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":64000}}}},
		"abacus":{"models":{"gpt-5.6-luna":{"limit":{"context":1000000,"output":32000}}}},
		"302ai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":64000}}}}
	}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"mygateway": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:9999/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"m": {Provider: "mygateway", ID: "gpt-5.6-luna"},
		}},
	}

	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "m", true, &http.Client{}, fixtureLoader(t, data), nil)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if got := rm.Facts.ContextWindow.Value; got != 1000000 {
		t.Errorf("ContextWindow = %d, want 1000000", got)
	}
	if got := rm.Facts.ContextWindow.Source; got != FactSourceModelsDev {
		t.Errorf("Source = %q, want %q", got, FactSourceModelsDev)
	}
	if got := rm.Facts.ContextWindow.Confidence; got != "low" {
		t.Errorf("Confidence = %q, want %q", got, "low")
	}
	if rm.EffectiveTransport != TransportConfigured {
		t.Errorf("EffectiveTransport = %q, want %q", rm.EffectiveTransport, TransportConfigured)
	}
	if len(rm.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", rm.Warnings)
	}
}

// TestModelsDevSource_GenericProviderAliasedAsRealProviderKeyUsesStrictLookup
// proves that when a generic provider's alias happens to match a real
// models.dev provider key, the strict lookup under that key is used instead
// of the cross-provider merge.
func TestModelsDevSource_GenericProviderAliasedAsRealProviderKeyUsesStrictLookup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte(`{
		"openrouter":{"models":{"gpt-5.6-luna":{"limit":{"context":900000,"output":16000}}}},
		"abacus":{"models":{"gpt-5.6-luna":{"limit":{"context":1000000,"output":32000}}}}
	}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"openrouter": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:9999/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"m": {Provider: "openrouter", ID: "gpt-5.6-luna"},
		}},
	}

	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "m", true, &http.Client{}, fixtureLoader(t, data), nil)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if got := rm.Facts.ContextWindow.Value; got != 900000 {
		t.Errorf("ContextWindow = %d, want 900000 (strict lookup under alias, not merged)", got)
	}
	if got := rm.Facts.ContextWindow.Confidence; got != "medium" {
		t.Errorf("Confidence = %q, want %q (strict lookup, not merged)", got, "medium")
	}
}

func TestModelsDevSourceEffortsDeepCopy(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte(`{"openai":{"models":{"gpt-5-x":{"reasoning_options":[{"type":"effort","values":["low","high"]}]}}}}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"openai": {Type: config.ProviderTypeOpenAI},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"m": {Provider: "openai", ID: "gpt-5-x"},
		}},
	}

	// Share a single loader across both resolutions, matching production
	// usage where one Resolver's loader is reused across many resolutions in
	// a session — this is what makes aliasing into the shared models.dev
	// index observable.
	loader := fixtureLoader(t, data)

	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "m", true, &http.Client{}, loader, nil)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if len(rm.Facts.ReasoningEfforts.Value) != 2 {
		t.Fatalf("efforts len = %d, want 2", len(rm.Facts.ReasoningEfforts.Value))
	}

	rm.Facts.ReasoningEfforts.Value[0] = "mutated"

	rm2, err := resolveReferenceWithLoader(context.Background(), &cfg, "m", true, &http.Client{}, loader, nil)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if rm2.Facts.ReasoningEfforts.Value[0] != "low" {
		t.Errorf("models.dev index was mutated: efforts[0] = %q, want low", rm2.Facts.ReasoningEfforts.Value[0])
	}
}
