package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/oauth"
)

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
		if !catalogProviderSupported(provider.Type) {
			continue
		}
		apiKey := provider.APIKey
		if strings.TrimSpace(apiKey) == "" && strings.TrimSpace(provider.APIKeyEnv) != "" {
			apiKey = os.Getenv(strings.TrimSpace(provider.APIKeyEnv))
		}
		baseURL := strings.TrimSpace(provider.BaseURL)
		if baseURL == "" {
			baseURL = catalogDefaultBaseURL(provider.Type)
		}
		endpoints = append(endpoints, modelcatalog.Endpoint{
			Alias:   alias,
			Type:    string(provider.Type),
			BaseURL: baseURL,
			APIKey:  apiKey,
			Headers: cloneStringMap(provider.Headers),
		})
	}
	return service, endpoints, popularity
}

func catalogProviderSupported(providerType config.ProviderType) bool {
	switch providerType {
	case config.ProviderTypeOpenAICompat, config.ProviderTypeOllama, config.ProviderTypeLMStudio,
		config.ProviderTypeOpenRouter, config.ProviderTypeOpenAI, config.ProviderTypeLiteLLM,
		config.ProviderTypeAnthropic, config.ProviderTypeCodex:
		return true
	default:
		return false
	}
}

// catalogDefaultBaseURL mirrors provider.defaultProviderBaseURL without
// importing that unexported provider implementation detail.
func catalogDefaultBaseURL(providerType config.ProviderType) string {
	switch providerType {
	case config.ProviderTypeOpenRouter:
		return "https://openrouter.ai/api/v1"
	case config.ProviderTypeOpenAI, config.ProviderTypeCodex:
		return "https://api.openai.com/v1"
	default:
		return ""
	}
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
