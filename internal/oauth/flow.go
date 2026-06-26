package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// FlowConfig holds configuration for the OAuth authorization code flow.
type FlowConfig struct {
	Endpoint     oauth2.Endpoint
	ClientID     string
	CallbackPort int
	Scopes       []string
}

// RunAuthCodeFlow executes the OAuth 2.0 authorization code flow with PKCE.
func RunAuthCodeFlow(ctx context.Context, cfg FlowConfig) (*oauth2.Token, error) {
	verifier, err := GenerateVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate pkce verifier: %w", err)
	}

	challenge := ChallengeS256(verifier)

	// Generate CSRF state
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	// Start local callback server
	listener, port, err := startCallbackServer(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer listener.Close()

	// Build OAuth2 config
	conf := &oauth2.Config{
		ClientID:    cfg.ClientID,
		Endpoint:    cfg.Endpoint,
		Scopes:      cfg.Scopes,
		RedirectURL: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
	}

	// Build auth URL with PKCE challenge
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(challenge))

	// Open browser
	if err := openBrowser(authURL); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}

	// Wait for callback (with timeout if no deadline set)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
	}

	// Serve HTTP and wait for callback
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go serveCallback(listener, state, codeChan, errChan)

	var code string
	select {
	case code = <-codeChan:
	case err := <-errChan:
		return nil, fmt.Errorf("callback error: %w", err)
	case <-ctx.Done():
		return nil, fmt.Errorf("auth flow cancelled: %w", ctx.Err())
	}

	// Exchange code for token
	token, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange code for token: %w", err)
	}

	return token, nil
}

// generateState creates a random CSRF state (16 bytes, hex-encoded).
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// startCallbackServer starts a local HTTP server and returns the listener and bound port.
// Tries CallbackPort; if that fails, tries up to CallbackPort+10.
func startCallbackServer(ctx context.Context, state string) (net.Listener, int, error) {
	var listener net.Listener
	var port int

	// Try to bind to the preferred port or next available
	for offset := 0; offset <= 10; offset++ {
		p := 8080 + offset
		addr := fmt.Sprintf("127.0.0.1:%d", p)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			listener = l
			port = p
			break
		}
	}

	if listener == nil {
		return nil, 0, fmt.Errorf("could not bind to any port in range 8080-8090")
	}

	return listener, port, nil
}

// serveCallback runs the HTTP callback handler and sends the auth code to codeChan.
func serveCallback(listener net.Listener, expectedState string, codeChan chan string, errChan chan error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		defer listener.Close()

		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body>Authentication failed: %s</body></html>", errParam)
			errChan <- fmt.Errorf("auth error: %s", errParam)
			return
		}

		if state != expectedState {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body>State mismatch</body></html>")
			errChan <- fmt.Errorf("state mismatch")
			return
		}

		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body>No authorization code received</body></html>")
			errChan <- fmt.Errorf("no authorization code")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "<html><body>Authentication successful. You can close this tab.</body></html>")
		codeChan <- code
	})

	_ = http.Serve(listener, mux)
}

// openBrowser opens the given URL in the default browser.
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// Use Start rather than Run to not wait for the browser to close
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	return nil
}
