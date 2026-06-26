package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStoreSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	tests := []struct {
		name  string
		token *oauth2.Token
	}{
		{
			name: "token with expiry",
			token: &oauth2.Token{
				AccessToken:  "access123",
				RefreshToken: "refresh456",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "token with metadata",
			token: (&oauth2.Token{
				AccessToken:  "access-meta",
				RefreshToken: "refresh-meta",
				TokenType:    "Bearer",
			}).WithExtra(map[string]any{
				"id_token":               "id-123",
				chatGPTAccountIDExtraKey: "acct-456",
				OpenAIAPIKeyExtraKey:     "sk-codex",
				"unrelated_metadata":     "ignored",
			}),
		},
		{
			name: "token without expiry",
			token: &oauth2.Token{
				AccessToken:  "access789",
				RefreshToken: "refresh000",
				TokenType:    "Bearer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore(tokenPath)

			if err := store.Save(tt.token); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			// Check file permissions
			fi, err := os.Stat(tokenPath)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if fi.Mode()&0o077 != 0 {
				t.Errorf("file permissions = %o, want 0o600", fi.Mode().Perm())
			}

			loaded, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if loaded.AccessToken != tt.token.AccessToken {
				t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, tt.token.AccessToken)
			}
			if loaded.RefreshToken != tt.token.RefreshToken {
				t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, tt.token.RefreshToken)
			}
			if loaded.TokenType != tt.token.TokenType {
				t.Errorf("TokenType = %q, want %q", loaded.TokenType, tt.token.TokenType)
			}

			if got := loaded.Extra("id_token"); got != tt.token.Extra("id_token") {
				t.Errorf("Extra(id_token) = %v, want %v", got, tt.token.Extra("id_token"))
			}
			if got := loaded.Extra(chatGPTAccountIDExtraKey); got != tt.token.Extra(chatGPTAccountIDExtraKey) {
				t.Errorf("Extra(account_id) = %v, want %v", got, tt.token.Extra(chatGPTAccountIDExtraKey))
			}
			if got := TokenOpenAIAPIKey(loaded); got != TokenOpenAIAPIKey(tt.token) {
				t.Errorf("TokenOpenAIAPIKey() = %v, want %v", got, TokenOpenAIAPIKey(tt.token))
			}

			// Check expiry (handles zero time specially)
			if tt.token.Expiry.IsZero() && loaded.Expiry.IsZero() {
				// Both zero, OK
			} else if !tt.token.Expiry.Equal(loaded.Expiry) {
				t.Errorf("Expiry = %v, want %v", loaded.Expiry, tt.token.Expiry)
			}

			// Clean up for next iteration
			if err := os.Remove(tokenPath); err != nil {
				t.Fatalf("Remove() error = %v", err)
			}
		})
	}
}

func TestTokenStoreDerivesAccountIDFromIDToken(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")
	idToken := unsignedJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-id-token",
		},
	})

	store := NewTokenStore(tokenPath)
	token := (&oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
	}).WithExtra(map[string]any{"id_token": idToken})

	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := TokenChatGPTAccountID(loaded); got != "acct-from-id-token" {
		t.Fatalf("TokenChatGPTAccountID() = %q, want acct-from-id-token", got)
	}
}

func TestTokenStoreLoadLegacyFormat(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	legacy := map[string]any{
		"access_token":  "legacy-access",
		"refresh_token": "legacy-refresh",
		"token_type":    "Bearer",
		"expiry":        time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := NewTokenStore(tokenPath)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.AccessToken != "legacy-access" {
		t.Errorf("AccessToken = %q, want legacy-access", loaded.AccessToken)
	}
	if loaded.Extra("id_token") != nil {
		t.Errorf("Extra(id_token) = %v, want nil", loaded.Extra("id_token"))
	}
	if loaded.Extra(chatGPTAccountIDExtraKey) != nil {
		t.Errorf("Extra(account_id) = %v, want nil", loaded.Extra(chatGPTAccountIDExtraKey))
	}
	if got := TokenOpenAIAPIKey(loaded); got != "" {
		t.Errorf("TokenOpenAIAPIKey() = %q, want empty", got)
	}
}

func unsignedJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func TestTokenStoreLoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "nonexistent.json")

	store := NewTokenStore(tokenPath)
	_, err := store.Load()

	if !errors.Is(err, ErrNoToken) {
		t.Errorf("Load() error = %v, want ErrNoToken", err)
	}
}

func TestTokenStoreAtomicOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)

	token1 := &oauth2.Token{
		AccessToken:  "first",
		RefreshToken: "refresh1",
		TokenType:    "Bearer",
	}
	if err := store.Save(token1); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}

	token2 := &oauth2.Token{
		AccessToken:  "second",
		RefreshToken: "refresh2",
		TokenType:    "Bearer",
	}
	if err := store.Save(token2); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.AccessToken != "second" {
		t.Errorf("AccessToken = %q, want 'second'", loaded.AccessToken)
	}
}

func TestTokenStoreSavePreservesExistingMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)

	original := (&oauth2.Token{
		AccessToken:  "first",
		RefreshToken: "refresh1",
		TokenType:    "Bearer",
	}).WithExtra(map[string]any{
		"id_token":               "id-original",
		chatGPTAccountIDExtraKey: "acct-original",
		OpenAIAPIKeyExtraKey:     "sk-original",
	})
	if err := store.Save(original); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}

	updated := &oauth2.Token{
		AccessToken:  "second",
		RefreshToken: "refresh2",
		TokenType:    "Bearer",
	}
	if err := store.Save(updated); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.Extra("id_token") != "id-original" {
		t.Errorf("Extra(id_token) = %v, want id-original", loaded.Extra("id_token"))
	}
	if loaded.Extra(chatGPTAccountIDExtraKey) != "acct-original" {
		t.Errorf("Extra(account_id) = %v, want acct-original", loaded.Extra(chatGPTAccountIDExtraKey))
	}
	if got := TokenOpenAIAPIKey(loaded); got != "sk-original" {
		t.Errorf("TokenOpenAIAPIKey() = %q, want sk-original", got)
	}
}

func TestDefaultTokenPath(t *testing.T) {
	path, err := DefaultTokenPath()
	if err != nil {
		t.Fatalf("DefaultTokenPath() error = %v", err)
	}

	if path == "" {
		t.Errorf("DefaultTokenPath() returned empty string")
	}

	if !filepath.IsAbs(path) {
		t.Errorf("DefaultTokenPath() = %q, not absolute", path)
	}

	if filepath.Base(path) != "codex_auth.json" {
		t.Errorf("DefaultTokenPath() base = %q, want codex_auth.json", filepath.Base(path))
	}
}
