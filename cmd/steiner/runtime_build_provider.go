package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oauth"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/quota"
)

// buildRuntimeProviderFactory returns the provider factory for every model
// route, including sub-agents and advisors. Each call marks rm.Alias used for
// quota refresh. quotaService may be nil.
func buildRuntimeProviderFactory(httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger, claudePool *provider.ClaudeSubscriptionPool, quotaService *quota.Service) func(provider.ResolvedModel, string) (provider.Provider, error) {
	return func(rm provider.ResolvedModel, sessionID string) (provider.Provider, error) {
		quotaService.MarkUsed(rm.ProviderAlias)
		if rm.ProviderConfig.Type == config.ProviderTypeOpencodeGo || rm.ProviderConfig.Type == config.ProviderTypeOpencodeZen {
			return newOpencodeProvider(rm, rm.ProviderConfig.Type, httpClient, streamErrorLog, sessionID)
		}

		providerType := effectiveProviderType(rm)
		if providerType == "" {
			return nil, fmt.Errorf("resolved provider type is empty for model %q", rm.Alias)
		}

		switch providerType {
		case config.ProviderTypeClaudeSubscription:
			if claudePool == nil {
				return nil, fmt.Errorf("the claude_subscription provider is not available for this run")
			}
			return provider.NewClaudeSubscriptionProvider(claudePool, streamErrorLog), nil
		case config.ProviderTypeOpenAICompat, config.ProviderTypeOllama, config.ProviderTypeLMStudio,
			config.ProviderTypeOpenRouter, config.ProviderTypeOpenAI, config.ProviderTypeLiteLLM:
			return newOpenAICompat(runtimeProviderConfig(rm, rm.ProviderConfig.Type, httpClient, streamErrorLog))
		case config.ProviderTypeAnthropic:
			return newAnthropic(runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog))
		case config.ProviderTypeCodex:
			return newCodexProvider(rm, providerType, httpClient, streamErrorLog, quotaService)
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

func newCodexProvider(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger, quotaService *quota.Service) (provider.Provider, error) {
	auth, err := provider.ResolveCodexAuth(context.Background(), httpClient)
	if errors.Is(err, oauth.ErrNoToken) {
		return nil, fmt.Errorf("codex provider requires authentication — run 'steiner login codex' first")
	} else if err != nil {
		return nil, err
	}
	cfg := runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog)
	cfg.QuotaObserver = quotaService.Observe
	if auth.APIKey != "" {
		cfg.APIKey = auth.APIKey
	} else {
		if auth.AccountID == "" {
			return nil, fmt.Errorf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		cfg.BaseURL = codexChatGPTBackendURL
		cfg.APIKey = auth.AccessToken
		cfg.Headers = cloneStringMap(cfg.Headers)
		cfg.Headers["ChatGPT-Account-ID"] = auth.AccountID
	}
	if !isCodexWSDispatch(rm) {
		return newCodexResponses(cfg)
	}
	return newCodexResponsesWS(cfg)
}

// effectiveProviderType returns the resolved effective provider type, falling
// back to the configured provider type for models without an override.
func effectiveProviderType(rm provider.ResolvedModel) config.ProviderType {
	if rm.EffectiveProviderType != "" {
		return rm.EffectiveProviderType
	}
	return rm.ProviderConfig.Type
}

// isCodexWSDispatch reports whether rm resolves to a Codex WebSocket
// transport (explicit websocket only; anything else, including unset,
// dispatches to HTTP). This is the single place defining WS eligibility;
// buildRuntimeProviderFactory's dispatch and cliRunner.runtimeProvider's
// caching both consult it.
func isCodexWSDispatch(rm provider.ResolvedModel) bool {
	providerType := effectiveProviderType(rm)
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
