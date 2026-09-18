package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// fakeModelCatalog is a test double for ModelCatalog keyed by
// "providerAlias\x00modelID".
type roundTripFuncCatalog func(*http.Request) (*http.Response, error)

func (f roundTripFuncCatalog) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
			name:    "Codex opt-in selects max context",
			catalog: fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 128000, MaxContextWindow: 256000}},
			ref:     modelRef{ProviderAlias: "codex", BackendModelID: "gpt-5-codex", Provider: config.ProviderConfig{Type: config.ProviderTypeCodex}, ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Codex: config.ModelCodexConfig{UseMaxContextWindow: true}}}},
			want:    fieldSet(fieldContextWindow),
			wantCW:  Fact[int]{Value: 256000, Known: true, Source: FactSourceCatalog, Confidence: "high"},
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

func TestCatalogCodexContextSelection(t *testing.T) {
	tests := []struct {
		name   string
		model  CatalogModel
		useMax bool
		want   int
	}{
		{name: "default normal", model: CatalogModel{ContextWindow: 128000, MaxContextWindow: 256000}, want: 128000},
		{name: "opt in max", model: CatalogModel{ContextWindow: 128000, MaxContextWindow: 256000}, useMax: true, want: 256000},
		{name: "missing max normal", model: CatalogModel{ContextWindow: 128000}, useMax: true, want: 128000},
		{name: "nonpositive max normal", model: CatalogModel{ContextWindow: 128000, MaxContextWindow: -1}, useMax: true, want: 128000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := modelRef{ProviderAlias: "codex", BackendModelID: "model", Provider: config.ProviderConfig{Type: config.ProviderTypeCodex}, ModelConfig: config.ModelConfig{Advanced: config.AdvancedConfig{Codex: config.ModelCodexConfig{UseMaxContextWindow: tc.useMax}}}}
			res := (catalogSource{catalog: fakeModelCatalog{"codex\x00model": tc.model}}).resolve(context.Background(), ref, fieldSet(fieldContextWindow))
			if got := res.facts.ContextWindow.Value; got != tc.want {
				t.Fatalf("context = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestResolveCodexCatalogNormalContextForDefaultAndFalse(t *testing.T) {
	for _, useMax := range []bool{false} {
		t.Run(fmt.Sprintf("use_max=%t", useMax), func(t *testing.T) {
			cfg := config.Config{Providers: map[string]config.ProviderConfig{"codex": {Type: config.ProviderTypeCodex}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"default": {Provider: "codex", ID: "gpt-5-codex", Advanced: config.AdvancedConfig{Codex: config.ModelCodexConfig{UseMaxContextWindow: useMax}}}}}}
			catalog := fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 128000, MaxContextWindow: 256000}}
			rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "default", true, http.DefaultClient, nil, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if rm.Facts.ContextWindow.Value != 128000 || rm.EffectiveLimits.ContextWindow != 128000 {
				t.Fatalf("context=%+v limits=%+v", rm.Facts.ContextWindow, rm.EffectiveLimits)
			}
		})
	}
}

func TestResolveCatalogUnusableContextFallsThroughToModelsDevAndFallback(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"codex": {Type: config.ProviderTypeCodex}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"model": {Provider: "codex", ID: "gpt-5-codex"}}}}
	catalog := fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 0, MaxContextWindow: -1}}
	writeModelsDevCache(t, `{"openai":{"models":{"gpt-5-codex":{"limit":{"context":900000}}}}}`)
	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "model", true, http.DefaultClient, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Facts.ContextWindow.Value != 900000 || rm.Facts.ContextWindow.Source != FactSourceModelsDev {
		t.Fatalf("models.dev context=%+v", rm.Facts.ContextWindow)
	}
	loader := newModelsDevLoaderWithFunc(func(context.Context) metadata.LoadResult { return metadata.LoadResult{} })
	rm, err = resolveReferenceWithLoader(context.Background(), &cfg, "model", true, http.DefaultClient, loader, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Facts.ContextWindow.Value != 32768 || rm.Facts.ContextWindow.Source != FactSourceFallback {
		t.Fatalf("fallback context=%+v", rm.Facts.ContextWindow)
	}
}

func TestResolveDirectNewModelConfigBaseCodexUsesCatalogContext(t *testing.T) {
	model := config.NewModelConfigBase()
	model.Provider = "codex"
	model.ID = "gpt-5-codex"
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"codex": {Type: config.ProviderTypeCodex}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"default": model}}}
	catalog := fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 128000, MaxContextWindow: 256000}}
	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "default", true, http.DefaultClient, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Facts.ContextWindow.Value != 128000 || rm.Facts.ContextWindow.Source != FactSourceCatalog {
		t.Fatalf("context=%+v, want catalog normal context", rm.Facts.ContextWindow)
	}
}

func TestResolveReferenceCatalogMissThenOllamaProbeSuppliesContext(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"ollama": {Type: config.ProviderTypeOllama, BaseURL: "http://ollama"}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"model": {Provider: "ollama", ID: "llama"}}}}
	catalog := fakeModelCatalog{"ollama\x00llama": {MaxOutputTokens: 2048}}
	client := &http.Client{Transport: roundTripFuncCatalog(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"model_info":{"general.context_length":65536}}`)), Header: http.Header{}, Request: req}, nil
	})}
	loader := newModelsDevLoaderWithFunc(func(context.Context) metadata.LoadResult { return metadata.LoadResult{} })
	rm, err := resolveReferenceWithLoader(context.Background(), &cfg, "model", true, client, loader, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Facts.ContextWindow.Value != 65536 || rm.Facts.ContextWindow.Source != FactSourceDiscovery {
		t.Fatalf("context = %+v, want Ollama discovery 65536", rm.Facts.ContextWindow)
	}
	if rm.Facts.MaxOutputTokens.Value != 2048 || rm.Facts.MaxOutputTokens.Source != FactSourceCatalog {
		t.Fatalf("output = %+v, want catalog 2048", rm.Facts.MaxOutputTokens)
	}
}

func TestResolveModelMetadataCatalogBeatsModelsDevPerField(t *testing.T) {
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

func TestResolveModelMetadataCatalogWinsOverModelsDevAllFields(t *testing.T) {
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

func TestResolveModelMetadataCatalogMissFallsThroughToModelsDev(t *testing.T) {
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

func TestCatalogSourceEffortsDeepCopy(t *testing.T) {
	catalog := fakeModelCatalog{
		"router\x00model": {SupportedEfforts: []string{"low", "high"}},
	}
	s := catalogSource{catalog: catalog}
	ref := modelRef{ProviderAlias: "router", BackendModelID: "model"}
	res := s.resolve(context.Background(), ref, fieldSet(fieldEfforts))

	if len(res.facts.ReasoningEfforts.Value) != 2 {
		t.Fatalf("efforts len = %d, want 2", len(res.facts.ReasoningEfforts.Value))
	}

	res.facts.ReasoningEfforts.Value[0] = "mutated"

	res2 := s.resolve(context.Background(), ref, fieldSet(fieldEfforts))
	if res2.facts.ReasoningEfforts.Value[0] != "low" {
		t.Errorf("catalog source was mutated: efforts[0] = %q, want low", res2.facts.ReasoningEfforts.Value[0])
	}
}
