package metadata

import (
	"fmt"
	"os"
	"reflect"
	"testing"
)

func pricePtr(v float64) *float64 { return &v }

// TestParseIndexPrice exercises price parsing against a models.dev-shaped
// fixture: absent, malformed, zero, cache-key gaps, and context tiers.
func TestParseIndexPrice(t *testing.T) {
	data, err := os.ReadFile("testdata/models_dev_cost.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	idx := ParseIndex(data)
	if idx.Malformed() {
		t.Fatal("fixture parsed as malformed")
	}

	tests := []struct {
		name  string
		model string
		want  *ModelPrice
	}{
		{
			name:  "single context tier with legacy duplicate ignored",
			model: "gpt-6-luna",
			want: &ModelPrice{
				Input: 0.1, Output: 0.5,
				CacheRead: pricePtr(0.01), CacheWrite: pricePtr(0.125),
				Tiers: []PriceTier{{
					Size:  272000,
					Input: pricePtr(0.2), Output: pricePtr(0.75),
					CacheRead: pricePtr(0.02), CacheWrite: pricePtr(0.25),
				}},
			},
		},
		{name: "no cost key", model: "image-no-cost"},
		{name: "non-object cost", model: "non-numeric-cost"},
		{name: "cost without output", model: "partial-cost"},
		{
			name:  "all-zero cost is a free price",
			model: "zero-cost",
			want:  &ModelPrice{Input: 0, Output: 0, CacheRead: pricePtr(0)},
		},
		{
			name:  "cache write missing",
			model: "no-cache-write",
			want:  &ModelPrice{Input: 1.25, Output: 10, CacheRead: pricePtr(0.125)},
		},
		{
			name:  "cache read missing",
			model: "no-cache-read",
			want:  &ModelPrice{Input: 1, Output: 2, CacheWrite: pricePtr(0.5)},
		},
		{
			name:  "tier without cache write",
			model: "grok-style",
			want: &ModelPrice{
				Input: 1.25, Output: 2.5, CacheRead: pricePtr(0.2),
				Tiers: []PriceTier{{
					Size: 200000, Input: pricePtr(2.5), Output: pricePtr(5), CacheRead: pricePtr(0.4),
				}},
			},
		},
		{
			name:  "two tiers sorted ascending",
			model: "multi-tier",
			want: &ModelPrice{
				Input: 0.46376812, Output: 2.31884058, CacheRead: pricePtr(0.09275362),
				Tiers: []PriceTier{
					{Size: 32000, Input: pricePtr(0.69565217), Output: pricePtr(3.47826087), CacheRead: pricePtr(0.13913043)},
					{Size: 128000, Input: pricePtr(1.39130435), Output: pricePtr(6.95652174), CacheRead: pricePtr(0.27826087)},
				},
			},
		},
		{
			name:  "tier omitting keys keeps pointers nil",
			model: "tier-partial-override",
			want: &ModelPrice{
				Input: 1, Output: 2, CacheRead: pricePtr(0.1), CacheWrite: pricePtr(0.2),
				Tiers: []PriceTier{{Size: 1000, Output: pricePtr(3)}},
			},
		},
		{
			name:  "non-context and zero-size tiers dropped",
			model: "tier-non-context",
			want: &ModelPrice{
				Input: 1, Output: 2,
				Tiers: []PriceTier{{Size: 500, Input: pricePtr(4)}},
			},
		},
		{
			name:  "duplicate tier size keeps the first",
			model: "tier-duplicate-size",
			want: &ModelPrice{
				Input: 1, Output: 2,
				Tiers: []PriceTier{
					{Size: 500, Input: pricePtr(1.5)},
					{Size: 1000, Input: pricePtr(2)},
				},
			},
		},
		{
			name:  "malformed tier dropped alone",
			model: "tier-malformed-entry",
			want: &ModelPrice{
				Input: 1, Output: 2,
				Tiers: []PriceTier{{Size: 2000, Input: pricePtr(1.5)}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := idx.LookupProvider("openai", tc.model)
			if !result.Info.Found {
				t.Fatalf("LookupProvider(%q) = %+v, want a found match", tc.model, result)
			}
			if !reflect.DeepEqual(result.Info.Price, tc.want) {
				t.Errorf("Price = %s, want %s", formatPrice(result.Info.Price), formatPrice(tc.want))
			}
		})
	}
}

// TestParseIndexPriceKeepsOtherMetadata proves a malformed cost does not drop
// the model's remaining metadata.
func TestParseIndexPriceKeepsOtherMetadata(t *testing.T) {
	data, err := os.ReadFile("testdata/models_dev_cost.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	idx := ParseIndex(data)
	for _, model := range []string{"non-numeric-cost", "partial-cost"} {
		info := idx.LookupProvider("openai", model).Info
		if !info.Found || info.ContextWindow != 8000 || info.MaxOutputTokens != 1000 {
			t.Errorf("%s info = %+v, want found with ContextWindow 8000 and MaxOutputTokens 1000", model, info)
		}
		if info.Price != nil {
			t.Errorf("%s Price = %s, want nil", model, formatPrice(info.Price))
		}
	}
}

// TestLookupMergedPrice covers price exposure through both lookup paths. A
// merged result keeps the first priced entry in sorted provider order, since
// prices are not blended across providers.
func TestLookupMergedPrice(t *testing.T) {
	data, err := os.ReadFile("testdata/models_dev_cost.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	idx := ParseIndex(data)

	merged := idx.LookupMerged("merged-cost-model")
	if merged.Reason != LookupReasonMerged {
		t.Fatalf("Reason = %q, want %q", merged.Reason, LookupReasonMerged)
	}
	want := &ModelPrice{Input: 2, Output: 4}
	if !reflect.DeepEqual(merged.Info.Price, want) {
		t.Errorf("merged Price = %s, want %s", formatPrice(merged.Info.Price), formatPrice(want))
	}

	single := idx.LookupMerged("merged-unpriced")
	if !single.Info.Found || single.Info.Price != nil {
		t.Errorf("merged-unpriced result = %+v, want found with nil Price", single.Info)
	}
}

// formatPrice renders a price for failure messages; reflect.DeepEqual already
// reports structural differences, but pointer fields read better dereferenced.
func formatPrice(p *ModelPrice) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("input=%v output=%v cache_read=%v cache_write=%v tiers=%d", p.Input, p.Output, derefFloat(p.CacheRead), derefFloat(p.CacheWrite), len(p.Tiers))
}

func derefFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
