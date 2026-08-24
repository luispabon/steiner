package provider

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

func TestResolveReferenceParity(t *testing.T) {
	// A configured alias with no explicit advanced.limits (as real YAML
	// unmarshaling produces) must resolve identically to the equivalent raw
	// provider/model-id reference — both fall back to the same unconfigured
	// defaults rather than a raw ref losing discoverability to a baked-in base.
	model := config.NewModelConfigBase()
	model.Provider = "local"
	model.ID = "gpt-4"
	model.Advanced.Limits = config.AdvancedLimitsConfig{}
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"configured": model,
		}},
	}

	alias, err := Resolve(cfg, "configured")
	if err != nil {
		t.Fatalf("Resolve(alias) error = %v", err)
	}
	raw, err := Resolve(cfg, "local/gpt-4")
	if err != nil {
		t.Fatalf("Resolve(reference) error = %v", err)
	}
	if !reflect.DeepEqual(alias.EffectiveLimits, raw.EffectiveLimits) {
		t.Fatalf("EffectiveLimits differ: alias=%#v raw=%#v", alias.EffectiveLimits, raw.EffectiveLimits)
	}
	if !reflect.DeepEqual(alias.Retry, raw.Retry) {
		t.Fatalf("Retry differs: alias=%#v raw=%#v", alias.Retry, raw.Retry)
	}
}

func TestResolveReferenceWarningIsNeutral(t *testing.T) {
	rm := ResolvedModel{
		Alias:           "local/custom-model",
		BackendModelID:  "custom-model",
		EffectiveLimits: resolveEffectiveLimits(config.AdvancedLimitsConfig{}),
		ProviderConfig:  config.ProviderConfig{Type: config.ProviderTypeOpenAICompat},
	}
	resolveLimitsFromDiscovery(&rm, config.AdvancedLimitsConfig{}, metadata.ModelInfo{}, nil, false, "local/custom-model")

	if len(rm.Warnings) != 1 {
		t.Fatalf("Warnings len = %d, want 1", len(rm.Warnings))
	}
	if strings.Contains(rm.Warnings[0], "models.") {
		t.Fatalf("raw reference warning suggests editing a definition: %q", rm.Warnings[0])
	}
}

func TestResolveReferenceUsesLongestProviderPrefix(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"open":       {Type: config.ProviderTypeOpenAICompat},
			"openrouter": {Type: config.ProviderTypeOpenRouter},
		},
	}

	rm, err := Resolve(cfg, "openrouter/openai/gpt-4")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := rm.ProviderAlias, "openrouter"; got != want {
		t.Fatalf("ProviderAlias = %q, want %q", got, want)
	}
	if got, want := rm.BackendModelID, "openai/gpt-4"; got != want {
		t.Fatalf("BackendModelID = %q, want %q", got, want)
	}
}

func TestResolveReferenceAliasWins(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat}},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"local/model": {Provider: "local", ID: "configured-id"},
		}},
	}

	rm, err := Resolve(cfg, "local/model")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := rm.BackendModelID, "configured-id"; got != want {
		t.Fatalf("BackendModelID = %q, want %q", got, want)
	}
}

func TestResolveReferenceInvalid(t *testing.T) {
	_, err := Resolve(config.Config{}, "missing/model")
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
	if got, want := err.Error(), `model alias "missing/model" not found`; got != want {
		t.Fatalf("Resolve() error = %q, want %q", got, want)
	}
}

func TestResolveWithDiscoveryReferenceConsumerShapes(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
	}
	cases := []struct {
		name string
		ref  string
	}{
		{name: "main session wiring", ref: "local/session-model"},
		{name: "advisor", ref: "local/advisor-model"},
		{name: "delegation registry", ref: "local/delegation-model"},
		{name: "sub-agent model resolver", ref: "local/sub-agent-model"},
		{name: "oneshot phase fallback", ref: "local/oneshot-model"},
		{name: "workflow handoff", ref: "local/handoff-model"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rm, err := ResolveWithDiscovery(cfg, tt.ref, nil)
			if err != nil {
				t.Fatalf("ResolveWithDiscovery() error = %v", err)
			}
			if got, want := rm.ProviderAlias, "local"; got != want {
				t.Fatalf("ProviderAlias = %q, want %q", got, want)
			}
			if got, want := rm.BackendModelID, strings.TrimPrefix(tt.ref, "local/"); got != want {
				t.Fatalf("BackendModelID = %q, want %q", got, want)
			}
		})
	}
}
