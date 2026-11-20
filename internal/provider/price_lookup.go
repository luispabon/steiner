package provider

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// ModelPrice returns the models.dev price for one model, applying the same
// strict/generic lookup rules as the models.dev fact source, with one
// difference: a generic alias that falls back to a cross-provider merge is
// unpriced, because another provider's rates do not estimate the configured
// backend. available reports whether the index could be consulted at all: it
// is false while the index has not loaded (or parsed as malformed), so a
// caller that memoizes results must not cache that miss, because a later call
// may succeed once metadata loads.
//
// ModelPrice never starts or waits on a models.dev load, because it runs on the
// usage recorder's goroutine. WarmModelsDev loads the index in the background.
func (r *Resolver) ModelPrice(providerAlias string, providerType config.ProviderType, backendModelID string) (*metadata.ModelPrice, bool) {
	if r == nil || r.mdLoader == nil {
		return nil, false
	}
	idx, _, loaded := r.mdLoader.loadedIndex()
	if !loaded || idx == nil || idx.Malformed() {
		return nil, false
	}
	return lookupModelPrice(idx, providerAlias, providerType, backendModelID), true
}

// lookupModelPrice resolves one model's price from a parsed index. Named
// provider profiles use their fixed models.dev provider ID; generic profiles
// try the alias as a models.dev provider key and are unpriced when that misses,
// because a cross-provider merge cannot tell which reseller's rates apply. A
// nil result means the index carries no price for the model.
func lookupModelPrice(idx *metadata.Index, providerAlias string, providerType config.ProviderType, backendModelID string) *metadata.ModelPrice {
	if idx == nil || idx.Malformed() || backendModelID == "" {
		return nil
	}
	ref := modelRef{
		ProviderAlias:  providerAlias,
		BackendModelID: backendModelID,
		Profile:        profileFor(providerType),
	}
	lookup, merged := modelsDevLookup(idx, ref)
	if merged {
		return nil
	}
	return lookup.Info.Price
}
