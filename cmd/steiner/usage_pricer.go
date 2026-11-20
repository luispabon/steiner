package main

import (
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// priceLookup resolves one model's models.dev price. available is false when
// the models.dev index could not be consulted yet, as opposed to a consulted
// index carrying no price for the model.
type priceLookup func(providerAlias string, providerType config.ProviderType, backendModelID string) (*metadata.ModelPrice, bool)

// usagePricer implements usagestats.Pricer from models.dev prices served by the
// session's model resolver, so pricing shares the resolver's single models.dev
// index. Results are memoized per provider and model; an unavailable index is
// never memoized, so calls recorded before the warm-up finishes can still be
// priced later while the negative model lookup stays cached.
type usagePricer struct {
	lookup priceLookup

	mu     sync.Mutex
	prices map[usagePriceKey]usagePrice
}

type usagePriceKey struct {
	providerAlias          string
	configuredProviderType string
	backendModelID         string
}

// usagePrice is a memoized outcome: rates apply only when priced is true.
type usagePrice struct {
	rates  usagestats.Rates
	priced bool
}

// newUsagePricer returns a pricer backed by resolver's models.dev index.
func newUsagePricer(resolver *provider.Resolver) *usagePricer {
	return &usagePricer{lookup: resolver.ModelPrice, prices: make(map[usagePriceKey]usagePrice)}
}

// Price implements usagestats.Pricer. It keys on the configured provider type,
// the same source context-window resolution uses, because a transport override
// can change the effective type without changing which models.dev profile
// applies to the alias.
func (p *usagePricer) Price(obs usagestats.Observation) (usagestats.Rates, bool) {
	if p == nil || p.lookup == nil {
		return usagestats.Rates{}, false
	}
	key := usagePriceKey{
		providerAlias:          obs.ProviderAlias,
		configuredProviderType: obs.ConfiguredProviderType,
		backendModelID:         obs.BackendModelID,
	}
	p.mu.Lock()
	cached, ok := p.prices[key]
	p.mu.Unlock()
	if ok {
		return cached.rates, cached.priced
	}

	price, available := p.lookup(obs.ProviderAlias, config.ProviderType(obs.ConfiguredProviderType), obs.BackendModelID)
	if !available {
		return usagestats.Rates{}, false
	}
	entry := usagePrice{priced: price != nil}
	if price != nil {
		entry.rates = usageRates(*price)
	}
	p.mu.Lock()
	p.prices[key] = entry
	p.mu.Unlock()
	return entry.rates, entry.priced
}

// usageRates converts a models.dev price into per-million-token rates for one
// call, copying the context tiers so a caller cannot mutate the cached price.
func usageRates(price metadata.ModelPrice) usagestats.Rates {
	rates := usagestats.Rates{
		Input:      price.Input,
		Output:     price.Output,
		CacheRead:  price.CacheRead,
		CacheWrite: price.CacheWrite,
	}
	if len(price.Tiers) > 0 {
		rates.Tiers = make([]usagestats.RateTier, 0, len(price.Tiers))
		for _, tier := range price.Tiers {
			rates.Tiers = append(rates.Tiers, usagestats.RateTier{
				Size:       tier.Size,
				Input:      tier.Input,
				Output:     tier.Output,
				CacheRead:  tier.CacheRead,
				CacheWrite: tier.CacheWrite,
			})
		}
	}
	return rates
}
