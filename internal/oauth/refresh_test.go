package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestRefreshableTokenSourceCached(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)
	token := &oauth2.Token{
		AccessToken:  "cached_token",
		RefreshToken: "refresh_token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Token refresh should not be called for valid cached token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	conf := &oauth2.Config{
		Endpoint: oauth2.Endpoint{
			TokenURL: mockServer.URL + "/token",
		},
	}

	source := NewRefreshableTokenSource(store, conf, token)
	retrieved, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	if retrieved.AccessToken != "cached_token" {
		t.Errorf("AccessToken = %q, want 'cached_token'", retrieved.AccessToken)
	}
}

func TestRefreshableTokenSourceRefreshNearExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)

	token := &oauth2.Token{
		AccessToken:  "old_token",
		RefreshToken: "refresh_token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(2 * time.Minute),
	}

	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"new_token","refresh_token":"refresh_token","expires_in":3600,"id_token":"new-id","%s":"new-account"}`, chatGPTAccountIDExtraKey)
	}))
	defer mockServer.Close()

	conf := &oauth2.Config{
		Endpoint: oauth2.Endpoint{
			TokenURL: mockServer.URL,
		},
		ClientID: "test_client",
	}

	source := NewRefreshableTokenSource(store, conf, token)
	retrieved, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	if retrieved.AccessToken != "new_token" {
		t.Errorf("AccessToken = %q, want 'new_token'", retrieved.AccessToken)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if reloaded.AccessToken != "new_token" {
		t.Errorf("persisted AccessToken = %q, want 'new_token'", reloaded.AccessToken)
	}
	if reloaded.Extra("id_token") != "new-id" {
		t.Errorf("persisted Extra(id_token) = %v, want new-id", reloaded.Extra("id_token"))
	}
	if reloaded.Extra(chatGPTAccountIDExtraKey) != "new-account" {
		t.Errorf("persisted Extra(account_id) = %v, want new-account", reloaded.Extra(chatGPTAccountIDExtraKey))
	}
}

func TestRefreshableTokenSourceConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)

	token := &oauth2.Token{
		AccessToken:  "initial_token",
		RefreshToken: "refresh_token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(2 * time.Minute),
	}

	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var refreshCount atomic.Int32

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"refreshed_token","refresh_token":"refresh_token","expires_in":3600}`)
	}))
	defer mockServer.Close()

	conf := &oauth2.Config{
		Endpoint: oauth2.Endpoint{
			TokenURL: mockServer.URL,
		},
		ClientID: "test_client",
	}

	source := NewRefreshableTokenSource(store, conf, token)

	var wg sync.WaitGroup
	numGoroutines := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retrieved, err := source.Token()
			if err != nil {
				t.Errorf("Token() error = %v", err)
			}
			if retrieved.AccessToken != "refreshed_token" {
				t.Errorf("AccessToken = %q, want 'refreshed_token'", retrieved.AccessToken)
			}
		}()
	}

	wg.Wait()

	if refreshCount.Load() > 2 {
		t.Errorf("refresh called %d times, expected 1-2", refreshCount.Load())
	}
}

func TestRefreshableTokenSourcePreservesMetadataWhenResponseOmitsIt(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)
	token := (&oauth2.Token{
		AccessToken:  "old_token",
		RefreshToken: "refresh_token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(2 * time.Minute),
	}).WithExtra(map[string]any{
		"id_token":               "existing-id",
		chatGPTAccountIDExtraKey: "existing-account",
	})

	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"new_token","refresh_token":"refresh_token","expires_in":3600}`)
	}))
	defer mockServer.Close()

	conf := &oauth2.Config{
		Endpoint: oauth2.Endpoint{
			TokenURL: mockServer.URL,
		},
		ClientID: "test_client",
	}

	source := NewRefreshableTokenSource(store, conf, token)
	retrieved, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	if retrieved.AccessToken != "new_token" {
		t.Errorf("AccessToken = %q, want 'new_token'", retrieved.AccessToken)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if reloaded.Extra("id_token") != "existing-id" {
		t.Errorf("persisted Extra(id_token) = %v, want existing-id", reloaded.Extra("id_token"))
	}
	if reloaded.Extra(chatGPTAccountIDExtraKey) != "existing-account" {
		t.Errorf("persisted Extra(account_id) = %v, want existing-account", reloaded.Extra(chatGPTAccountIDExtraKey))
	}
}

func TestTokenPersistenceEqual(t *testing.T) {
	tests := []struct {
		name      string
		a         *oauth2.Token
		b         *oauth2.Token
		wantEqual bool
	}{
		{
			name:      "both nil",
			a:         nil,
			b:         nil,
			wantEqual: true,
		},
		{
			name: "first nil, second not",
			a:    nil,
			b: &oauth2.Token{
				AccessToken: "token",
			},
			wantEqual: false,
		},
		{
			name: "first not nil, second nil",
			a: &oauth2.Token{
				AccessToken: "token",
			},
			b:         nil,
			wantEqual: false,
		},
		{
			name: "identical tokens",
			a: &oauth2.Token{
				AccessToken:  "test_access",
				RefreshToken: "test_refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			b: &oauth2.Token{
				AccessToken:  "test_access",
				RefreshToken: "test_refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			wantEqual: true,
		},
		{
			name: "different access token",
			a: &oauth2.Token{
				AccessToken:  "access1",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			},
			b: &oauth2.Token{
				AccessToken:  "access2",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			},
			wantEqual: false,
		},
		{
			name: "different refresh token",
			a: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh1",
				TokenType:    "Bearer",
			},
			b: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh2",
				TokenType:    "Bearer",
			},
			wantEqual: false,
		},
		{
			name: "different token type",
			a: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			},
			b: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "DPoP",
			},
			wantEqual: false,
		},
		{
			name: "different expiry",
			a: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			b: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 13, 0, 0, 0, time.UTC),
			},
			wantEqual: false,
		},
		{
			name: "same instant different timezone",
			a: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			b: &oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 13, 0, 0, 0, time.FixedZone("UTC+1", 3600)),
			},
			wantEqual: true,
		},
		{
			name: "different id_token",
			a: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{"id_token": "id1"}),
			b: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{"id_token": "id2"}),
			wantEqual: false,
		},
		{
			name: "different account_id",
			a: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{chatGPTAccountIDExtraKey: "acct1"}),
			b: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{chatGPTAccountIDExtraKey: "acct2"}),
			wantEqual: false,
		},
		{
			name: "different openai_api_key",
			a: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{OpenAIAPIKeyExtraKey: "key1"}),
			b: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{OpenAIAPIKeyExtraKey: "key2"}),
			wantEqual: false,
		},
		{
			name: "all extras match",
			a: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			}).WithExtra(map[string]any{
				"id_token":               "id123",
				chatGPTAccountIDExtraKey: "acct123",
				OpenAIAPIKeyExtraKey:     "key123",
			}),
			b: (&oauth2.Token{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			}).WithExtra(map[string]any{
				"id_token":               "id123",
				chatGPTAccountIDExtraKey: "acct123",
				OpenAIAPIKeyExtraKey:     "key123",
			}),
			wantEqual: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenPersistenceEqual(tt.a, tt.b)
			if result != tt.wantEqual {
				t.Errorf("tokenPersistenceEqual() = %v, want %v", result, tt.wantEqual)
			}
		})
	}
}
