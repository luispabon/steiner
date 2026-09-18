package provider

import (
	"context"
	"slices"

	"github.com/luispabon/steiner/internal/config"
)

// CatalogModel is the subset of enumerated provider-catalog data a catalog
// lookup can contribute to fact resolution.
type CatalogModel struct {
	ContextWindow    int
	MaxContextWindow int
	MaxOutputTokens  int
	SupportedEfforts []string
}

// ModelCatalog looks up enumerated model metadata from a provider catalog
// (e.g. an OpenRouter or Codex model list). Implemented outside this package
// (internal/provider must not import internal/modelcatalog) and injected via
// ResolverOptions.
type ModelCatalog interface {
	CatalogModel(providerAlias, modelID string) (CatalogModel, bool)
}

// catalogSource answers ContextWindow, MaxOutputTokens, and ReasoningEfforts
// from an injected provider model catalog. A nil catalog or a lookup miss
// leaves all fields unknown, with no sourceErr — catalog data is best-effort,
// like probeSource.
type catalogSource struct {
	catalog ModelCatalog
}

func (catalogSource) name() FactSource { return FactSourceCatalog }

func (s catalogSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	if s.catalog == nil {
		return sourceResult{}
	}
	model, ok := s.catalog.CatalogModel(ref.ProviderAlias, ref.BackendModelID)
	if !ok {
		return sourceResult{}
	}

	var facts ModelFacts
	if want&fieldSet(fieldContextWindow) != 0 {
		contextWindow := model.ContextWindow
		if ref.Provider.Type == config.ProviderTypeCodex && ref.ModelConfig.Advanced.Codex.UseMaxContextWindow && model.MaxContextWindow > 0 {
			contextWindow = model.MaxContextWindow
		}
		if contextWindow > 0 {
			facts.ContextWindow = Fact[int]{Value: contextWindow, Known: true, Source: FactSourceCatalog, Confidence: "high"}
		}
	}
	if want&fieldSet(fieldMaxOutput) != 0 && model.MaxOutputTokens > 0 {
		facts.MaxOutputTokens = Fact[int]{Value: model.MaxOutputTokens, Known: true, Source: FactSourceCatalog, Confidence: "high"}
	}
	if want&fieldSet(fieldEfforts) != 0 && len(model.SupportedEfforts) > 0 {
		facts.ReasoningEfforts = Fact[[]string]{Value: slices.Clone(model.SupportedEfforts), Known: true, Source: FactSourceCatalog, Confidence: "high"}
	}
	return sourceResult{facts: facts}
}
