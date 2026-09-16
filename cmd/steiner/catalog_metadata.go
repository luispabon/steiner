package main

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/provider"
)

// catalogMetadataAdapter adapts modelcatalog.Service to provider.ModelCatalog,
// snapshotting cfg at construction time (matching how modelCatalogEndpoints
// is derived once from cfg in buildModelCatalogService) rather than threading
// a live config getter through every lookup. This means catalog lookups use
// the config as it existed when the adapter was built, not live config.
type catalogMetadataAdapter struct {
	service *modelcatalog.Service
	cfg     *config.Config
}

// newCatalogMetadataAdapter returns a catalogMetadataAdapter wrapping service
// and a snapshot of cfg.
func newCatalogMetadataAdapter(service *modelcatalog.Service, cfg *config.Config) *catalogMetadataAdapter {
	return &catalogMetadataAdapter{service: service, cfg: catalogConfigCopy(cfg)}
}

// CatalogModel implements provider.ModelCatalog. It routes the lookup through
// catalogConfigCopy so Codex providers are looked up under the ChatGPT
// backend fingerprint the catalog cache actually stores.
func (a *catalogMetadataAdapter) CatalogModel(providerAlias, modelID string) (provider.CatalogModel, bool) {
	if a == nil || a.service == nil {
		return provider.CatalogModel{}, false
	}
	model, ok := a.service.Model(a.cfg, providerAlias, modelID)
	if !ok {
		return provider.CatalogModel{}, false
	}
	return provider.CatalogModel{
		ContextWindow:    model.ContextLength,
		MaxOutputTokens:  model.MaxOutputTokens,
		SupportedEfforts: model.SupportedEfforts,
	}, true
}
