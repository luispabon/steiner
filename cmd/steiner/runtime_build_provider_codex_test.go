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
	if first.AccountID != "acct-123" || second.AccountID != "acct-123" {
		t.Errorf("account IDs = %q, %q, want acct-123", first.AccountID, second.AccountID)
	}
}
