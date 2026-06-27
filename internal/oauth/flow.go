package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// Codex OAuth client identity and endpoints shared by the login command
// and the codex provider transport.
const (
	CodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexAuthURL      = "https://auth.openai.com/oauth/authorize"
	CodexTokenURL     = "https://auth.openai.com/oauth/token"
	CodexRedirectURI  = "http://localhost:1455/auth/callback"
	codexCallbackPort = 1455
)

// FlowConfig holds configuration for the OAuth authorization code flow.
type FlowConfig struct {
	Endpoint     oauth2.Endpoint
	ClientID     string
	RedirectURI  string // full redirect URI; must match what's registered with the provider
	CallbackPort int    // local port to listen on for the callback
	CallbackPath string // URL path for the callback handler (e.g. "/auth/callback")
	Scopes       []string
	ExtraParams  map[string]string      // additional auth URL parameters (e.g. "audience")
	OpenBrowser  func(url string) error // if nil, uses platform default
	OnAuthURL    func(url string)       // if set, called with the full auth URL before opening browser
}

// RunAuthCodeFlow executes the OAuth 2.0 authorization code flow with PKCE.
func RunAuthCodeFlow(ctx context.Context, cfg FlowConfig) (*oauth2.Token, error) {
	verifier, err := GenerateVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate pkce verifier: %w", err)
	}

	// Generate CSRF state
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	callbackPath := cfg.CallbackPath
	if callbackPath == "" {
		callbackPath = "/callback"
	}

	// Start local callback server
	listener, port, err := startCallbackServer(cfg.CallbackPort)
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	redirectURI := cfg.RedirectURI
	if redirectURI == "" {
		redirectURI = fmt.Sprintf("http://localhost:%d%s", port, callbackPath)
	}

	// Build OAuth2 config
	conf := &oauth2.Config{
		ClientID:    cfg.ClientID,
		Endpoint:    cfg.Endpoint,
		Scopes:      cfg.Scopes,
		RedirectURL: redirectURI,
	}

	// Build auth URL with PKCE: S256ChallengeOption takes the verifier and computes
	// the challenge internally; we must not pre-hash it ourselves.
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	for k, v := range cfg.ExtraParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}
	authURL := conf.AuthCodeURL(state, opts...)

	if cfg.OnAuthURL != nil {
		cfg.OnAuthURL(authURL)
	}

	// Open browser
	launcher := cfg.OpenBrowser
	if launcher == nil {
		launcher = openBrowser
	}
	if err := launcher(authURL); err != nil {
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

	go serveCallback(listener, callbackPath, state, codeChan, errChan)

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

// startCallbackServer starts a local HTTP server on the given port (0 = OS-assigned).
func startCallbackServer(port int) (net.Listener, int, error) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return nil, 0, fmt.Errorf("listen on localhost:%d: %w", port, err)
	}
	return l, l.Addr().(*net.TCPAddr).Port, nil
}

// serveCallback runs the HTTP callback handler and sends the auth code to codeChan.
func serveCallback(listener net.Listener, path, expectedState string, codeChan chan string, errChan chan error) {
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			_ = listener.Close()
		}()

		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body>Authentication failed: %s</body></html>", html.EscapeString(errParam))
			errChan <- fmt.Errorf("auth error: %s", errParam)
			return
		}

		if state != expectedState {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body>State mismatch</body></html>")
			errChan <- fmt.Errorf("state mismatch")
			return
		}

		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body>No authorization code received</body></html>")
			errChan <- fmt.Errorf("no authorization code")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "<html><body>Authentication successful. You can close this tab.</body></html>")
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
	c := exec.CommandContext(context.Background(), cmd, args...)
	if err := c.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	return nil
}
