package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestConfigSourceResolve(t *testing.T) {
	trueVal := true
	tests := []struct {
		name  string
		ref   modelRef
		field factField
		check func(t *testing.T, facts ModelFacts)
	}{
		{
			name:  "context window known",
			ref:   modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 128000}}}},
			field: fieldContextWindow,
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.ContextWindow.Known || facts.ContextWindow.Value != 128000 || facts.ContextWindow.Source != FactSourceConfig {
					t.Errorf("ContextWindow = %+v", facts.ContextWindow)
				}
			},
		},
		{
			name:  "context window unknown when zero",
			ref:   modelRef{},
			field: fieldContextWindow,
			check: func(t *testing.T, facts ModelFacts) {
				if facts.ContextWindow.Known {
					t.Errorf("ContextWindow.Known = true, want false")
				}
			},
		},
		{
			name:  "max output tokens known",
			ref:   modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{MaxOutputTokens: 8192}}}},
			field: fieldMaxOutput,
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.MaxOutputTokens.Known || facts.MaxOutputTokens.Value != 8192 {
					t.Errorf("MaxOutputTokens = %+v", facts.MaxOutputTokens)
				}
			},
		},
		{
			name:  "vision known",
			ref:   modelRef{ModelConfig: config.ModelConfig{Vision: &trueVal}},
			field: fieldVision,
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.Vision.Known || facts.Vision.Value != true {
					t.Errorf("Vision = %+v", facts.Vision)
				}
			},
		},
		{
			name:  "vision unknown when nil",
			ref:   modelRef{},
			field: fieldVision,
			check: func(t *testing.T, facts ModelFacts) {
				if facts.Vision.Known {
					t.Errorf("Vision.Known = true, want false")
				}
			},
		},
		{
			name: "reasoning efforts known",
			ref: modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{
				Reasoning: config.ReasoningConfig{SupportedEfforts: []string{"low", "high"}},
			}}},
			field: fieldEfforts,
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.ReasoningEfforts.Known || !reflect.DeepEqual(facts.ReasoningEfforts.Value, []string{"low", "high"}) {
					t.Errorf("ReasoningEfforts = %+v", facts.ReasoningEfforts)
				}
			},
		},
		{
			name:  "reasoning echo back known",
			ref:   modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{ReasoningEchoBack: &trueVal}}},
			field: fieldEchoBack,
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.ReasoningEchoBack.Known || facts.ReasoningEchoBack.Value != true {
					t.Errorf("ReasoningEchoBack = %+v", facts.ReasoningEchoBack)
				}
			},
		},
		{
			name:  "transport override openai_compat",
			ref:   modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Transport: config.ModelTransportOpenAICompat}}},
			field: fieldTransport,
			check: func(t *testing.T, facts ModelFacts) {
				want := transportChoice{ProviderType: config.ProviderTypeOpenAICompat, Transport: TransportOpenAICompat, Reason: "explicit config override"}
				if !facts.Transport.Known || facts.Transport.Value != want {
					t.Errorf("Transport = %+v, want %+v", facts.Transport, want)
				}
			},
		},
		{
			name:  "transport override anthropic",
			ref:   modelRef{ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Transport: config.ModelTransportAnthropic}}},
			field: fieldTransport,
			check: func(t *testing.T, facts ModelFacts) {
				want := transportChoice{ProviderType: config.ProviderTypeAnthropic, Transport: TransportAnthropic, Reason: "explicit config override"}
				if !facts.Transport.Known || facts.Transport.Value != want {
					t.Errorf("Transport = %+v, want %+v", facts.Transport, want)
				}
			},
		},
		{
			name:  "transport unknown when no override",
			ref:   modelRef{},
			field: fieldTransport,
			check: func(t *testing.T, facts ModelFacts) {
				if facts.Transport.Known {
					t.Errorf("Transport.Known = true, want false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := configSource{}.resolve(context.Background(), tt.ref, fieldSet(tt.field))
			tt.check(t, res.facts)
		})
	}
}

func TestProviderFixedSourceResolve(t *testing.T) {
	codexTransport := transportChoice{ProviderType: config.ProviderTypeCodex, Transport: TransportConfigured, Reason: "codex provider uses OAuth Responses transport"}

	tests := []struct {
		name  string
		ref   modelRef
		check func(t *testing.T, facts ModelFacts)
	}{
		{
			name: "codex profile answers transport",
			ref:  modelRef{Profile: providerProfile{FixedTransport: &codexTransport}},
			check: func(t *testing.T, facts ModelFacts) {
				if !facts.Transport.Known || facts.Transport.Value != codexTransport {
					t.Errorf("Transport = %+v, want %+v", facts.Transport, codexTransport)
				}
			},
		},
		{
			name: "non-codex profile leaves transport unknown",
			ref:  modelRef{Profile: providerProfile{}},
			check: func(t *testing.T, facts ModelFacts) {
				if facts.Transport.Known {
					t.Errorf("Transport.Known = true, want false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := providerFixedSource{}.resolve(context.Background(), tt.ref, fieldSet(fieldTransport))
			tt.check(t, res.facts)
		})
	}
}
