package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestResolveFactsPrecedenceChains(t *testing.T) {
	codexTransport := transportChoice{
		ProviderType: config.ProviderTypeCodex,
		Transport:    TransportConfigured,
		Reason:       "codex provider uses OAuth Responses transport",
	}

	tests := []struct {
		name    string
		field   factField
		ref     modelRef
		sources []factSource
		want    ModelFacts
	}{
		{
			name:  "context config beats every lower tier",
			field: fieldContextWindow,
			sources: []factSource{
				intSource(FactSourceConfig, fieldContextWindow, 100),
				intSource(FactSourceCatalog, fieldContextWindow, 200),
				intSource(FactSourceDiscovery, fieldContextWindow, 300),
				intSource(FactSourceModelsDev, fieldContextWindow, 400),
				intSource(FactSourceFallback, fieldContextWindow, 500),
			},
			want: ModelFacts{ContextWindow: intFact(100, FactSourceConfig)},
		},
		{
			name:  "context catalog wins without config",
			field: fieldContextWindow,
			sources: []factSource{
				intSource(FactSourceCatalog, fieldContextWindow, 200),
				intSource(FactSourceDiscovery, fieldContextWindow, 300),
				intSource(FactSourceModelsDev, fieldContextWindow, 400),
				intSource(FactSourceFallback, fieldContextWindow, 500),
			},
			want: ModelFacts{ContextWindow: intFact(200, FactSourceCatalog)},
		},
		{
			name:  "context discovery wins without config or catalog",
			field: fieldContextWindow,
			sources: []factSource{
				intSource(FactSourceDiscovery, fieldContextWindow, 300),
				intSource(FactSourceModelsDev, fieldContextWindow, 400),
				intSource(FactSourceFallback, fieldContextWindow, 500),
			},
			want: ModelFacts{ContextWindow: intFact(300, FactSourceDiscovery)},
		},
		{
			name:  "context models.dev wins without higher tiers",
			field: fieldContextWindow,
			sources: []factSource{
				intSource(FactSourceModelsDev, fieldContextWindow, 400),
				intSource(FactSourceFallback, fieldContextWindow, 500),
			},
			want: ModelFacts{ContextWindow: intFact(400, FactSourceModelsDev)},
		},
		{
			name:  "context conservative fallback wins without sources",
			field: fieldContextWindow,
			want:  ModelFacts{ContextWindow: Fact[int]{Value: defaultContextWindow, Known: true, Source: FactSourceFallback, Confidence: "low", Note: "conservative default"}},
		},
		{
			name:  "max output config beats every lower tier",
			field: fieldMaxOutput,
			sources: []factSource{
				intSource(FactSourceConfig, fieldMaxOutput, 101),
				intSource(FactSourceCatalog, fieldMaxOutput, 201),
				intSource(FactSourceModelsDev, fieldMaxOutput, 401),
				intSource(FactSourceFallback, fieldMaxOutput, 501),
			},
			want: ModelFacts{MaxOutputTokens: intFact(101, FactSourceConfig)},
		},
		{
			name:  "max output catalog wins without config",
			field: fieldMaxOutput,
			sources: []factSource{
				intSource(FactSourceCatalog, fieldMaxOutput, 201),
				intSource(FactSourceModelsDev, fieldMaxOutput, 401),
				intSource(FactSourceFallback, fieldMaxOutput, 501),
			},
			want: ModelFacts{MaxOutputTokens: intFact(201, FactSourceCatalog)},
		},
		{
			name:  "max output models.dev wins without higher tiers",
			field: fieldMaxOutput,
			sources: []factSource{
				intSource(FactSourceModelsDev, fieldMaxOutput, 401),
				intSource(FactSourceFallback, fieldMaxOutput, 501),
			},
			want: ModelFacts{MaxOutputTokens: intFact(401, FactSourceModelsDev)},
		},
		{
			name:  "max output conservative fallback wins without sources",
			field: fieldMaxOutput,
			want: ModelFacts{
				ContextWindow:   Fact[int]{Value: defaultContextWindow, Known: true, Source: FactSourceFallback, Confidence: "low", Note: "conservative default"},
				MaxOutputTokens: Fact[int]{Value: defaultMaxOutputTokens, Known: true, Source: FactSourceFallback, Confidence: "low", Note: "conservative default"},
			},
		},
		{
			name:  "efforts config beats lower tiers",
			field: fieldEfforts,
			sources: []factSource{
				effortSource(FactSourceConfig, "config"),
				effortSource(FactSourceCatalog, "catalog"),
				effortSource(FactSourceModelsDev, "models.dev"),
				effortSource(FactSourceFallback, "fallback"),
			},
			want: ModelFacts{ReasoningEfforts: effortFact("config", FactSourceConfig)},
		},
		{
			name:    "efforts catalog wins without config",
			field:   fieldEfforts,
			sources: []factSource{effortSource(FactSourceCatalog, "catalog"), effortSource(FactSourceModelsDev, "models.dev"), effortSource(FactSourceFallback, "fallback")},
			want:    ModelFacts{ReasoningEfforts: effortFact("catalog", FactSourceCatalog)},
		},
		{
			name:    "efforts models.dev wins without higher tiers",
			field:   fieldEfforts,
			sources: []factSource{effortSource(FactSourceModelsDev, "models.dev"), effortSource(FactSourceFallback, "fallback")},
			want:    ModelFacts{ReasoningEfforts: effortFact("models.dev", FactSourceModelsDev)},
		},
		{
			name:    "efforts fallback wins without higher tiers",
			field:   fieldEfforts,
			sources: []factSource{effortSource(FactSourceFallback, "fallback")},
			want:    ModelFacts{ReasoningEfforts: effortFact("fallback", FactSourceFallback)},
		},
		{
			name:    "vision config beats models.dev",
			field:   fieldVision,
			sources: []factSource{boolSource(FactSourceConfig, false), boolSource(FactSourceModelsDev, true)},
			want:    ModelFacts{Vision: boolFact(false, FactSourceConfig)},
		},
		{
			name:    "vision models.dev wins without config",
			field:   fieldVision,
			sources: []factSource{boolSource(FactSourceModelsDev, true)},
			want:    ModelFacts{Vision: boolFact(true, FactSourceModelsDev)},
		},
		{
			name:    "echo back config beats models.dev",
			field:   fieldEchoBack,
			sources: []factSource{boolSource(FactSourceConfig, false), boolSource(FactSourceModelsDev, true)},
			want:    ModelFacts{ReasoningEchoBack: boolFact(false, FactSourceConfig)},
		},
		{
			name:    "echo back models.dev wins without config",
			field:   fieldEchoBack,
			sources: []factSource{boolSource(FactSourceModelsDev, true)},
			want:    ModelFacts{ReasoningEchoBack: boolFact(true, FactSourceModelsDev)},
		},
		{
			name:  "transport explicit config beats models.dev",
			field: fieldTransport,
			ref: modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{
				Transport: config.ModelTransportAnthropic,
			}}},
			sources: []factSource{configSource{}, transportSource(FactSourceModelsDev, transportChoice{ProviderType: config.ProviderTypeOpenAICompat, Transport: TransportOpenAICompat})},
			want:    ModelFacts{Transport: transportFact(transportChoice{ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic, Reason: "explicit config override"}, FactSourceConfig)},
		},
		{
			name:    "transport provider-fixed beats models.dev",
			field:   fieldTransport,
			ref:     modelRef{Profile: providerProfile{FixedTransport: &codexTransport}},
			sources: []factSource{providerFixedSource{}, transportSource(FactSourceModelsDev, transportChoice{ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic})},
			want:    ModelFacts{Transport: transportFact(codexTransport, FactSourceConfig)},
		},
		{
			name:    "transport models.dev wins without higher tiers",
			field:   fieldTransport,
			ref:     modelRef{Provider: config.ProviderConfig{Type: config.ProviderTypeOpenAICompat}},
			sources: []factSource{transportSource(FactSourceModelsDev, transportChoice{ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic})},
			want:    ModelFacts{Transport: transportFact(transportChoice{ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic}, FactSourceModelsDev)},
		},
		{
			name:  "transport configured fallback wins without sources",
			field: fieldTransport,
			ref:   modelRef{Provider: config.ProviderConfig{Type: config.ProviderTypeOpenAICompat}},
			want:  ModelFacts{Transport: transportFact(transportChoice{ProviderType: config.ProviderTypeOpenAICompat, Transport: TransportConfigured, Reason: "none"}, FactSourceFallback)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := resolveFacts(context.Background(), tc.ref, tc.sources)
			assertFact(t, tc.field, got, tc.want)
		})
	}
}

type fixedFactsSource struct {
	source FactSource
	facts  ModelFacts
}

func (s *fixedFactsSource) name() FactSource { return s.source }

func (s *fixedFactsSource) resolve(_ context.Context, _ modelRef, want fieldSet) sourceResult {
	var facts ModelFacts
	if want&fieldSet(fieldContextWindow) != 0 {
		facts.ContextWindow = s.facts.ContextWindow
	}
	if want&fieldSet(fieldMaxOutput) != 0 {
		facts.MaxOutputTokens = s.facts.MaxOutputTokens
	}
	if want&fieldSet(fieldVision) != 0 {
		facts.Vision = s.facts.Vision
	}
	if want&fieldSet(fieldEfforts) != 0 {
		facts.ReasoningEfforts = s.facts.ReasoningEfforts
	}
	if want&fieldSet(fieldEchoBack) != 0 {
		facts.ReasoningEchoBack = s.facts.ReasoningEchoBack
	}
	if want&fieldSet(fieldTransport) != 0 {
		facts.Transport = s.facts.Transport
	}
	return sourceResult{facts: facts}
}

func intSource(source FactSource, field factField, value int) factSource {
	facts := ModelFacts{}
	if field == fieldContextWindow {
		facts.ContextWindow = intFact(value, source)
	} else {
		facts.MaxOutputTokens = intFact(value, source)
	}
	return &fixedFactsSource{source: source, facts: facts}
}

func effortSource(source FactSource, value string) factSource {
	return &fixedFactsSource{source: source, facts: ModelFacts{ReasoningEfforts: effortFact(value, source)}}
}

func boolSource(source FactSource, value bool) factSource {
	return &fixedFactsSource{source: source, facts: ModelFacts{
		Vision:            boolFact(value, source),
		ReasoningEchoBack: boolFact(value, source),
	}}
}

func transportSource(source FactSource, value transportChoice) factSource {
	return &fixedFactsSource{source: source, facts: ModelFacts{Transport: transportFact(value, source)}}
}

func intFact(value int, source FactSource) Fact[int] {
	return Fact[int]{Value: value, Known: true, Source: source}
}

func effortFact(value string, source FactSource) Fact[[]string] {
	return Fact[[]string]{Value: []string{value}, Known: true, Source: source}
}

func boolFact(value bool, source FactSource) Fact[bool] {
	return Fact[bool]{Value: value, Known: true, Source: source}
}

func transportFact(value transportChoice, source FactSource) Fact[transportChoice] {
	return Fact[transportChoice]{Value: value, Known: true, Source: source}
}

func assertFact(t *testing.T, field factField, got, want ModelFacts) {
	t.Helper()
	var gotValue, wantValue any
	var gotKnown, wantKnown bool
	var gotSource, wantSource FactSource
	switch field {
	case fieldContextWindow:
		gotValue, wantValue = got.ContextWindow.Value, want.ContextWindow.Value
		gotKnown, wantKnown = got.ContextWindow.Known, want.ContextWindow.Known
		gotSource, wantSource = got.ContextWindow.Source, want.ContextWindow.Source
	case fieldMaxOutput:
		gotValue, wantValue = got.MaxOutputTokens.Value, want.MaxOutputTokens.Value
		gotKnown, wantKnown = got.MaxOutputTokens.Known, want.MaxOutputTokens.Known
		gotSource, wantSource = got.MaxOutputTokens.Source, want.MaxOutputTokens.Source
	case fieldVision:
		gotValue, wantValue = got.Vision.Value, want.Vision.Value
		gotKnown, wantKnown = got.Vision.Known, want.Vision.Known
		gotSource, wantSource = got.Vision.Source, want.Vision.Source
	case fieldEfforts:
		gotValue, wantValue = got.ReasoningEfforts.Value, want.ReasoningEfforts.Value
		gotKnown, wantKnown = got.ReasoningEfforts.Known, want.ReasoningEfforts.Known
		gotSource, wantSource = got.ReasoningEfforts.Source, want.ReasoningEfforts.Source
	case fieldEchoBack:
		gotValue, wantValue = got.ReasoningEchoBack.Value, want.ReasoningEchoBack.Value
		gotKnown, wantKnown = got.ReasoningEchoBack.Known, want.ReasoningEchoBack.Known
		gotSource, wantSource = got.ReasoningEchoBack.Source, want.ReasoningEchoBack.Source
	case fieldTransport:
		gotValue, wantValue = got.Transport.Value, want.Transport.Value
		gotKnown, wantKnown = got.Transport.Known, want.Transport.Known
		gotSource, wantSource = got.Transport.Source, want.Transport.Source
	}
	if !gotKnown || !wantKnown || gotSource != wantSource || !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("resolved %q fact = known %v, source %q, value %v; want known %v, source %q, value %v", field, gotKnown, gotSource, gotValue, wantKnown, wantSource, wantValue)
	}
}
