package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// fakeModelCatalog is a test double for ModelCatalog keyed by
// "providerAlias\x00modelID".
type fakeModelCatalog map[string]CatalogModel

func (f fakeModelCatalog) CatalogModel(providerAlias, modelID string) (CatalogModel, bool) {
	m, ok := f[providerAlias+"\x00"+modelID]
	return m, ok
}

func TestCatalogSource_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		catalog ModelCatalog
		ref     modelRef
		want    fieldSet
		wantCW  Fact[int]
		wantMO  Fact[int]
	}{
		{
			name:    "nil catalog leaves fields unknown",
			catalog: nil,
			ref:     modelRef{ProviderAlias: "router", BackendModelID: "openai/gpt-4o"},
			want:    fieldSet(fieldContextWindow | fieldMaxOutput),
		},
		{
			name:    "lookup miss leaves fields unknown",
			catalog: fakeModelCatalog{},
			ref:     modelRef{ProviderAlias: "router", BackendModelID: "openai/gpt-4o"},
			want:    fieldSet(fieldContextWindow | fieldMaxOutput),
		},
		{
			name: "lookup hit answers wanted fields",
			catalog: fakeModelCatalog{
				"router\x00openai/gpt-4o": {ContextWindow: 128000, MaxOutputTokens: 16384},
			},
			ref:    modelRef{ProviderAlias: "router", BackendModelID: "openai/gpt-4o"},
			want:   fieldSet(fieldContextWindow | fieldMaxOutput),
			wantCW: Fact[int]{Value: 128000, Known: true, Source: FactSourceCatalog, Confidence: "high"},
			wantMO: Fact[int]{Value: 16384, Known: true, Source: FactSourceCatalog, Confidence: "high"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := catalogSource{catalog: tc.catalog}
			res := s.resolve(context.Background(), tc.ref, tc.want)
			if res.facts.ContextWindow != tc.wantCW {
				t.Errorf("ContextWindow = %+v, want %+v", res.facts.ContextWindow, tc.wantCW)
			}
			if res.facts.MaxOutputTokens != tc.wantMO {
				t.Errorf("MaxOutputTokens = %+v, want %+v", res.facts.MaxOutputTokens, tc.wantMO)
			}
			if res.sourceErr != "" {
				t.Errorf("sourceErr = %q, want none", res.sourceErr)
			}
		})
	}
}

func TestResolveWithDiscoveryCatalogBeatsModelsDevPerField(t *testing.T) {
	writeModelsDevCache(t, `{"openai":{"models":{"gpt-5-codex":{"limit":{"context":1050000,"output":128000}}}}}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"codexmodel": {Provider: "codex", ID: "gpt-5-codex"},
			},
		},
	}

	catalog := fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 272000}}
	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "codexmodel", true, http.DefaultClient, nil, catalog)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if got, want := rm.Facts.ContextWindow.Value, 272000; got != want {
		t.Fatalf("ContextWindow value = %d, want %d", got, want)
	}
	if got, want := rm.Facts.ContextWindow.Source, FactSourceCatalog; got != want {
		t.Fatalf("ContextWindow source = %q, want %q", got, want)
	}
	if got, want := rm.Facts.MaxOutputTokens.Value, 128000; got != want {
		t.Fatalf("MaxOutputTokens value = %d, want %d", got, want)
	}
	if got, want := rm.Facts.MaxOutputTokens.Source, FactSourceModelsDev; got != want {
		t.Fatalf("MaxOutputTokens source = %q, want %q", got, want)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", rm.Warnings)
	}
}

func TestResolveWithDiscoveryCatalogWinsOverModelsDevAllFields(t *testing.T) {
	writeModelsDevCache(t, `{"openrouter":{"models":{"openai/gpt-4o":{"limit":{"context":64000,"output":4096}}}}}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"router": {Type: config.ProviderTypeOpenRouter, BaseURL: "http://localhost:1"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"gpt4o": {Provider: "router", ID: "openai/gpt-4o"},
			},
		},
	}

	catalog := fakeModelCatalog{
		"router\x00openai/gpt-4o": {ContextWindow: 128000, MaxOutputTokens: 16384, SupportedEfforts: []string{"low", "high"}},
	}
	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "gpt4o", true, http.DefaultClient, nil, catalog)
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if got, want := rm.Facts.ContextWindow.Value, 128000; got != want {
		t.Fatalf("ContextWindow value = %d, want %d", got, want)
	}
	if got, want := rm.Facts.MaxOutputTokens.Value, 16384; got != want {
		t.Fatalf("MaxOutputTokens value = %d, want %d", got, want)
	}
	if got, want := rm.Facts.ContextWindow.Source, FactSourceCatalog; got != want {
		t.Fatalf("ContextWindow source = %q, want %q", got, want)
	}
	if len(rm.Facts.ReasoningEfforts.Value) != 2 || rm.Facts.ReasoningEfforts.Source != FactSourceCatalog {
		t.Fatalf("ReasoningEfforts = %+v, want catalog-sourced [low high]", rm.Facts.ReasoningEfforts)
	}
}

func TestResolveWithDiscoveryCatalogMissFallsThroughToModelsDev(t *testing.T) {
	writeModelsDevCache(t, `{"openrouter":{"models":{"openai/gpt-4o":{"limit":{"context":64000,"output":4096}}}}}`)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"router": {Type: config.ProviderTypeOpenRouter, BaseURL: "http://localhost:1"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"gpt4o": {Provider: "router", ID: "openai/gpt-4o"},
			},
		},
	}

	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "gpt4o", true, http.DefaultClient, nil, fakeModelCatalog{})
	if err != nil {
		t.Fatalf("resolveReferenceWithLoader() error = %v", err)
	}
	if got, want := rm.Facts.ContextWindow.Value, 64000; got != want {
		t.Fatalf("ContextWindow value = %d, want %d", got, want)
	}
	if got, want := rm.Facts.ContextWindow.Source, FactSourceModelsDev; got != want {
		t.Fatalf("ContextWindow source = %q, want %q", got, want)
	}
}
