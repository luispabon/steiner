package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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
