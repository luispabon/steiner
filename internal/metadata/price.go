package metadata

import (
	"encoding/json"
	"sort"
)

// ModelPrice holds a models.dev model's prices in US dollars per million
// tokens. A nil *ModelPrice on ModelInfo means no usable price is known.
type ModelPrice struct {
	// Input is the uncached prompt token price.
	Input float64
	// Output is the completion token price. It covers reasoning tokens too,
	// since models.dev counts those inside completion tokens.
	Output float64
	// CacheRead is the cached prompt read price. Nil means the model carries
	// no separate cache read price.
	CacheRead *float64
	// CacheWrite is the cache write price. Nil means the model carries no
	// separate cache write price.
	CacheWrite *float64
	// Tiers holds context-length overrides sorted ascending by Size. Each
	// override replaces the base prices for a whole request whose prompt
	// token count exceeds PriceTier.Size.
	Tiers []PriceTier
}

// PriceTier overrides ModelPrice for requests whose prompt token count exceeds
// Size. A nil price field keeps the base or lower-tier price.
type PriceTier struct {
	// Size is the prompt token threshold above which this tier applies.
	Size int
	// Input is the uncached prompt token price override.
	Input *float64
	// Output is the completion token price override.
	Output *float64
	// CacheRead is the cached prompt read price override.
	CacheRead *float64
	// CacheWrite is the cache write price override.
	CacheWrite *float64
}

// parseModelPrice decodes a models.dev cost object. An absent or unusable
// object yields nil rather than an error, so a malformed cost never drops the
// rest of the model's metadata. Models.dev duplicates the first tier in a
// legacy context_over_200k object and prices audio and reasoning there too;
// both are ignored here.
func parseModelPrice(raw json.RawMessage) *ModelPrice {
	if len(raw) == 0 {
		return nil
	}
	var cost struct {
		Input      *float64          `json:"input"`
		Output     *float64          `json:"output"`
		CacheRead  *float64          `json:"cache_read"`
		CacheWrite *float64          `json:"cache_write"`
		Tiers      []json.RawMessage `json:"tiers"`
	}
	if err := json.Unmarshal(raw, &cost); err != nil {
		return nil
	}
	if cost.Input == nil || cost.Output == nil {
		return nil
	}
	price := &ModelPrice{
		Input:      *cost.Input,
		Output:     *cost.Output,
		CacheRead:  cost.CacheRead,
		CacheWrite: cost.CacheWrite,
	}
	seen := make(map[int]struct{}, len(cost.Tiers))
	for _, rawTier := range cost.Tiers {
		tier, ok := parsePriceTier(rawTier)
		if !ok {
			continue
		}
		if _, duplicate := seen[tier.Size]; duplicate {
			continue
		}
		seen[tier.Size] = struct{}{}
		price.Tiers = append(price.Tiers, tier)
	}
	sort.Slice(price.Tiers, func(i, j int) bool { return price.Tiers[i].Size < price.Tiers[j].Size })
	return price
}

// parsePriceTier decodes one cost tier, keeping it only when it is a positive-
// size context tier. A tier that fails to decode is dropped on its own instead
// of discarding the whole price.
func parsePriceTier(raw json.RawMessage) (PriceTier, bool) {
	var tier struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
		Tier       struct {
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"tier"`
	}
	if err := json.Unmarshal(raw, &tier); err != nil {
		return PriceTier{}, false
	}
	if tier.Tier.Type != "context" || tier.Tier.Size <= 0 {
		return PriceTier{}, false
	}
	return PriceTier{
		Size:       tier.Tier.Size,
		Input:      tier.Input,
		Output:     tier.Output,
		CacheRead:  tier.CacheRead,
		CacheWrite: tier.CacheWrite,
	}, true
}
