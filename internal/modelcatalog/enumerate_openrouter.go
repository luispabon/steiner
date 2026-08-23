package modelcatalog

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const openRouterMaxPages = 100

// OpenRouterEnumerator discovers text-capable models from OpenRouter's models endpoint.
type OpenRouterEnumerator struct {
	client *http.Client
}

// NewOpenRouterEnumerator creates an OpenRouter model enumerator.
func NewOpenRouterEnumerator(client *http.Client) *OpenRouterEnumerator {
	return &OpenRouterEnumerator{client: clientOrDefault(client)}
}

type openRouterListResponse struct {
	Data  []openRouterModel `json:"data"`
	Links openRouterLinks   `json:"links"`
}

type openRouterLinks struct {
	Next *string `json:"next"`
}

type openRouterModel struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Context      int                    `json:"context_length"`
	Architecture openRouterArchitecture `json:"architecture"`
}

type openRouterArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Modality         string   `json:"modality"`
}

// Enumerate discovers text-capable models from OpenRouter.
func (e *OpenRouterEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	original, err := joinModelsURL("openrouter", ep.BaseURL)
	if err != nil {
		return EnumerationResult{}, err
	}
	originalURL, err := url.Parse(original)
	if err != nil {
		return EnumerationResult{}, fmt.Errorf("parse OpenRouter models URL: %w", err)
	}

	models := make([]DiscoveredModel, 0)
	seen := make(map[string]bool)
	endpoint := original
	var etag string
	for page := 0; page < openRouterMaxPages; page++ {
		if seen[endpoint] {
			break
		}
		seen[endpoint] = true

		req, err := newGETRequest(ctx, ep, endpoint, "Bearer "+ep.APIKey, ep.APIKey != "")
		if err != nil {
			return EnumerationResult{}, err
		}
		var response openRouterListResponse
		pageETag, err := doJSONRequest(e.client, req, &response)
		if err != nil {
			return EnumerationResult{}, err
		}
		if pageETag != "" {
			etag = pageETag
		}
		for _, item := range response.Data {
			if openRouterNonText(item.Architecture) {
				continue
			}
			displayName := item.Name
			if displayName == "" {
				displayName = item.ID
			}
			models = append(models, DiscoveredModel{
				ProviderAlias: ep.Alias,
				ProviderType:  ep.Type,
				ID:            item.ID,
				DisplayName:   displayName,
				Description:   item.Description,
				ContextLength: item.Context,
			})
		}
		if response.Links.Next == nil || *response.Links.Next == "" {
			break
		}
		next, ok := safeOpenRouterNextURL(originalURL, *response.Links.Next)
		if !ok {
			break
		}
		endpoint = next
	}
	return EnumerationResult{Models: models, ETag: etag}, nil
}

func openRouterNonText(architecture openRouterArchitecture) bool {
	if len(architecture.OutputModalities) == 0 {
		return strings.Contains(strings.ToLower(architecture.Modality), "embedding")
	}
	hasText := false
	for _, modality := range architecture.OutputModalities {
		switch strings.ToLower(modality) {
		case "text":
			hasText = true
		case "embeddings":
			return true
		}
	}
	return !hasText
}

func safeOpenRouterNextURL(original *url.URL, next string) (string, bool) {
	candidate, err := url.Parse(next)
	if err != nil {
		return "", false
	}
	resolved := original.ResolveReference(candidate)
	if !strings.EqualFold(resolved.Scheme, original.Scheme) || !strings.EqualFold(resolved.Host, original.Host) {
		return "", false
	}
	return resolved.String(), true
}
