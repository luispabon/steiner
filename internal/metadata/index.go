package metadata

import (
	"bytes"
	"encoding/json"
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
