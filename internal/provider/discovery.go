package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

const discoveryTimeout = 8 * time.Second

// ModelMetadata holds optional discovered model limits.
type ModelMetadata struct {
	ContextWindow     int // 0 = unknown
	MaxOutputTokens   int // 0 = unknown
	ReasoningEchoBack bool
}

// ModelMetadataDiscoverer discovers token limits for a backend model ID.
// Discovery is best-effort: failure returns zero ModelMetadata, not an error.
type ModelMetadataDiscoverer interface {
	DiscoverModelMetadata(ctx context.Context, backendModelID string) (ModelMetadata, error)
}

// NewDiscoverer returns the appropriate discoverer for the provider type.
// Returns nil when the provider type has no discovery implementation.
func NewDiscoverer(cfg config.ProviderConfig, httpClient *http.Client) ModelMetadataDiscoverer {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	switch cfg.Type {
	case config.ProviderTypeOpenRouter:
		return &openRouterDiscoverer{baseURL: cfg.BaseURL, apiKey: cfg.APIKey, httpClient: httpClient}
	case config.ProviderTypeOllama:
		return &ollamaDiscoverer{baseURL: cfg.BaseURL, httpClient: httpClient}
	case config.ProviderTypeLMStudio:
		return &lmStudioDiscoverer{baseURL: cfg.BaseURL, httpClient: httpClient}
	case config.ProviderTypeOpenAICompat, config.ProviderTypeOpencodeGo, config.ProviderTypeOpencodeZen:
		return &genericDiscoverer{baseURL: cfg.BaseURL, httpClient: httpClient}
	case config.ProviderTypeCodex:
		return nil
	default:
		return nil
	}
}
