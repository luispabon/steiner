package modelcatalog

import (
	"context"
	"net/http"
)

// LMStudioEnumerator discovers models from LM Studio's native REST API.
type LMStudioEnumerator struct {
	client *http.Client
}

// NewLMStudioEnumerator creates an LM Studio model enumerator.
func NewLMStudioEnumerator(client *http.Client) *LMStudioEnumerator {
	return &LMStudioEnumerator{client: clientOrDefault(client)}
}

type lmStudioModelsResponse struct {
	Models []lmStudioModel `json:"models"`
}

type lmStudioModel struct {
	Type             string                `json:"type"`
	Publisher        string                `json:"publisher"`
	Key              string                `json:"key"`
	DisplayName      string                `json:"display_name"`
	Description      string                `json:"description"`
	MaxContextLength int                   `json:"max_context_length"`
	Capabilities     *lmStudioCapabilities `json:"capabilities"`
}

type lmStudioCapabilities struct {
	Reasoning *lmStudioReasoning `json:"reasoning"`
}

type lmStudioReasoning struct {
	AllowedOptions []string `json:"allowed_options"`
}

// Enumerate discovers models from an LM Studio endpoint.
func (e *LMStudioEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	endpoint, err := joinModelsURL(ep.Type, ep.BaseURL)
	if err != nil {
		return EnumerationResult{}, err
	}
	req, err := newGETRequest(ctx, ep, endpoint, "Bearer "+ep.APIKey, ep.APIKey != "")
	if err != nil {
		return EnumerationResult{}, err
	}
	var response lmStudioModelsResponse
	etag, err := doJSONRequest(e.client, req, &response)
	if err != nil {
		return EnumerationResult{}, err
	}

	models := make([]DiscoveredModel, 0, len(response.Models))
	for _, item := range response.Models {
		if item.Type == "embedding" {
			continue
		}
		displayName := item.DisplayName
		if displayName == "" {
			displayName = item.Key
		}
		model := DiscoveredModel{
			ProviderAlias: ep.Alias,
			ProviderType:  ep.Type,
			ID:            item.Key,
			DisplayName:   displayName,
			Description:   item.Description,
			ContextLength: item.MaxContextLength,
		}
		if item.Capabilities != nil && item.Capabilities.Reasoning != nil {
			model.SupportedEfforts = item.Capabilities.Reasoning.AllowedOptions
		}
		models = append(models, model)
	}
	return EnumerationResult{Models: models, ETag: etag}, nil
}
