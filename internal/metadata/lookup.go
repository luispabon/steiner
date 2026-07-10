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
		NPM    string                     `json:"npm"`
		API    string                     `json:"api"`
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(providerRaw, &provider); err != nil || provider.Models == nil {
		return ModelInfo{}, false
	}
	modelRaw, ok := provider.Models[modelID]
	if !ok {
		return ModelInfo{}, false
	}
	return parseModelEntry(provider.NPM, provider.API, modelRaw), true
}

func parseModelEntry(providerNPM, providerAPI string, raw json.RawMessage) ModelInfo {
	var entry struct {
		Limit struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
		Provider struct {
			NPM string `json:"npm"`
			API string `json:"api"`
		} `json:"provider"`
		Interleaved struct {
			Field string `json:"field"`
		} `json:"interleaved"`
		Modalities struct {
			Input []string `json:"input"`
		} `json:"modalities"`
		ReasoningOptions []struct {
			Type   string   `json:"type"`
			Values []string `json:"values"`
		} `json:"reasoning_options"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ModelInfo{}
	}
	var supportedEfforts []string
	for _, ro := range entry.ReasoningOptions {
		if ro.Type == "effort" {
			supportedEfforts = ro.Values
			break
		}
	}
	return ModelInfo{
		ContextWindow:             entry.Limit.Context,
		MaxOutputTokens:           entry.Limit.Output,
		ReasoningEchoBack:         entry.Interleaved.Field == "reasoning_content",
		ReasoningSupportedEfforts: supportedEfforts,
		ProviderNPM:               providerNPM,
		ProviderAPI:               providerAPI,
		ModelProviderNPM:          entry.Provider.NPM,
		ModelProviderAPI:          entry.Provider.API,
		InterleavedField:          entry.Interleaved.Field,
		VisionInput:               containsFold(entry.Modalities.Input, "image"),
		Found:                     true,
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
