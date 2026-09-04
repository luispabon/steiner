package oauth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestRunAuthCodeFlowSuccess(t *testing.T) {
	// Mock token server: handles POST /token and returns a JSON token response.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"test_token","token_type":"Bearer","expires_in":3600}`)
		}
	}))
	defer mockServer.Close()

	cfg := FlowConfig{
		Endpoint: oauth2.Endpoint{
			AuthURL:  mockServer.URL + "/auth",
			TokenURL: mockServer.URL + "/token",
		},
		ClientID:     "test_client",
		CallbackPort: 0, // OS-assigned
		CallbackPath: "/callback",
		Scopes:       []string{"openid", "profile"},
		// OpenBrowser receives the full auth URL; it extracts the state and the
		// redirect_uri (which carries the actual bound port) then simulates the
		// browser redirect by calling the callback endpoint.
		OpenBrowser: func(authURL string) error {
			go func() {
				time.Sleep(1 * time.Millisecond)
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
				callbackURL := fmt.Sprintf("http://localhost:%s/callback?code=test_code&state=%s", ru.Port(), state)
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
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"test_token","token_type":"Bearer"}`)
	}))
	defer mockServer.Close()

	cfg := FlowConfig{
		Endpoint: oauth2.Endpoint{
			AuthURL:  mockServer.URL + "/auth",
			TokenURL: mockServer.URL + "/token",
		},
		ClientID:     "test_client",
		CallbackPort: 0,
		CallbackPath: "/callback",
		Scopes:       []string{"openid"},
		OpenBrowser:  func(_ string) error { return nil },
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
}

func TestServeCallback(t *testing.T) {
	tests := []struct {
		name          string
		queryParams   url.Values
		expectedState string
		wantCode      string
		wantErrSubstr string
		wantStatusOK  bool
	}{
		{
			name: "success",
			queryParams: url.Values{
				"code":  {"abc"},
				"state": {"expected_state_value"},
			},
			expectedState: "expected_state_value",
			wantCode:      "abc",
			wantStatusOK:  true,
		},
		{
			name: "error param",
			queryParams: url.Values{
				"error": {"access_denied"},
				"state": {"expected_state_value"},
			},
			expectedState: "expected_state_value",
			wantErrSubstr: "auth error: access_denied",
		},
		{
			name: "error param with xss",
			queryParams: url.Values{
				"error": {"<script>alert(1)</script>"},
				"state": {"expected_state_value"},
			},
			expectedState: "expected_state_value",
			wantErrSubstr: "auth error: <script>alert(1)</script>",
		},
		{
			name: "state mismatch",
			queryParams: url.Values{
				"code":  {"abc"},
				"state": {"wrong_state"},
			},
			expectedState: "expected_state_value",
			wantErrSubstr: "state mismatch",
		},
		{
			name: "missing code",
			queryParams: url.Values{
				"state": {"expected_state_value"},
			},
			expectedState: "expected_state_value",
			wantErrSubstr: "no authorization code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lc net.ListenConfig
			l, err := lc.Listen(context.Background(), "tcp", "localhost:0")
			if err != nil {
				t.Fatalf("net.Listen() error = %v", err)
			}
			addr := l.Addr().(*net.TCPAddr)

			codeChan := make(chan string, 1)
			errChan := make(chan error, 1)

			served := make(chan struct{})
			go func() {
				serveCallback(l, "/callback", tt.expectedState, codeChan, errChan)
				close(served)
			}()

			callbackURL := fmt.Sprintf("http://localhost:%d/callback?%s", addr.Port, tt.queryParams.Encode())
			resp, err := http.Get(callbackURL) //nolint:noctx
			if err != nil {
				t.Fatalf("http.Get() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("io.ReadAll() error = %v", err)
			}

			if tt.wantStatusOK && resp.StatusCode != http.StatusOK {
				t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			if !tt.wantStatusOK && resp.StatusCode != http.StatusBadRequest {
				t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}

			select {
			case code := <-codeChan:
				if tt.wantCode == "" {
					t.Errorf("got code %q, expected none", code)
				} else if code != tt.wantCode {
					t.Errorf("code = %q, want %q", code, tt.wantCode)
				}
			case err := <-errChan:
				switch {
				case tt.wantErrSubstr == "":
					t.Errorf("got error %v, expected none", err)
				case err == nil:
					t.Errorf("got nil error, want one containing %q", tt.wantErrSubstr)
				case err.Error() != tt.wantErrSubstr:
					t.Errorf("error = %q, want %q", err.Error(), tt.wantErrSubstr)
				}
			case <-time.After(1 * time.Second):
				t.Errorf("callback channel timeout")
			}

			if tt.wantErrSubstr != "" && tt.name == "error param with xss" {
				bodyStr := string(body)
				if contains(bodyStr, "<script>") {
					t.Errorf("response body contains unescaped <script> tag")
				}
				if !contains(bodyStr, "&lt;script&gt;") {
					t.Errorf("response body should contain escaped &lt;script&gt;")
				}
			}

			select {
			case <-served:
			case <-time.After(2 * time.Second):
				t.Errorf("listener not closed after request")
			}
		})
	}
}

func TestOpenBrowser(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("TestOpenBrowser skipped on %s", runtime.GOOS)
	}

	tmpDir := t.TempDir()
	markerPath := filepath.Join(tmpDir, "marker.txt")

	var binName string
	switch runtime.GOOS {
	case "darwin":
		binName = "open"
	case "linux":
		binName = "xdg-open"
	}

	fakeBinDir := filepath.Join(tmpDir, "bin")
	if err := os.Mkdir(fakeBinDir, 0o755); err != nil {
		t.Fatalf("mkdir() error = %v", err)
	}

	scriptPath := filepath.Join(fakeBinDir, binName)
	scriptContent := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %s\n", markerPath)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("PATH", fakeBinDir)

	err := openBrowser("https://example.test")
	if err != nil {
		t.Errorf("openBrowser() error = %v", err)
	}

	for i := 0; i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
		if _, err := os.Stat(markerPath); err == nil {
			data, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if !contains(string(data), "https://example.test") {
				t.Errorf("marker file = %q, want to contain https://example.test", string(data))
			}
			return
		}
	}
	t.Errorf("marker file not created after 2 seconds")
}

func TestOpenBrowserEmptyPath(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("TestOpenBrowserEmptyPath skipped on %s", runtime.GOOS)
	}

	t.Setenv("PATH", "")

	err := openBrowser("https://example.test")
	if err == nil {
		t.Errorf("openBrowser() expected error with empty PATH, got nil")
	}
	if !contains(err.Error(), "start browser:") || !contains(err.Error(), "$PATH") {
		t.Errorf("error = %q, want to contain 'start browser:' and '$PATH'", err.Error())
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
