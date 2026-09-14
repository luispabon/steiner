package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oauth"
	"github.com/luispabon/steiner/internal/provider"
)

func buildRuntimeProviderFactory(httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) func(provider.ResolvedModel, string) (provider.Provider, error) {
	return func(rm provider.ResolvedModel, sessionID string) (provider.Provider, error) {
		if rm.ProviderConfig.Type == config.ProviderTypeOpencodeGo || rm.ProviderConfig.Type == config.ProviderTypeOpencodeZen {
			return newOpencodeProvider(rm, rm.ProviderConfig.Type, httpClient, streamErrorLog, sessionID)
		}

		providerType := rm.EffectiveProviderType
		if providerType == "" {
			providerType = rm.ProviderConfig.Type
		}
		if providerType == "" {
			return nil, fmt.Errorf("resolved provider type is empty for model %q", rm.Alias)
		}

		switch providerType {
		case config.ProviderTypeOpenAICompat, config.ProviderTypeOllama, config.ProviderTypeLMStudio,
			config.ProviderTypeOpenRouter, config.ProviderTypeOpenAI, config.ProviderTypeLiteLLM:
			return newOpenAICompat(runtimeProviderConfig(rm, rm.ProviderConfig.Type, httpClient, streamErrorLog))
		case config.ProviderTypeAnthropic:
			return newAnthropic(runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog))
		case config.ProviderTypeCodex:
			return newCodexProvider(rm, providerType, httpClient, streamErrorLog)
		default:
			return nil, fmt.Errorf("provider type %q is not implemented by the runtime provider factory", providerType)
		}
	}
}

// newOpencodeProvider builds a provider for opencode_go/opencode_zen, injecting
// the X-Opencode-Session header and dispatching to either the Anthropic-native
// or OpenAI-compatible transport based on the model's resolved effective transport.
func newOpencodeProvider(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger, sessionID string) (provider.Provider, error) {
	cfg := runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog)
	cfg.Headers = cloneStringMap(cfg.Headers)
	cfg.Headers["X-Opencode-Session"] = sessionID
	if rm.EffectiveProviderType == config.ProviderTypeAnthropic {
		return newAnthropic(cfg)
	}
	return newOpenAICompat(cfg)
}

func newCodexProvider(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) (provider.Provider, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return nil, fmt.Errorf("resolve token path: %w", err)
	}
	store := oauth.NewTokenStore(path)
	token, err := store.Load()
	if errors.Is(err, oauth.ErrNoToken) {
		return nil, fmt.Errorf("codex provider requires authentication — run 'steiner login codex' first")
	} else if err != nil {
		return nil, fmt.Errorf("load codex token: %w", err)
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh codex token: %w", err)
	}
	cfg := runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog)
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		cfg.APIKey = apiKey
	} else {
		accountID := oauth.TokenChatGPTAccountID(token)
		if accountID == "" {
			return nil, fmt.Errorf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		cfg.BaseURL = codexChatGPTBackendURL
		cfg.APIKey = token.AccessToken
		cfg.Headers = cloneStringMap(cfg.Headers)
		cfg.Headers["ChatGPT-Account-ID"] = accountID
	}
	if !isCodexWSDispatch(rm) {
		return newCodexResponses(cfg)
	}
	return newCodexResponsesWS(cfg)
}

// isCodexWSDispatch reports whether rm resolves to a Codex WebSocket
// transport (explicit websocket only; anything else, including unset,
// dispatches to HTTP). This is the single place defining WS eligibility;
// buildRuntimeProviderFactory's dispatch and cliRunner.runtimeProvider's
// caching both consult it.
func isCodexWSDispatch(rm provider.ResolvedModel) bool {
	providerType := rm.EffectiveProviderType
	if providerType == "" {
		providerType = rm.ProviderConfig.Type
	}
	if providerType != config.ProviderTypeCodex {
		return false
	}
	return rm.ProviderConfig.Codex.Transport == config.CodexTransportWebSocket
}

func runtimeProviderConfig(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) provider.ClientConfig {
	return provider.ClientConfig{
		BaseURL: rm.ProviderConfig.BaseURL,
		APIKey:  rm.ProviderConfig.APIKey,
		Headers: rm.ProviderConfig.Headers,
		Model:   rm.BackendModelID,
		Timeout: time.Duration(rm.ProviderConfig.Timeout.Duration()),
		Retry: provider.RetryConfig{
			Enabled:        rm.Retry.Enabled,
			MaxAttempts:    rm.Retry.MaxAttempts,
			InitialBackoff: time.Duration(rm.Retry.InitialBackoff.Duration()),
			MaxBackoff:     time.Duration(rm.Retry.MaxBackoff.Duration()),
			RetryAfterMax:  time.Duration(rm.Retry.RetryAfterMax.Duration()),
		},
		ProviderType:       string(providerType),
		HTTPClient:         httpClient,
		StreamErrorLog:     streamErrorLog,
		MinRequestInterval: time.Duration(rm.ProviderConfig.Codex.MinRequestInterval.Duration()),
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
