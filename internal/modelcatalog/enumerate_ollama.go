package modelcatalog

import (
	"context"
	"net/http"
)

// OllamaEnumerator discovers models from Ollama's native tags endpoint.
type OllamaEnumerator struct {
	client *http.Client
}

// NewOllamaEnumerator creates an Ollama model enumerator.
func NewOllamaEnumerator(client *http.Client) *OllamaEnumerator {
	return &OllamaEnumerator{client: clientOrDefault(client)}
}

type ollamaTagsResponse struct {
	Models []ollamaModel `json:"models"`
}

type ollamaModel struct {
	Name         string    `json:"name"`
	Model        string    `json:"model"`
	Capabilities *[]string `json:"capabilities"`
}

// Enumerate discovers models from an Ollama endpoint.
func (e *OllamaEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	endpoint, err := joinModelsURL(ep.Type, ep.BaseURL)
	if err != nil {
		return EnumerationResult{}, err
	}
	req, err := newGETRequest(ctx, ep, endpoint, "", false)
	if err != nil {
		return EnumerationResult{}, err
	}
	var response ollamaTagsResponse
	etag, err := doJSONRequest(e.client, req, &response)
	if err != nil {
		return EnumerationResult{}, err
	}

	models := make([]DiscoveredModel, 0, len(response.Models))
	for _, item := range response.Models {
		id := item.Name
		if id == "" {
			id = item.Model
		}
		if item.Capabilities != nil {
			if contains(item.Capabilities, "embedding") {
				continue
			}
		} else if HeuristicallyExcluded(id) {
			continue
		}
		models = append(models, DiscoveredModel{
			ProviderAlias: ep.Alias,
			ProviderType:  ep.Type,
			ID:            id,
			DisplayName:   id,
		})
	}
	return EnumerationResult{Models: models, ETag: etag}, nil
}

func contains(values *[]string, wanted string) bool {
	for _, value := range *values {
		if value == wanted {
			return true
		}
	}
	return false
}
