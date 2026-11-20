package usagestats

import (
	"cmp"
	"math"
	"slices"
)

// Rates describes a model's price in US dollars per million tokens. Callers
// convert provider metadata into Rates; this package knows nothing about model
// catalogs.
type Rates struct {
	// Input is the uncached prompt token price.
	Input float64
	// Output is the completion token price. Reasoning tokens are part of the
	// completion count, so they are charged once, at this rate.
	Output float64
	// CacheRead is the cached prompt read price. Nil charges cache reads at
	// the effective Input rate.
	CacheRead *float64
	// CacheWrite is the cache write price. Nil charges cache writes at the
	// effective Input rate.
	CacheWrite *float64
	// Tiers holds context-length overrides. EstimateMicroUSD sorts them by
	// Size, so callers need not.
	Tiers []RateTier
}

// RateTier overrides Rates for requests whose prompt token count exceeds
// Size. A nil field keeps the base or lower-tier rate.
type RateTier struct {
	// Size is the prompt token threshold; a request applies the tier only when
	// its prompt token count is strictly greater.
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

// EstimateMicroUSD returns the estimated cost of one call in micro-US dollars
// (millionths of a dollar), rounded to the nearest micro. The prompt count is
// the total input: cache reads and writes are subtracted from it before the
// uncached input rate applies, and cache tokens falling outside it are ignored.
//
// A tier applies to the whole request once the prompt token count exceeds the
// tier size, replacing the base rates rather than pricing marginal tokens.
// Later, larger tiers override earlier ones field by field.
func (r Rates) EstimateMicroUSD(prompt, cacheRead, cacheWrite, completion int) int64 {
	prompt = max(prompt, 0)
	cacheRead = max(cacheRead, 0)
	cacheWrite = max(cacheWrite, 0)
	completion = max(completion, 0)

	effective := r
	tiers := slices.Clone(r.Tiers)
	slices.SortStableFunc(tiers, func(a, b RateTier) int { return cmp.Compare(a.Size, b.Size) })
	for _, tier := range tiers {
		if prompt <= tier.Size {
			continue
		}
		if tier.Input != nil {
			effective.Input = *tier.Input
		}
		if tier.Output != nil {
			effective.Output = *tier.Output
		}
		if tier.CacheRead != nil {
			effective.CacheRead = tier.CacheRead
		}
		if tier.CacheWrite != nil {
			effective.CacheWrite = tier.CacheWrite
		}
	}

	readRate := effective.Input
	if effective.CacheRead != nil {
		readRate = *effective.CacheRead
	}
	writeRate := effective.Input
	if effective.CacheWrite != nil {
		writeRate = *effective.CacheWrite
	}

	uncached := max(prompt-cacheRead-cacheWrite, 0)
	// Rates are per million tokens and this result is per millionth of a dollar,
	// so the token counts multiply the rates directly with no scaling step.
	micro := float64(uncached)*effective.Input +
		float64(cacheRead)*readRate +
		float64(cacheWrite)*writeRate +
		float64(completion)*effective.Output
	return int64(math.Round(micro))
}

// Pricer estimates rates for observations whose provider reported no cost.
// Price returns ok false when no price is known for the observation's model,
// which leaves the request counted as unpriced. Implementations run on the
// recording goroutine with no recorder lock held, so they must be safe for
// concurrent use.
type Pricer interface {
	Price(obs Observation) (Rates, bool)
}

// SetPricer installs the pricer Record consults for observations without a
// provider-reported cost. A nil Pricer clears it. It is safe to call after
// construction and concurrently with Record.
func (r *Recorder) SetPricer(p Pricer) {
	if p == nil {
		r.pricer.Store(nil)
		return
	}
	r.pricer.Store(&p)
}

// pricerOrNil returns the installed pricer, or nil when none is set.
func (r *Recorder) pricerOrNil() Pricer {
	p := r.pricer.Load()
	if p == nil {
		return nil
	}
	return *p
}
