package metadata

import (
	"encoding/json"
	"strings"
)

// ModelInfo holds metadata for a single model from the models.dev cache.
type ModelInfo struct {
	ContextWindow     int
	MaxOutputTokens   int
	ReasoningEchoBack bool
}

// Lookup finds model metadata for the given backend model ID in the cached JSON.
// The models.dev format is {provider: {models: {model_id: {...}}}}.
// Lookup searches across all providers for the first matching model ID.
// Returns zero ModelInfo if not found or if data is malformed.
func Lookup(data []byte, modelID string) ModelInfo {
	return LookupWithProvider(data, "", modelID)
}

// LookupWithProvider finds model metadata for modelID, preferring providerID
// when it is present in the models.dev cache before falling back across all
// providers. Provider preference matters because models.dev may list the same
// model ID with different limits for different providers.
func LookupWithProvider(data []byte, providerID, modelID string) ModelInfo {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return ModelInfo{}
	}
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		if providerRaw, ok := root[providerID]; ok {
			info, ok := lookupProviderModel(providerRaw, modelID)
			if ok {
				return info
			}
		}
	}
	for _, providerRaw := range root {
		info, ok := lookupProviderModel(providerRaw, modelID)
		if ok {
			return info
		}
	}
	return ModelInfo{}
}

func lookupProviderModel(providerRaw json.RawMessage, modelID string) (ModelInfo, bool) {
	var provider struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(providerRaw, &provider); err != nil || provider.Models == nil {
		return ModelInfo{}, false
	}
	modelRaw, ok := provider.Models[modelID]
	if !ok {
		return ModelInfo{}, false
	}
	return parseModelEntry(modelRaw), true
}

func parseModelEntry(raw json.RawMessage) ModelInfo {
	var entry struct {
		Limit struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
		Interleaved struct {
			Field string `json:"field"`
		} `json:"interleaved"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ModelInfo{}
	}
	return ModelInfo{
		ContextWindow:     entry.Limit.Context,
		MaxOutputTokens:   entry.Limit.Output,
		ReasoningEchoBack: entry.Interleaved.Field == "reasoning_content",
	}
}

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
