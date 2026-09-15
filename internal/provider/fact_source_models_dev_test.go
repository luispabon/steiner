package provider

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
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

// TestResolveReasoningBatchLoadsModelsDevAtMostOnce proves the shared
// modelsDevLoader loads the cache at most once across a whole batch, even
// when multiple aliases need models.dev facts.
func TestResolveReasoningBatchLoadsModelsDevAtMostOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	transport := &countingFailTransport{}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"one": {Provider: "local", ID: "model-one"},
			"two": {Provider: "local", ID: "model-two"},
		}},
	}

	ResolveReasoningBatch(cfg, &http.Client{Transport: transport})

	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("transport.calls = %d, want exactly 1 (loaded once, shared across aliases)", got)
	}
}
