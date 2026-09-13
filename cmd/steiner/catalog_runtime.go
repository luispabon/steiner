package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/oauth"
	providerpkg "github.com/luispabon/steiner/internal/provider"
)

// codexChatGPTBackendURL is the model-serving host used by ChatGPT-subscription
// OAuth sessions (no distinct OpenAI API key). It differs from the OpenAI
// Platform API host used when the Codex token carries its own API key.
const codexChatGPTBackendURL = "https://chatgpt.com/backend-api/codex"

// codexCatalogClientVersion is the Codex client version reported during catalog
// discovery. Codex gates catalog models on a compatible client version, so
// discovery sends this fixed value instead of Steiner's build version ("dev"
// for local builds).
const codexCatalogClientVersion = "0.153.0"

// buildModelCatalogService creates the shared model catalog service and its
// provider endpoints. Discovery-disabled configurations retain a usable service
// and popularity store, but do not expose endpoints or perform discovery work.
func buildModelCatalogService(cfg *config.Config, httpClient *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
	popularity := modelcatalog.NewStore("")
	enabled := cfg != nil && cfg.Models.DiscoveryEnabled
	cache := modelcatalog.NewCache("")

	dispatcher := func(providerType string, client *http.Client) (modelcatalog.Enumerator, error) {
		if providerType == string(config.ProviderTypeCodex) {
			return modelcatalog.NewCodexEnumerator(client, codexCatalogClientVersion, codexCatalogCredentials), nil
		}
		return modelcatalog.DefaultDispatcher(providerType, client)
	}
	service := modelcatalog.NewService(dispatcher, cache, popularity, httpClient, enabled)
	if !enabled || cfg == nil {
		return service, nil, popularity
	}

	aliases := make([]string, 0, len(cfg.Providers))
	for alias := range cfg.Providers {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	endpoints := make([]modelcatalog.Endpoint, 0, len(aliases))
	for _, alias := range aliases {
		provider := cfg.Providers[alias]
		if !modelcatalog.SupportsType(provider.Type) {
			continue
		}
		provider = providerpkg.ResolveProviderConfig(provider)
		provider.BaseURL = strings.TrimSpace(provider.BaseURL)
		endpoint := modelcatalog.Endpoint{Alias: alias, Type: string(provider.Type), BaseURL: provider.BaseURL, APIKey: provider.APIKey, Headers: cloneStringMap(provider.Headers)}
		if provider.Type == config.ProviderTypeCodex {
			// Codex discovery always lists against the ChatGPT backend: the cached
			// fingerprint must not follow the configured host or the token's
			// exchanged API key.
			endpoint.BaseURL = codexChatGPTBackendURL
			endpoint.Prepare = func(ctx context.Context) (modelcatalog.Endpoint, error) {
				return prepareCodexCatalogEndpoint(ctx, endpoint)
			}
		}
		endpoints = append(endpoints, endpoint)
	}
	return service, endpoints, popularity
}

// catalogConfigCopy returns a copy of cfg whose Codex providers point at the
// ChatGPT backend, matching the base URL catalog discovery stores in its cache.
// The runtime config is left untouched.
func catalogConfigCopy(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	copied := *cfg
	copied.Providers = make(map[string]config.ProviderConfig, len(cfg.Providers))
	for alias, provider := range cfg.Providers {
		if provider.Type == config.ProviderTypeCodex {
			provider.BaseURL = codexChatGPTBackendURL
		}
		copied.Providers[alias] = provider
	}
	return &copied
}

// prepareCodexCatalogEndpoint refreshes persisted OAuth and keeps the listing
// host on the ChatGPT backend. Token load, refresh, and auth failures abort the
// refresh rather than listing with stale credentials.
func prepareCodexCatalogEndpoint(ctx context.Context, endpoint modelcatalog.Endpoint) (modelcatalog.Endpoint, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return endpoint, fmt.Errorf("resolve Codex token path: %w", err)
	}
	store := oauth.NewTokenStore(path)
	token, err := store.Load()
	if err != nil {
		return endpoint, err
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{ClientID: oauth.CodexClientID, Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL}}, token).Token()
	if err != nil {
		return endpoint, err
	}
	endpoint.BaseURL = codexChatGPTBackendURL
	return endpoint, nil
}

func codexCatalogCredentials(_ context.Context) (string, string, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return "", "", errors.New("resolve Codex token path: " + err.Error())
	}
	store := oauth.NewTokenStore(path)
	token, err := store.Load()
	if err != nil {
		return "", "", err
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		return "", "", err
	}
	accountID := oauth.TokenChatGPTAccountID(token)
	if accountID == "" {
		return "", "", errors.New("Codex token missing ChatGPT account metadata")
	}
	return token.AccessToken, accountID, nil
}
