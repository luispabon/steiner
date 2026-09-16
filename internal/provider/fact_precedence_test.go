package provider

import (
	"context"
	"testing"
)

func TestResolveFactsPrecedenceAllFieldsAndSourceCombinations(t *testing.T) {
	all := ModelFacts{
		ContextWindow:     Fact[int]{Value: 1, Known: true, Source: FactSourceConfig},
		MaxOutputTokens:   Fact[int]{Value: 2, Known: true, Source: FactSourceConfig},
		Vision:            Fact[bool]{Value: true, Known: true, Source: FactSourceConfig},
		ReasoningEfforts:  Fact[[]string]{Value: []string{"low"}, Known: true, Source: FactSourceConfig},
		ReasoningEchoBack: Fact[bool]{Value: true, Known: true, Source: FactSourceConfig},
		Transport:         Fact[transportChoice]{Value: transportChoice{Transport: TransportConfigured}, Known: true, Source: FactSourceConfig},
	}
	catalog := all
	catalog.ContextWindow.Value, catalog.ContextWindow.Source = 3, FactSourceCatalog
	catalog.MaxOutputTokens.Value, catalog.MaxOutputTokens.Source = 4, FactSourceCatalog
	catalog.ReasoningEfforts.Value, catalog.ReasoningEfforts.Source = []string{"medium"}, FactSourceCatalog
	modelsDev := catalog
	modelsDev.ContextWindow.Value, modelsDev.ContextWindow.Source = 5, FactSourceModelsDev
	modelsDev.MaxOutputTokens.Value, modelsDev.MaxOutputTokens.Source = 6, FactSourceModelsDev
	modelsDev.Vision.Value, modelsDev.Vision.Source = false, FactSourceModelsDev
	modelsDev.ReasoningEfforts.Value, modelsDev.ReasoningEfforts.Source = []string{"high"}, FactSourceModelsDev
	modelsDev.ReasoningEchoBack.Value, modelsDev.ReasoningEchoBack.Source = false, FactSourceModelsDev
	modelsDev.Transport.Value, modelsDev.Transport.Source = transportChoice{Transport: TransportConfigured}, FactSourceModelsDev
	fallback := ModelFacts{
		ContextWindow:     Fact[int]{Value: 7, Known: true, Source: FactSourceFallback},
		MaxOutputTokens:   Fact[int]{Value: 8, Known: true, Source: FactSourceFallback},
		Vision:            Fact[bool]{Value: true, Known: true, Source: FactSourceModelsDev},
		ReasoningEfforts:  Fact[[]string]{Value: []string{"fallback"}, Known: true, Source: FactSourceFallback},
		ReasoningEchoBack: Fact[bool]{Value: true, Known: true, Source: FactSourceModelsDev},
	}
	cases := []struct {
		name    string
		sources []factSource
		want    ModelFacts
	}{
		{"config beats catalog and models.dev", []factSource{&fixedFactsSource{source: FactSourceConfig, facts: all}, &fixedFactsSource{source: FactSourceCatalog, facts: catalog}, &fixedFactsSource{source: FactSourceModelsDev, facts: modelsDev}}, all},
		{"catalog fills fields before models.dev", []factSource{&fixedFactsSource{source: FactSourceCatalog, facts: catalog}, &fixedFactsSource{source: FactSourceModelsDev, facts: modelsDev}}, ModelFacts{ContextWindow: catalog.ContextWindow, MaxOutputTokens: catalog.MaxOutputTokens, Vision: modelsDev.Vision, ReasoningEfforts: catalog.ReasoningEfforts, ReasoningEchoBack: modelsDev.ReasoningEchoBack, Transport: modelsDev.Transport}},
		{"models.dev fills fields before fallback", []factSource{&fixedFactsSource{source: FactSourceModelsDev, facts: modelsDev}, &fixedFactsSource{source: FactSourceFallback, facts: fallback}}, modelsDev},
		{"fallback answers remaining limits", []factSource{&fixedFactsSource{source: FactSourceFallback, facts: fallback}}, ModelFacts{ContextWindow: fallback.ContextWindow, MaxOutputTokens: fallback.MaxOutputTokens, ReasoningEfforts: fallback.ReasoningEfforts, Transport: Fact[transportChoice]{Known: true, Source: FactSourceFallback}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := resolveFacts(context.Background(), modelRef{}, tc.sources)
			if got.ContextWindow.Source != tc.want.ContextWindow.Source || got.ContextWindow.Value != tc.want.ContextWindow.Value || got.MaxOutputTokens.Source != tc.want.MaxOutputTokens.Source || got.MaxOutputTokens.Value != tc.want.MaxOutputTokens.Value || got.Vision.Source != tc.want.Vision.Source || got.ReasoningEfforts.Source != tc.want.ReasoningEfforts.Source || got.ReasoningEchoBack.Source != tc.want.ReasoningEchoBack.Source || got.Transport.Source != tc.want.Transport.Source {
				t.Fatalf("facts = %+v, want precedence result %+v", got, tc.want)
			}
		})
	}
}

type fixedFactsSource struct {
	source FactSource
	facts  ModelFacts
}

func (s *fixedFactsSource) name() FactSource { return s.source }
func (s *fixedFactsSource) resolve(context.Context, modelRef, fieldSet) sourceResult {
	return sourceResult{facts: s.facts}
}
