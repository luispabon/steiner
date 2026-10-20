package interactive

import (
	"slices"

	"github.com/luispabon/steiner/internal/config"
)

// ModelProjection holds the model lookup tables the TUI derives from the
// configured model definitions. Every field is nil when no models are defined.
type ModelProjection struct {
	// AliasNames lists the model aliases in sorted order.
	AliasNames []string
	// BackendAliases maps each model's backend ID to its alias.
	BackendAliases map[string]string
	// ContextSizes maps each alias with a positive context window to that window.
	ContextSizes map[string]int
	// BaseURLs maps each alias whose provider is configured to that provider's base URL.
	BaseURLs map[string]string
	// ProviderNames maps each alias whose provider is configured to the provider name.
	ProviderNames map[string]string
}

// BuildModelProjection derives a ModelProjection from cfg in one pass over the
// model definitions.
func BuildModelProjection(cfg config.Config) ModelProjection {
	defs := cfg.Models.Definitions
	if len(defs) == 0 {
		return ModelProjection{}
	}
	p := ModelProjection{
		AliasNames:     make([]string, 0, len(defs)),
		BackendAliases: make(map[string]string, len(defs)),
		ContextSizes:   make(map[string]int, len(defs)),
		BaseURLs:       make(map[string]string, len(defs)),
		ProviderNames:  make(map[string]string, len(defs)),
	}
	for alias, model := range defs {
		p.AliasNames = append(p.AliasNames, alias)
		p.BackendAliases[model.ID] = alias
		if model.Advanced.Limits.ContextWindow > 0 {
			p.ContextSizes[alias] = model.Advanced.Limits.ContextWindow
		}
		if provider, ok := cfg.Providers[model.Provider]; ok {
			p.BaseURLs[alias] = provider.BaseURL
			p.ProviderNames[alias] = model.Provider
		}
	}
	slices.Sort(p.AliasNames)
	return p
}

// SortedProfileNames returns the profile names in sorted order. It returns an
// empty, non-nil slice when profiles is empty.
func SortedProfileNames(profiles map[string]config.ModelProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
