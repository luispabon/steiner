package provider

import (
	"context"
	"net/http"
)

// lmStudioDiscoverer implements ModelMetadataDiscoverer for LM Studio.
// LM Studio's /v1/models endpoint only returns model IDs, not token limits,
// so this is a no-op discoverer that always returns zero ModelMetadata.
type lmStudioDiscoverer struct {
	baseURL    string
	httpClient *http.Client
}

// DiscoverModelMetadata always returns zero ModelMetadata for LM Studio.
// LM Studio does not expose context window or max output tokens via its API.
func (d *lmStudioDiscoverer) DiscoverModelMetadata(_ context.Context, _ string) (ModelMetadata, error) {
	return ModelMetadata{}, nil
}
