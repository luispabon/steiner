package provider

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
)

// configSource answers facts directly from user config.
type configSource struct{}

func (configSource) name() FactSource { return FactSourceConfig }

func (configSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	var facts ModelFacts
	adv := ref.ModelConfig.Advanced

	if want&fieldSet(fieldContextWindow) != 0 && adv.Limits.ContextWindow > 0 && (ref.Provider.Type != config.ProviderTypeCodex || adv.Limits.ContextWindowExplicit()) {
		facts.ContextWindow = Fact[int]{Value: adv.Limits.ContextWindow, Known: true, Source: FactSourceConfig, Confidence: "high"}
	}
	if want&fieldSet(fieldMaxOutput) != 0 && adv.Limits.MaxOutputTokens > 0 {
		facts.MaxOutputTokens = Fact[int]{Value: adv.Limits.MaxOutputTokens, Known: true, Source: FactSourceConfig, Confidence: "high"}
	}
	if want&fieldSet(fieldVision) != 0 && ref.ModelConfig.Vision != nil {
		facts.Vision = Fact[bool]{Value: *ref.ModelConfig.Vision, Known: true, Source: FactSourceConfig, Confidence: "high"}
	}
	if want&fieldSet(fieldEfforts) != 0 && len(adv.Reasoning.SupportedEfforts) > 0 {
		facts.ReasoningEfforts = Fact[[]string]{
			Value: copyStrings(adv.Reasoning.SupportedEfforts), Known: true, Source: FactSourceConfig, Confidence: "high",
		}
	}
	if want&fieldSet(fieldEchoBack) != 0 && adv.ReasoningEchoBack != nil {
		facts.ReasoningEchoBack = Fact[bool]{Value: *adv.ReasoningEchoBack, Known: true, Source: FactSourceConfig, Confidence: "high"}
	}
	if want&fieldSet(fieldTransport) != 0 {
		if tc, ok := configTransportOverride(adv.Transport); ok {
			facts.Transport = Fact[transportChoice]{Value: tc, Known: true, Source: FactSourceConfig, Confidence: "high"}
		}
	}

	return sourceResult{facts: facts}
}

// configTransportOverride returns the transportChoice for an explicit
// advanced.transport config override, mirroring resolveEffectiveTransport's
// override switch.
func configTransportOverride(override config.ModelTransportType) (transportChoice, bool) {
	switch override {
	case config.ModelTransportOpenAICompat:
		return transportChoice{
			ProviderType: config.ProviderTypeOpenAICompat, Transport: TransportOpenAICompat,
			Reason: "explicit config override",
		}, true
	case config.ModelTransportAnthropic:
		return transportChoice{
			ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic,
			Reason: "explicit config override",
		}, true
	}
	return transportChoice{}, false
}

// providerFixedSource answers Transport for providers with a fixed
// transport (currently only Codex). It reports FactSourceConfig because,
// like an explicit config override, the Codex transport choice is
// deterministic from ProviderConfig.Type rather than a network lookup; this
// label is provisional pending stage B2's full transport precedence model.
type providerFixedSource struct{}

func (providerFixedSource) name() FactSource { return FactSourceConfig }

func (providerFixedSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	var facts ModelFacts
	if want&fieldSet(fieldTransport) != 0 && ref.Profile.FixedTransport != nil {
		facts.Transport = Fact[transportChoice]{
			Value: *ref.Profile.FixedTransport, Known: true, Source: FactSourceConfig, Confidence: "high",
		}
	}
	return sourceResult{facts: facts}
}
