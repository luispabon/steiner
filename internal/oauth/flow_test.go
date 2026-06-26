package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestRunAuthCodeFlowSuccess(t *testing.T) {
	// Mock token server: handles POST /token and returns a JSON token response.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"test_token","token_type":"Bearer","expires_in":3600}`)
		}
	}))
	defer mockServer.Close()

	cfg := FlowConfig{
		Endpoint: oauth2.Endpoint{
			AuthURL:  mockServer.URL + "/auth",
			TokenURL: mockServer.URL + "/token",
		},
		ClientID:     "test_client",
		CallbackPort: 18990,
		Scopes:       []string{"openid", "profile"},
		// OpenBrowser receives the full auth URL; it extracts the state and the
		// redirect_uri (which carries the actual bound port) then simulates the
		// browser redirect by calling the callback endpoint.
		OpenBrowser: func(authURL string) error {
			go func() {
				time.Sleep(50 * time.Millisecond)
				u, err := url.Parse(authURL)
				if err != nil {
					return
				}
				state := u.Query().Get("state")
				redirectURI := u.Query().Get("redirect_uri")
				ru, err := url.Parse(redirectURI)
				if err != nil {
					return
				}
				callbackURL := fmt.Sprintf("http://127.0.0.1:%s/callback?code=test_code&state=%s", ru.Port(), state)
				resp, err := http.Get(callbackURL) //nolint:noctx
				if err != nil {
					return
				}
				_ = resp.Body.Close()
			}()
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token, err := RunAuthCodeFlow(ctx, cfg)
	if err != nil {
		t.Fatalf("RunAuthCodeFlow() error = %v", err)
	}
	if token.AccessToken != "test_token" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "test_token")
	}
}

func TestRunAuthCodeFlowContextCancellation(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"test_token","token_type":"Bearer"}`)
	}))
	defer mockServer.Close()

	cfg := FlowConfig{
		Endpoint: oauth2.Endpoint{
			AuthURL:  mockServer.URL + "/auth",
			TokenURL: mockServer.URL + "/token",
		},
		ClientID:     "test_client",
		CallbackPort: 8080,
		Scopes:       []string{"openid"},
		OpenBrowser:  func(url string) error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Should return context error due to timeout
	_, err := RunAuthCodeFlow(ctx, cfg)
	if err == nil {
		t.Errorf("RunAuthCodeFlow() expected error on context cancellation, got nil")
	}

	// Verify it's a context-related error
	if ctx.Err() == nil {
		t.Errorf("context not cancelled as expected")
	}
}

func TestGenerateState(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"generates unique states"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state1, err := generateState()
			if err != nil {
				t.Fatalf("generateState() error = %v", err)
			}

			state2, err := generateState()
			if err != nil {
				t.Fatalf("generateState() error = %v", err)
			}

			if len(state1) != 32 {
				t.Errorf("state length = %d, want 32 (16 bytes hex-encoded)", len(state1))
			}

			if state1 == state2 {
				t.Errorf("states should be unique, got same value twice")
			}
		})
	}
}
