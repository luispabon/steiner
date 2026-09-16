package metadata

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

// Index is parsed models.dev data keyed by provider and model ID. Parsing
// happens once, in ParseIndex; LookupProvider then walks the already-parsed
// maps instead of re-parsing JSON on every call.
type Index struct {
	providers map[string]indexProvider
	malformed bool // top-level JSON invalid or unparseable
}

type indexProvider struct {
	npm, api string
	models   map[string]indexModel
}

type indexModel struct {
	info      ModelInfo
	malformed bool // this specific entry failed to parse (or was JSON null)
}

// ParseIndex parses models.dev cache data into an Index. A nil or invalid
// top-level document produces a malformed Index (LookupProvider always
// returns LookupReasonMalformed for it); this never panics.
func ParseIndex(data []byte) *Index {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return &Index{malformed: true}
	}

	idx := &Index{providers: make(map[string]indexProvider, len(root))}
	for providerID, providerRaw := range root {
		var provider struct {
			NPM    string                     `json:"npm"`
			API    string                     `json:"api"`
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(providerRaw, &provider); err != nil || provider.Models == nil {
			continue
		}
		models := make(map[string]indexModel, len(provider.Models))
		for modelID, modelRaw := range provider.Models {
			models[modelID] = parseIndexModel(provider.NPM, provider.API, modelRaw)
		}
		idx.providers[providerID] = indexProvider{npm: provider.NPM, api: provider.API, models: models}
	}
	return idx
}

func parseIndexModel(providerNPM, providerAPI string, raw json.RawMessage) indexModel {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return indexModel{malformed: true}
	}
	info, err := parseModelEntry(providerNPM, providerAPI, raw)
	if err != nil {
		return indexModel{malformed: true}
	}
	return indexModel{info: info}
}

// LookupProvider finds model metadata for modelID under providerID.
// If providerID doesn't have the model, checks whether another provider does
// (provider_mismatch) before reporting not_found. Semantics are identical to
// the pre-Index lookupForProvider.
func (idx *Index) LookupProvider(providerID, modelID string) LookupResult {
	if idx.malformed {
		return LookupResult{Reason: LookupReasonMalformed}
	}

	providerID = strings.TrimSpace(providerID)
	providerMalformed := false
	if provider, ok := idx.providers[providerID]; ok {
		if model, ok := provider.models[modelID]; ok {
			if !model.malformed {
				return LookupResult{Info: model.info}
			}
			providerMalformed = true
		}
	}

	otherProviders := make([]string, 0, len(idx.providers))
	for provider := range idx.providers {
		if provider != providerID {
			otherProviders = append(otherProviders, provider)
		}
	}
	sort.Strings(otherProviders)

	otherMalformed := false
	for _, otherProvider := range otherProviders {
		model, ok := idx.providers[otherProvider].models[modelID]
		if !ok {
			continue
		}
		if !model.malformed {
			// A valid match under another provider takes precedence over malformed
			// entries when classifying a provider mismatch.
			return LookupResult{Reason: LookupReasonProviderMismatch}
		}
		otherMalformed = true
	}

	if providerMalformed || otherMalformed {
		return LookupResult{Reason: LookupReasonMalformed}
	}
	return LookupResult{Reason: LookupReasonNotFound}
}

// LookupMerged looks up modelID across every provider in the index and
// merges every non-malformed entry found. It is used for "generic" provider
// profiles (openai_compat, ollama, litellm) whose configured alias doesn't
// correspond to a models.dev provider key, so no single provider's entry is
// authoritative for the model.
//
// Providers are visited in sorted key order for determinism. With zero valid
// entries and zero malformed entries, it reports LookupReasonNotFound; with
// zero valid entries and at least one malformed entry, LookupReasonMalformed.
// With exactly one valid entry, that entry's ModelInfo is returned unchanged
// (Reason "", ProviderCount 1). With two or more valid entries, the merged
// ModelInfo takes the minimum of each provider's positive ContextWindow and
// MaxOutputTokens, ANDs VisionInput and ReasoningEchoBack, keeps
// InterleavedField only if every entry agrees, intersects
// ReasoningSupportedEfforts preserving the first entry's order, clears the
// npm/api provenance fields, and reports LookupReasonMerged with
// ProviderCount set to the number of valid entries merged.
func (idx *Index) LookupMerged(modelID string) LookupResult {
	if idx.malformed {
		return LookupResult{Reason: LookupReasonMalformed}
	}

	providerIDs := make([]string, 0, len(idx.providers))
	for providerID := range idx.providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	var valid []ModelInfo
	sawMalformed := false
	for _, providerID := range providerIDs {
		model, ok := idx.providers[providerID].models[modelID]
		if !ok {
			continue
		}
		if model.malformed {
			sawMalformed = true
			continue
		}
		valid = append(valid, model.info)
	}

	switch {
	case len(valid) == 0 && sawMalformed:
		return LookupResult{Reason: LookupReasonMalformed}
	case len(valid) == 0:
		return LookupResult{Reason: LookupReasonNotFound}
	case len(valid) == 1:
		return LookupResult{Info: valid[0]}
	default:
		return LookupResult{Info: mergeModelInfos(valid), Reason: LookupReasonMerged}
	}
}

// mergeModelInfos combines two or more valid ModelInfo entries for the same
// model across providers. See LookupMerged for the exact merge rules.
func mergeModelInfos(entries []ModelInfo) ModelInfo {
	merged := ModelInfo{
		VisionInput:       true,
		ReasoningEchoBack: true,
		Found:             true,
		ProviderCount:     len(entries),
	}
	for i, entry := range entries {
		if entry.ContextWindow > 0 && (merged.ContextWindow == 0 || entry.ContextWindow < merged.ContextWindow) {
			merged.ContextWindow = entry.ContextWindow
		}
		if entry.MaxOutputTokens > 0 && (merged.MaxOutputTokens == 0 || entry.MaxOutputTokens < merged.MaxOutputTokens) {
			merged.MaxOutputTokens = entry.MaxOutputTokens
		}
		if !entry.VisionInput {
			merged.VisionInput = false
		}
		if !entry.ReasoningEchoBack {
			merged.ReasoningEchoBack = false
		}
		if i == 0 {
			merged.InterleavedField = entry.InterleavedField
		} else if merged.InterleavedField != entry.InterleavedField {
			merged.InterleavedField = ""
		}
	}
	merged.ReasoningSupportedEfforts = intersectEfforts(entries)
	return merged
}

// intersectEfforts intersects every entry's ReasoningSupportedEfforts,
// preserving the first entry's element order. Returns nil if any entry
// contributes an empty/nil slice.
func intersectEfforts(entries []ModelInfo) []string {
	for _, entry := range entries {
		if len(entry.ReasoningSupportedEfforts) == 0 {
			return nil
		}
	}
	result := make([]string, 0, len(entries[0].ReasoningSupportedEfforts))
	for _, effort := range entries[0].ReasoningSupportedEfforts {
		inAll := true
		for _, entry := range entries[1:] {
			if !slices.Contains(entry.ReasoningSupportedEfforts, effort) {
				inAll = false
				break
			}
		}
		if inAll {
			result = append(result, effort)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
		ProviderCount:             1,
	}, nil
}
