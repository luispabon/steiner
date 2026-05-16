package provider

import (
	"context"
	"encoding/json"
	"net/http"
)

// openRouterDiscoverer discovers model metadata via the OpenRouter API.
type openRouterDiscoverer struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// openRouterModelsResponse is the API response shape for /api/v1/models.
type openRouterModelsResponse struct {
	Data []openRouterModelEntry `json:"data"`
}

type openRouterModelEntry struct {
	ID          string                    `json:"id"`
	ContextLen  int                       `json:"context_length"`
	TopProvider openRouterTopProviderInfo `json:"top_provider"`
}

type openRouterTopProviderInfo struct {
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

// DiscoverModelMetadata fetches model limits from OpenRouter.
// Returns zero ModelMetadata on any failure.
func (d *openRouterDiscoverer) DiscoverModelMetadata(ctx context.Context, backendModelID string) (ModelMetadata, error) {
	url := d.baseURL + "/api/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}
	if d.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.apiKey)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return ModelMetadata{}, nil
	}

	var payload openRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}

	for _, entry := range payload.Data {
		if entry.ID == backendModelID {
			return ModelMetadata{
				ContextWindow:   entry.ContextLen,
				MaxOutputTokens: entry.TopProvider.MaxCompletionTokens,
			}, nil
		}
	}

	return ModelMetadata{}, nil
}
