package metadata

import (
	"encoding/json"
	"strings"
)

// ModelInfo holds metadata for a single model from the models.dev cache.
type ModelInfo struct {
	ContextWindow             int
	MaxOutputTokens           int
	ReasoningEchoBack         bool
	ReasoningSupportedEfforts []string
	ProviderNPM               string
	ProviderAPI               string
	ModelProviderNPM          string
	ModelProviderAPI          string
	InterleavedField          string
	VisionInput               bool
	Found                     bool
}

// LookupResult contains metadata and a degradation reason when lookup could not
// safely return a match.
type LookupResult struct {
	Info   ModelInfo
	Reason string
}

const (
	// LookupReasonMalformed reports invalid models.dev JSON.
	LookupReasonMalformed = "malformed"
	// LookupReasonNotFound reports no matching model metadata.
	LookupReasonNotFound = "not_found"
	// LookupReasonProviderMismatch reports a model found only under another provider.
	LookupReasonProviderMismatch = "provider_mismatch"
)

// CountModels returns the number of unique model entries across all providers
// in cached metadata.
func CountModels(data []byte) int {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return 0
	}
	seen := make(map[string]struct{})
	for _, providerRaw := range root {
		var provider struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(providerRaw, &provider); err != nil {
			continue
		}
		for k := range provider.Models {
			seen[k] = struct{}{}
		}
	}
	return len(seen)
}

// containsFold reports whether ss contains target, using case-insensitive comparison.
func containsFold(ss []string, target string) bool {
	target = strings.ToLower(target)
	for _, s := range ss {
		if strings.ToLower(s) == target {
			return true
		}
	}
	return false
}
