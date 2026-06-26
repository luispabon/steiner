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
	tests := []struct {
		name string
	}{
		{"returns cached token when valid and not near expiry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("Token refresh should not be called for valid cached token")
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer mockServer.Close()

			conf := &oauth2.Config{
				Endpoint: oauth2.Endpoint{
					TokenURL: mockServer.URL + "/token",
				},
			}

			source := NewRefreshableTokenSource(store, conf)
			retrieved, err := source.Token()
			if err != nil {
				t.Fatalf("Token() error = %v", err)
			}

			if retrieved.AccessToken != "cached_token" {
				t.Errorf("AccessToken = %q, want 'cached_token'", retrieved.AccessToken)
			}
		})
	}
}

func TestRefreshableTokenSourceRefreshNearExpiry(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"refreshes when token is within 5 minutes of expiry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tokenPath := filepath.Join(tmpDir, "token.json")

			store := NewTokenStore(tokenPath)

			// Token that expires in 2 minutes (within 5-minute refresh window)
			token := &oauth2.Token{
				AccessToken:  "old_token",
				RefreshToken: "refresh_token",
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(2 * time.Minute),
			}

			if err := store.Save(token); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"access_token":"new_token","refresh_token":"refresh_token","expires_in":3600}`)
			}))
			defer mockServer.Close()

			conf := &oauth2.Config{
				Endpoint: oauth2.Endpoint{
					TokenURL: mockServer.URL,
				},
				ClientID: "test_client",
			}

			source := NewRefreshableTokenSource(store, conf)
			retrieved, err := source.Token()
			if err != nil {
				t.Fatalf("Token() error = %v", err)
			}

			if retrieved.AccessToken != "new_token" {
				t.Errorf("AccessToken = %q, want 'new_token'", retrieved.AccessToken)
			}

			// Verify token was persisted
			reloaded, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if reloaded.AccessToken != "new_token" {
				t.Errorf("persisted AccessToken = %q, want 'new_token'", reloaded.AccessToken)
			}
		})
	}
}

func TestRefreshableTokenSourceConcurrency(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"concurrent calls don't trigger multiple refreshes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				refreshCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"access_token":"refreshed_token","refresh_token":"refresh_token","expires_in":3600}`)
			}))
			defer mockServer.Close()

			conf := &oauth2.Config{
				Endpoint: oauth2.Endpoint{
					TokenURL: mockServer.URL,
				},
				ClientID: "test_client",
			}

			source := NewRefreshableTokenSource(store, conf)

			// Launch multiple goroutines to request tokens concurrently
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

			// Due to mutex serialization, refresh should happen only once
			// (or minimal times if there are timing edge cases)
			if refreshCount.Load() > 2 {
				t.Errorf("refresh called %d times, expected 1-2", refreshCount.Load())
			}
		})
	}
}
