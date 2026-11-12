package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCodexWebSocketProviderResolvesCredentialsPerDial pins that only the
// WebSocket Codex provider gets a dial-time bearer resolver, and that the
// resolver reads the current stored sign-in instead of the build-time token.
func TestCodexWebSocketProviderResolvesCredentialsPerDial(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	writeToken := func(accessToken string) {
		t.Helper()
		token := `{"access_token":"` + accessToken + `","refresh_token":"refresh-token","token_type":"Bearer","account_id":"acct-123"}`
		if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
	}
	writeToken("token-one")

	oldWS, oldHTTP := newCodexResponsesWS, newCodexResponses
	t.Cleanup(func() {
		newCodexResponsesWS = oldWS
		newCodexResponses = oldHTTP
	})
	var wsCfg, httpCfg provider.ClientConfig
	newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
		wsCfg = cfg
		return &fakeProvider{}, nil
	}
	newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
		httpCfg = cfg
		return &fakeProvider{}, nil
	}

	factory := buildRuntimeProviderFactory(&http.Client{}, nil, nil, nil)
	wsModel := provider.ResolvedModel{
		Alias:                 "codex",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex, Codex: config.CodexConfig{Transport: config.CodexTransportWebSocket}},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	if _, err := factory(wsModel, "test-session"); err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	if wsCfg.DialBearerResolver == nil {
		t.Fatal("WebSocket Codex provider has no dial-time bearer resolver")
	}

	httpModel := wsModel
	httpModel.ProviderConfig.Codex.Transport = config.CodexTransportHTTP
	if _, err := factory(httpModel, "test-session"); err != nil {
		t.Fatalf("factory() HTTP error = %v", err)
	}
	if httpCfg.DialBearerResolver != nil {
		t.Fatal("HTTP Codex provider has a dial-time bearer resolver, want nil")
	}
	if httpCfg.CodexSignIn != nil {
		t.Fatal("HTTP Codex provider has a sign-in identity, want nil")
	}

	wantSignIn := provider.CodexSignInIdentity{AccountID: "acct-123"}
	if wsCfg.CodexSignIn == nil || *wsCfg.CodexSignIn != wantSignIn {
		t.Fatalf("CodexSignIn = %+v, want %+v", wsCfg.CodexSignIn, wantSignIn)
	}

	first, err := wsCfg.DialBearerResolver(context.Background())
	if err != nil {
		t.Fatalf("resolve first dial credentials: %v", err)
	}
	writeToken("token-two-longer")
	second, err := wsCfg.DialBearerResolver(context.Background())
	if err != nil {
		t.Fatalf("resolve second dial credentials: %v", err)
	}

	if first.AccessToken != "token-one" {
		t.Errorf("first resolution token = %q, want token-one", first.AccessToken)
	}
	if second.AccessToken != "token-two-longer" {
		t.Errorf("second resolution token = %q, want the re-read sign-in", second.AccessToken)
	}
	for _, resolved := range []provider.CodexAuth{first, second} {
		if resolved.AccountID != "acct-123" {
			t.Errorf("account ID = %q, want acct-123", resolved.AccountID)
		}
		if got := resolved.SignInIdentity(); got != wantSignIn {
			t.Errorf("resolved sign-in identity = %+v, want the build-time %+v", got, wantSignIn)
		}
	}
}

// TestCodexWebSocketProviderCapturesAPIModeSignIn pins that API-key mode comes
// from the resolved token, not from a configured ChatGPT-Account-ID header, so
// an API-key user who sets that header still dials.
func TestCodexWebSocketProviderCapturesAPIModeSignIn(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	token := `{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","openai_api_key":"sk-codex","account_id":"acct-123"}`
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	oldWS := newCodexResponsesWS
	t.Cleanup(func() { newCodexResponsesWS = oldWS })
	var wsCfg provider.ClientConfig
	newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
		wsCfg = cfg
		return &fakeProvider{}, nil
	}

	factory := buildRuntimeProviderFactory(&http.Client{}, nil, nil, nil)
	wsModel := provider.ResolvedModel{
		Alias: "codex",
		ProviderConfig: config.ProviderConfig{
			Type:    config.ProviderTypeCodex,
			BaseURL: "https://api.openai.com/v1",
			Headers: map[string]string{"ChatGPT-Account-ID": "acct-user-header"},
			Codex:   config.CodexConfig{Transport: config.CodexTransportWebSocket},
		},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	if _, err := factory(wsModel, "test-session"); err != nil {
		t.Fatalf("factory() error = %v", err)
	}

	wantSignIn := provider.CodexSignInIdentity{APIKeyMode: true}
	if wsCfg.CodexSignIn == nil || *wsCfg.CodexSignIn != wantSignIn {
		t.Fatalf("CodexSignIn = %+v, want %+v (API mode from the token, not the header)", wsCfg.CodexSignIn, wantSignIn)
	}
	if wsCfg.DialBearerResolver == nil {
		t.Fatal("WebSocket Codex provider has no dial-time bearer resolver")
	}
	resolved, err := wsCfg.DialBearerResolver(context.Background())
	if err != nil {
		t.Fatalf("resolve dial credentials: %v", err)
	}
	if got := resolved.SignInIdentity(); got != wantSignIn {
		t.Fatalf("resolved sign-in identity = %+v, want %+v: a dial would fail closed", got, wantSignIn)
	}
	if resolved.APIKey != "sk-codex" {
		t.Fatalf("resolved API key = %q, want sk-codex", resolved.APIKey)
	}
}

// TestCodexProviderBindsAccountHeaderCaseInsensitively pins that a ChatGPT
// sign-in replaces every case variant of the account header, so a configured
// "chatgpt-account-id" cannot be sent next to the injected header under map
// iteration order, and that an API-key sign-in leaves configured headers alone.
func TestCodexProviderBindsAccountHeaderCaseInsensitively(t *testing.T) {
	const (
		chatGPTToken = `{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","account_id":"acct-123"}`
		apiKeyToken  = `{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","openai_api_key":"sk-codex","account_id":"acct-123"}`
	)

	tests := []struct {
		name       string
		token      string
		headers    map[string]string
		apiKeyMode bool
		wantHeader string
		wantCount  int
	}{
		{
			name:       "ChatGPT sign-in replaces mixed-case variants",
			token:      chatGPTToken,
			headers:    map[string]string{"chatgpt-account-id": "acct-user-one", "CHATGPT-ACCOUNT-ID": "acct-user-two", "X-Other": "kept"},
			wantHeader: "acct-123",
			wantCount:  1,
		},
		{
			name:       "ChatGPT sign-in replaces a lower-case header",
			token:      chatGPTToken,
			headers:    map[string]string{"chatgpt-account-id": "acct-user-one"},
			wantHeader: "acct-123",
			wantCount:  1,
		},
		{
			name:       "API-key sign-in keeps the configured header",
			token:      apiKeyToken,
			headers:    map[string]string{"chatgpt-account-id": "acct-user-one"},
			apiKeyMode: true,
			wantHeader: "acct-user-one",
			wantCount:  1,
		},
		{
			name:       "API-key sign-in sends no account header",
			token:      apiKeyToken,
			apiKeyMode: true,
		},
	}

	for _, transport := range []config.CodexTransport{config.CodexTransportWebSocket, config.CodexTransportHTTP} {
		for _, tt := range tests {
			t.Run(tt.name+"/"+string(transport), func(t *testing.T) {
				configHome := t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", configHome)
				tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
				if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
					t.Fatalf("mkdir token dir: %v", err)
				}
				if err := os.WriteFile(tokenPath, []byte(tt.token), 0o600); err != nil {
					t.Fatalf("write token: %v", err)
				}

				oldWS, oldHTTP := newCodexResponsesWS, newCodexResponses
				t.Cleanup(func() {
					newCodexResponsesWS = oldWS
					newCodexResponses = oldHTTP
				})
				var wsCfg, httpCfg provider.ClientConfig
				newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
					wsCfg = cfg
					return &fakeProvider{}, nil
				}
				newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
					httpCfg = cfg
					return &fakeProvider{}, nil
				}

				factory := buildRuntimeProviderFactory(&http.Client{}, nil, nil, nil)
				rm := provider.ResolvedModel{
					Alias: "codex",
					ProviderConfig: config.ProviderConfig{
						Type:    config.ProviderTypeCodex,
						BaseURL: "https://api.openai.com/v1",
						Headers: tt.headers,
						Codex:   config.CodexConfig{Transport: transport},
					},
					BackendModelID:        "codex-default",
					EffectiveProviderType: config.ProviderTypeCodex,
				}
				if _, err := factory(rm, "test-session"); err != nil {
					t.Fatalf("factory() error = %v", err)
				}

				got := httpCfg
				if transport == config.CodexTransportWebSocket {
					got = wsCfg
				}
				count, value := 0, ""
				for key, headerValue := range got.Headers {
					if strings.EqualFold(key, "ChatGPT-Account-ID") {
						count++
						value = headerValue
					}
				}
				if count != tt.wantCount {
					t.Errorf("account header variants = %d (%v), want %d", count, got.Headers, tt.wantCount)
				}
				if value != tt.wantHeader {
					t.Errorf("account header = %q, want %q", value, tt.wantHeader)
				}
				if _, ok := tt.headers["X-Other"]; ok && got.Headers["X-Other"] != "kept" {
					t.Errorf("X-Other = %q, want the configured header preserved", got.Headers["X-Other"])
				}
				if transport == config.CodexTransportWebSocket {
					if got.CodexSignIn == nil || got.CodexSignIn.APIKeyMode != tt.apiKeyMode {
						t.Errorf("CodexSignIn = %+v, want API-key mode %v", got.CodexSignIn, tt.apiKeyMode)
					}
				}
			})
		}
	}
}
