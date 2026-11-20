package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// priceLookupFixture is a models.dev-shaped document covering a named
// provider, an openrouter-style namespaced model ID, a model with no cost, and
// an all-zero (free) model.
const priceLookupFixture = `{
  "anthropic": {
    "npm": "@ai-sdk/anthropic",
    "models": {
      "claude-sonnet-5-5": {
        "cost": { "input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75 }
      },
      "claude-free": { "cost": { "input": 0, "output": 0 } },
      "claude-no-cost": { "limit": { "context": 200000, "output": 8192 } }
    }
  },
  "openai": {
    "npm": "@ai-sdk/openai",
    "models": {
      "gpt-6-luna": {
        "cost": {
          "input": 0.1,
          "output": 0.5,
          "cache_read": 0.01,
          "cache_write": 0.125,
          "tiers": [
            { "input": 0.2, "output": 0.75, "tier": { "type": "context", "size": 272000 } }
          ]
        }
      }
    }
  },
  "openrouter": {
    "npm": "@openrouter/ai-sdk-provider",
    "models": {
      "anthropic/claude-sonnet-5-5": { "cost": { "input": 3.2, "output": 16 } }
    }
  }
}`

func pricePtr(v float64) *float64 { return &v }

func TestLookupModelPrice(t *testing.T) {
	idx := metadata.ParseIndex([]byte(priceLookupFixture))
	if idx.Malformed() {
		t.Fatal("fixture parsed as malformed")
	}

	anthropicPrice := &metadata.ModelPrice{
		Input: 3, Output: 15, CacheRead: pricePtr(0.3), CacheWrite: pricePtr(3.75),
	}
	openAIPrice := &metadata.ModelPrice{
		Input: 0.1, Output: 0.5, CacheRead: pricePtr(0.01), CacheWrite: pricePtr(0.125),
		Tiers: []metadata.PriceTier{{
			Size: 272000, Input: pricePtr(0.2), Output: pricePtr(0.75),
		}},
	}

	tests := []struct {
		name          string
		providerAlias string
		providerType  config.ProviderType
		modelID       string
		want          *metadata.ModelPrice
	}{
		{
			name:          "claude_subscription uses the anthropic provider key",
			providerAlias: "claude",
			providerType:  config.ProviderTypeClaudeSubscription,
			modelID:       "claude-sonnet-5-5",
			want:          anthropicPrice,
		},
		{
			name:          "anthropic uses its fixed provider key",
			providerAlias: "anthropic",
			providerType:  config.ProviderTypeAnthropic,
			modelID:       "claude-sonnet-5-5",
			want:          anthropicPrice,
		},
		{
			name:          "openai keeps context tiers",
			providerAlias: "openai",
			providerType:  config.ProviderTypeOpenAI,
			modelID:       "gpt-6-luna",
			want:          openAIPrice,
		},
		{
			name:          "codex resolves under the openai provider key",
			providerAlias: "codex",
			providerType:  config.ProviderTypeCodex,
			modelID:       "gpt-6-luna",
			want:          openAIPrice,
		},
		{
			name:          "openrouter namespaced model ID",
			providerAlias: "openrouter",
			providerType:  config.ProviderTypeOpenRouter,
			modelID:       "anthropic/claude-sonnet-5-5",
			want:          &metadata.ModelPrice{Input: 3.2, Output: 16},
		},
		{
			name:          "generic alias without a provider key merges across providers",
			providerAlias: "llama",
			providerType:  config.ProviderTypeOllama,
			modelID:       "claude-sonnet-5-5",
			want:          anthropicPrice,
		},
		{
			name:          "generic alias matching a provider key wins the strict lookup",
			providerAlias: "openrouter",
			providerType:  config.ProviderTypeLiteLLM,
			modelID:       "anthropic/claude-sonnet-5-5",
			want:          &metadata.ModelPrice{Input: 3.2, Output: 16},
		},
		{
			name:          "generic alias with no models.dev match is unpriced",
			providerAlias: "ollama",
			providerType:  config.ProviderTypeOllama,
			modelID:       "llama3",
			want:          nil,
		},
		{
			name:          "model without a cost object is unpriced",
			providerAlias: "anthropic",
			providerType:  config.ProviderTypeAnthropic,
			modelID:       "claude-no-cost",
			want:          nil,
		},
		{
			name:          "all-zero cost is a free price, not unpriced",
			providerAlias: "anthropic",
			providerType:  config.ProviderTypeAnthropic,
			modelID:       "claude-free",
			want:          &metadata.ModelPrice{Input: 0, Output: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookupModelPrice(idx, tt.providerAlias, tt.providerType, tt.modelID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("lookupModelPrice(%q, %q, %q) = %#v, want %#v", tt.providerAlias, tt.providerType, tt.modelID, got, tt.want)
			}
		})
	}
}

func TestLookupModelPriceUnavailable(t *testing.T) {
	if got := lookupModelPrice(nil, "openai", config.ProviderTypeOpenAI, "gpt-6-luna"); got != nil {
		t.Fatalf("nil index price = %#v, want nil", got)
	}
	malformed := metadata.ParseIndex([]byte("not json"))
	if got := lookupModelPrice(malformed, "openai", config.ProviderTypeOpenAI, "gpt-6-luna"); got != nil {
		t.Fatalf("malformed index price = %#v, want nil", got)
	}
}

func TestResolverModelPriceAvailability(t *testing.T) {
	t.Run("loaded index is available", func(t *testing.T) {
		resolver := NewResolver(ResolverOptions{ModelsDev: func(context.Context) metadata.LoadResult {
			return metadata.LoadResult{Data: []byte(priceLookupFixture)}
		}})
		price, available := resolver.ModelPrice(context.Background(), "openai", config.ProviderTypeOpenAI, "gpt-6-luna")
		if !available {
			t.Fatal("available = false, want true for a loaded index")
		}
		if price == nil || price.Input != 0.1 {
			t.Fatalf("price = %#v, want the fixture's gpt-6-luna price", price)
		}
		// A miss on a loaded index is still an available lookup; the price is
		// what is missing, not the index.
		if price, available := resolver.ModelPrice(context.Background(), "openai", config.ProviderTypeOpenAI, "absent"); !available || price != nil {
			t.Fatalf("absent model = (%#v, %t), want (nil, true)", price, available)
		}
	})

	t.Run("unloaded index is unavailable", func(t *testing.T) {
		resolver := NewResolver(ResolverOptions{ModelsDev: func(context.Context) metadata.LoadResult {
			return metadata.LoadResult{}
		}})
		price, available := resolver.ModelPrice(context.Background(), "openai", config.ProviderTypeOpenAI, "gpt-6-luna")
		if price != nil || available {
			t.Fatalf("unloaded index = (%#v, %t), want (nil, false)", price, available)
		}
	})

	t.Run("nil resolver is unavailable", func(t *testing.T) {
		var resolver *Resolver
		if price, available := resolver.ModelPrice(context.Background(), "openai", config.ProviderTypeOpenAI, "gpt-6-luna"); price != nil || available {
			t.Fatalf("nil resolver = (%#v, %t), want (nil, false)", price, available)
		}
	})
}
