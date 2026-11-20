package provider

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// ModelPrice returns the models.dev price for one model, applying the same
// strict/generic lookup rules as the models.dev fact source. available reports
// whether the index could be consulted at all: it is false while the cache has
// not loaded (or parsed as malformed), so a caller that memoizes results must
// not cache that miss, because a later call may succeed once metadata loads.
func (r *Resolver) ModelPrice(ctx context.Context, providerAlias string, providerType config.ProviderType, backendModelID string) (price *metadata.ModelPrice, available bool) {
	if r == nil || r.mdLoader == nil {
		return nil, false
	}
	idx, _ := r.mdLoader.load(ctx)
	if idx == nil || idx.Malformed() {
		return nil, false
	}
	return lookupModelPrice(idx, providerAlias, providerType, backendModelID), true
}

// lookupModelPrice resolves one model's price from a parsed index. Named
// provider profiles use their fixed models.dev provider ID; generic profiles
// try the alias as a models.dev provider key and fall back to a cross-provider
// merge, exactly as modelsDevLookup does for model facts. A nil result means
// the index carries no price for the model.
func lookupModelPrice(idx *metadata.Index, providerAlias string, providerType config.ProviderType, backendModelID string) *metadata.ModelPrice {
	if idx == nil || idx.Malformed() || backendModelID == "" {
		return nil
	}
	ref := modelRef{
		ProviderAlias:  providerAlias,
		BackendModelID: backendModelID,
		Profile:        profileFor(providerType),
	}
	lookup, _ := modelsDevLookup(idx, ref)
	return lookup.Info.Price
}
