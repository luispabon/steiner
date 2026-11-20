package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/usagestats"
)

func floatPtr(v float64) *float64 { return &v }

func TestUsageRatesCopiesPrice(t *testing.T) {
	tests := []struct {
		name  string
		price metadata.ModelPrice
		want  usagestats.Rates
	}{
		{
			name:  "base rates only",
			price: metadata.ModelPrice{Input: 3, Output: 15},
			want:  usagestats.Rates{Input: 3, Output: 15},
		},
		{
			name: "cache rates",
			price: metadata.ModelPrice{
				Input: 1, Output: 2, CacheRead: floatPtr(0.1), CacheWrite: floatPtr(0.2),
			},
			want: usagestats.Rates{Input: 1, Output: 2, CacheRead: floatPtr(0.1), CacheWrite: floatPtr(0.2)},
		},
		{
			name: "context tiers",
			price: metadata.ModelPrice{
				Input: 1, Output: 2,
				Tiers: []metadata.PriceTier{{Size: 272000, Input: floatPtr(2), Output: floatPtr(3)}},
			},
			want: usagestats.Rates{
				Input: 1, Output: 2,
				Tiers: []usagestats.RateTier{{Size: 272000, Input: floatPtr(2), Output: floatPtr(3)}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usageRates(tt.price)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("usageRates() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// priceResult is one scripted answer for recordingLookup.
type priceResult struct {
	price     *metadata.ModelPrice
	available bool
}

// recordingLookup returns a priceLookup that records how often it is called and
// answers from a per-call plan, falling back to the last result.
func recordingLookup(calls *int, results ...priceResult) priceLookup {
	return func(context.Context, string, config.ProviderType, string) (*metadata.ModelPrice, bool) {
		index := min(*calls, len(results)-1)
		(*calls)++
		return results[index].price, results[index].available
	}
}

func TestUsagePricerMemoizesPricedModels(t *testing.T) {
	calls := 0
	price := &metadata.ModelPrice{Input: 3, Output: 15}
	pricer := &usagePricer{
		lookup: recordingLookup(&calls, priceResult{price: price, available: true}),
		prices: make(map[usagePriceKey]usagePrice),
	}
	obs := usagestats.Observation{ProviderAlias: "alpha", ProviderType: string(config.ProviderTypeAnthropic), BackendModelID: "claude-sonnet-5-5"}

	for i := 0; i < 2; i++ {
		rates, ok := pricer.Price(obs)
		if !ok || rates.Input != 3 || rates.Output != 15 {
			t.Fatalf("call %d rates = (%#v, %t), want the fixture rates", i, rates, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("lookup calls = %d, want 1 after memoizing the model", calls)
	}

	// A different model is a different key and pays one more lookup.
	obs.BackendModelID = "other"
	if _, ok := pricer.Price(obs); !ok {
		t.Fatal("second model ok = false, want true")
	}
	if calls != 2 {
		t.Fatalf("lookup calls = %d, want 2 after a second model", calls)
	}
}

func TestUsagePricerMemoizesUnpricedModels(t *testing.T) {
	calls := 0
	pricer := &usagePricer{
		lookup: recordingLookup(&calls, priceResult{available: true}),
		prices: make(map[usagePriceKey]usagePrice),
	}
	obs := usagestats.Observation{ProviderAlias: "alpha", ProviderType: string(config.ProviderTypeOllama), BackendModelID: "llama3"}

	for i := 0; i < 2; i++ {
		if rates, ok := pricer.Price(obs); ok || !reflect.DeepEqual(rates, usagestats.Rates{}) {
			t.Fatalf("call %d = (%#v, %t), want unpriced", i, rates, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("lookup calls = %d, want 1 for a consulted index with no price", calls)
	}
}

func TestUsagePricerDoesNotMemoizeUnavailableIndex(t *testing.T) {
	calls := 0
	price := &metadata.ModelPrice{Input: 1, Output: 2}
	pricer := &usagePricer{
		lookup: recordingLookup(&calls,
			priceResult{available: false},
			priceResult{price: price, available: true},
		),
		prices: make(map[usagePriceKey]usagePrice),
	}
	obs := usagestats.Observation{ProviderAlias: "alpha", ProviderType: string(config.ProviderTypeOpenAI), BackendModelID: "gpt-6-luna"}

	if _, ok := pricer.Price(obs); ok {
		t.Fatal("first call ok = true, want unpriced while models.dev is unavailable")
	}
	rates, ok := pricer.Price(obs)
	if !ok || rates.Input != 1 || rates.Output != 2 {
		t.Fatalf("second call = (%#v, %t), want the price once metadata is available", rates, ok)
	}
	if calls != 2 {
		t.Fatalf("lookup calls = %d, want 2 because the unavailable result must not be cached", calls)
	}
}

func TestUsagePricerNilLookup(t *testing.T) {
	pricer := &usagePricer{}
	if rates, ok := pricer.Price(usagestats.Observation{}); ok || !reflect.DeepEqual(rates, usagestats.Rates{}) {
		t.Fatalf("nil lookup = (%#v, %t), want unpriced", rates, ok)
	}
	var nilResolver *usagePricer
	if _, ok := nilResolver.Price(usagestats.Observation{}); ok {
		t.Fatal("nil pricer ok = true, want unpriced")
	}
}
