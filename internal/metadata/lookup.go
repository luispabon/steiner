package metadata

import (
	"encoding/json"
	"reflect"
	"sort"
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
// Providerless lookup is deterministic and rejects conflicting matches.
// Returns zero ModelInfo if not found or if data is malformed.
func Lookup(data []byte, modelID string) ModelInfo {
	return LookupWithProviderResult(data, "", modelID).Info
}

// LookupWithProvider finds model metadata for modelID. A supplied providerID
// limits lookup to that provider. It never selects metadata from an unrelated
// provider when the requested provider has no match.
func LookupWithProvider(data []byte, providerID, modelID string) ModelInfo {
	return LookupWithProviderResult(data, providerID, modelID).Info
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
	// LookupReasonConflict reports conflicting provider-specific matches.
	LookupReasonConflict = "conflict"
)

// LookupWithProviderResult is LookupWithProvider with an observable reason for
// malformed, missing, mismatched, or conflicting metadata.
func LookupWithProviderResult(data []byte, providerID, modelID string) LookupResult {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return LookupResult{Reason: LookupReasonMalformed}
	}
	providerID = strings.TrimSpace(providerID)
	if providerID != "" {
		return lookupForProvider(root, providerID, modelID)
	}
	return lookupAcrossProviders(root, modelID)
}

func lookupForProvider(root map[string]json.RawMessage, providerID, modelID string) LookupResult {
	if providerRaw, ok := root[providerID]; ok {
		if info, found, malformed := lookupProviderModel(providerRaw, modelID); found {
			return LookupResult{Info: info}
		} else if malformed {
			return LookupResult{Reason: LookupReasonMalformed}
		}
	}
	for otherProvider, providerRaw := range root {
		if otherProvider == providerID {
			continue
		}
		if _, found, malformed := lookupProviderModel(providerRaw, modelID); found {
			return LookupResult{Reason: LookupReasonProviderMismatch}
		} else if malformed {
			return LookupResult{Reason: LookupReasonMalformed}
		}
	}
	return LookupResult{Reason: LookupReasonNotFound}
}

func lookupAcrossProviders(root map[string]json.RawMessage, modelID string) LookupResult {
	type candidate struct {
		provider string
		info     ModelInfo
	}
	providers := make([]string, 0, len(root))
	for provider := range root {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	candidates := make([]candidate, 0, len(providers))
	for _, provider := range providers {
		if info, found, malformed := lookupProviderModel(root[provider], modelID); found {
			candidates = append(candidates, candidate{provider: provider, info: info})
		} else if malformed {
			return LookupResult{Reason: LookupReasonMalformed}
		}
	}
	if len(candidates) == 0 {
		return LookupResult{Reason: LookupReasonNotFound}
	}
	for _, candidate := range candidates[1:] {
		if !reflect.DeepEqual(candidate.info, candidates[0].info) {
			return LookupResult{Reason: LookupReasonConflict}
		}
	}
	return LookupResult{Info: candidates[0].info}
}

func lookupProviderModel(providerRaw json.RawMessage, modelID string) (ModelInfo, bool, bool) {
	var provider struct {
		NPM    string                     `json:"npm"`
		API    string                     `json:"api"`
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(providerRaw, &provider); err != nil || provider.Models == nil {
		return ModelInfo{}, false, false
	}
	modelRaw, ok := provider.Models[modelID]
	if !ok {
		return ModelInfo{}, false, false
	}
	info, err := parseModelEntry(provider.NPM, provider.API, modelRaw)
	if err != nil {
		return ModelInfo{}, false, true
	}
	return info, true, false
}

func parseModelEntry(providerNPM, providerAPI string, raw json.RawMessage) (ModelInfo, error) {
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
		return ModelInfo{}, err
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
	}, nil
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
