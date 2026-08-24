package main

import (
	"context"
	"errors"
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

// buildModelCatalogService creates the shared model catalog service and its
// provider endpoints. Discovery-disabled configurations retain a usable service
// and popularity store, but do not expose endpoints or perform discovery work.
func buildModelCatalogService(cfg *config.Config, httpClient *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
	popularity := modelcatalog.NewStore("")
	enabled := cfg != nil && cfg.Models.DiscoveryEnabled
	cache := modelcatalog.NewCache("")

	dispatcher := func(providerType string, client *http.Client) (modelcatalog.Enumerator, error) {
		if providerType == string(config.ProviderTypeCodex) {
			return modelcatalog.NewCodexEnumerator(client, version, codexCatalogCredentials), nil
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
		if provider.Type == config.ProviderTypeCodex {
			if baseURL := codexModelsBaseURL(); baseURL != "" {
				provider.BaseURL = baseURL
			}
		}
		endpoints = append(endpoints, modelcatalog.Endpoint{
			Alias:   alias,
			Type:    string(provider.Type),
			BaseURL: provider.BaseURL,
			APIKey:  provider.APIKey,
			Headers: cloneStringMap(provider.Headers),
		})
	}
	return service, endpoints, popularity
}

// codexModelsBaseURL returns the model-listing host to use for the Codex
// provider's enumeration endpoint, mirroring newCodexProvider's transport
// selection (cmd/steiner/runtime_build.go): a ChatGPT-subscription OAuth
// session (no distinct OpenAI API key on the stored token) must list models
// from codexChatGPTBackendURL rather than the OpenAI Platform API host —
// the Platform host rejects that token with 403. Returns "" when the token
// carries its own API key or cannot be read, leaving the provider's
// configured/default base URL untouched.
func codexModelsBaseURL() string {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return ""
	}
	token, err := oauth.NewTokenStore(path).Load()
	if err != nil {
		return ""
	}
	if oauth.TokenOpenAIAPIKey(token) != "" {
		return ""
	}
	return codexChatGPTBackendURL
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
