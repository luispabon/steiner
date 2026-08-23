package modelcatalog

import (
	"context"
	"net/http"
)

// OpenAIEnumerator discovers models from OpenAI-compatible /models endpoints.
type OpenAIEnumerator struct {
	client *http.Client
}

// NewOpenAIEnumerator creates an OpenAI-compatible model enumerator.
func NewOpenAIEnumerator(client *http.Client) *OpenAIEnumerator {
	return &OpenAIEnumerator{client: clientOrDefault(client)}
}

type openAIListResponse struct {
	Data []openAIModel `json:"data"`
}

type openAIModel struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
}

// Enumerate discovers models from an OpenAI-compatible endpoint.
func (e *OpenAIEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	endpoint, err := joinModelsURL(ep.Type, ep.BaseURL)
	if err != nil {
		return EnumerationResult{}, err
	}
	req, err := newGETRequest(ctx, ep, endpoint, bearerAuthorization(ep.APIKey))
	if err != nil {
		return EnumerationResult{}, err
	}
	var response openAIListResponse
	etag, err := doJSONRequest(e.client, req, &response)
	if err != nil {
		return EnumerationResult{}, err
	}

	models := make([]DiscoveredModel, 0, len(response.Data))
	for _, item := range response.Data {
		if ep.Type == "litellm" {
			if item.Mode == "embedding" {
				continue
			}
			if item.Mode == "" && HeuristicallyExcluded(item.ID) {
				continue
			}
		} else if HeuristicallyExcluded(item.ID) {
			continue
		}
		models = append(models, DiscoveredModel{
			ProviderAlias: ep.Alias,
			ProviderType:  ep.Type,
			ID:            item.ID,
			DisplayName:   item.ID,
		})
	}
	return EnumerationResult{Models: models, ETag: etag}, nil
}
