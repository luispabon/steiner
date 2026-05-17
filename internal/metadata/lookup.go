package metadata

import "encoding/json"

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
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return ModelInfo{}
	}
	for _, providerRaw := range root {
		var provider struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(providerRaw, &provider); err != nil || provider.Models == nil {
			continue
		}
		modelRaw, ok := provider.Models[modelID]
		if !ok {
			continue
		}
		return parseModelEntry(modelRaw)
	}
	return ModelInfo{}
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
