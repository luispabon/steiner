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
