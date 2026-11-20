package provider

import (
	"bytes"
	"encoding/json"
)

// usageCostUSD decodes a provider-reported cost. Only a bare JSON number is a
// cost: OpenRouter reports usage.cost as a number, while providers such as
// Perplexity send an object, and a string or null is equally not a number.
// Those shapes leave CostUSD nil instead of failing the whole usage decode.
func usageCostUSD(raw json.RawMessage) *float64 {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var cost float64
	if err := json.Unmarshal(trimmed, &cost); err != nil {
		return nil
	}
	return &cost
}

// clampReasoningTokens keeps a reported reasoning count at or above zero. The
// recorder bounds it above by CompletionTokens, since reasoning is a subset of
// that total.
func clampReasoningTokens(tokens int) int {
	if tokens < 0 {
		return 0
	}
	return tokens
}
