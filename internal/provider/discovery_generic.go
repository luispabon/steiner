package provider

import (
	"context"
	"net/http"
)

// genericDiscoverer implements ModelMetadataDiscoverer for generic OpenAI-compat
// providers. Generic OpenAI-compat endpoints do not expose token limits via
// their /v1/models endpoint, so this is a no-op discoverer.
type genericDiscoverer struct {
	baseURL    string
	httpClient *http.Client
}

// DiscoverModelMetadata always returns zero ModelMetadata for generic OpenAI-compat.
// Generic OpenAI-compat providers do not expose context window or max output tokens.
func (d *genericDiscoverer) DiscoverModelMetadata(_ context.Context, _ string) (ModelMetadata, error) {
	return ModelMetadata{}, nil
}
