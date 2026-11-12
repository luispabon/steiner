package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsDialRecorder is a Codex WebSocket stand-in that records the handshake
// headers of every dial and answers each request frame with a completed
// response, so one connection can serve several requests.
type wsDialRecorder struct {
	*httptest.Server

	mu      sync.Mutex
	headers []http.Header
}

func newWSDialRecorder(t *testing.T) *wsDialRecorder {
	t.Helper()

	completed := mustJSON(t, wsCompleted())
	recorder := &wsDialRecorder{}
	recorder.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.headers = append(recorder.headers, r.Header.Clone())
		recorder.mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, completed)
			writeCancel()
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(recorder.Close)
	return recorder
}

func (r *wsDialRecorder) wsURL() string { return "ws" + r.URL[4:] }

func (r *wsDialRecorder) dialCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.headers)
}

func (r *wsDialRecorder) dialHeaders(i int) http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.headers) {
		return nil
	}
	return r.headers[i]
}

// newDialAuthWSProvider builds a Codex WebSocket provider with a dial-time
// bearer resolver, the given build-time headers and the sign-in identity of the
// credentials those headers describe.
func newDialAuthWSProvider(t *testing.T, wsURL string, headers map[string]string, signIn CodexSignInIdentity, resolver func(context.Context) (CodexAuth, error)) *codexWSProvider {
	t.Helper()

	created, err := NewCodexResponsesWS(ClientConfig{
		BaseURL:            "http://127.0.0.1:1",
		APIKey:             "static-bearer",
		Headers:            headers,
		Model:              "test-model",
		DialBearerResolver: resolver,
		CodexSignIn:        &signIn,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	provider := created.(*codexWSProvider)
	provider.wsURL = wsURL
	t.Cleanup(func() {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		provider.closeConn()
	})
	return provider
}

func wsAuthRequest() ChatRequest {
	return ChatRequest{
		Model:          "test-model",
		PromptCacheKey: "affinity-key",
		Messages:       []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
}

// TestCodexWSDialUsesFreshTokenPerDial proves a reused connection keeps its
// token and affinity, while a redial after a release picks up the current
// token for the same account.
func TestCodexWSDialUsesFreshTokenPerDial(t *testing.T) {
	server := newWSDialRecorder(t)
	tokens := []string{"token-one", "token-two-longer"}
	var mu sync.Mutex
	calls := 0
	chatGPT := CodexSignInIdentity{AccountID: "acct-123"}
	p := newDialAuthWSProvider(t, server.wsURL(), map[string]string{"ChatGPT-Account-ID": "acct-123"}, chatGPT, func(context.Context) (CodexAuth, error) {
		mu.Lock()
		defer mu.Unlock()
		token := tokens[min(calls, len(tokens)-1)]
		calls++
		return CodexAuth{AccessToken: token, AccountID: "acct-123"}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := wsAuthRequest()
	for i := 0; i < 2; i++ {
		if _, err := p.ChatCompletion(ctx, request); err != nil {
			t.Fatalf("ChatCompletion %d: %v", i+1, err)
		}
	}
	if got := server.dialCount(); got != 1 {
		t.Fatalf("dial count = %d, want 1: a live connection is reused", got)
	}
	mu.Lock()
	resolved := calls
	mu.Unlock()
	if resolved != 1 {
		t.Fatalf("resolver calls = %d, want 1 while the connection is reused", resolved)
	}

	ReleaseConnection(p)

	if _, err := p.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("ChatCompletion after release: %v", err)
	}
	if got := server.dialCount(); got != 2 {
		t.Fatalf("dial count = %d, want 2 after a release", got)
	}

	wantTokens := []string{"Bearer token-one", "Bearer token-two-longer"}
	for i, want := range wantTokens {
		headers := server.dialHeaders(i)
		if headers == nil {
			t.Fatalf("dial %d headers missing", i+1)
		}
		if got := headers.Get("Authorization"); got != want {
			t.Errorf("dial %d Authorization = %q, want %q", i+1, got, want)
		}
		if got := headers.Get("ChatGPT-Account-ID"); got != "acct-123" {
			t.Errorf("dial %d ChatGPT-Account-ID = %q, want acct-123", i+1, got)
		}
		for _, name := range []string{"session-id", "thread-id"} {
			if got := headers.Get(name); got != "affinity-key" {
				t.Errorf("dial %d %s = %q, want affinity-key", i+1, name, got)
			}
		}
		if got := headers.Get("originator"); got != "codex_cli_rs" {
			t.Errorf("dial %d originator = %q, want codex_cli_rs", i+1, got)
		}
	}
}

// TestCodexWSDialAPIKeyModeIgnoresAccountHeader pins that API-key mode comes
// from the explicit sign-in identity, not from a configured account header, and
// that the dial sends the freshly resolved key.
func TestCodexWSDialAPIKeyModeIgnoresAccountHeader(t *testing.T) {
	server := newWSDialRecorder(t)
	apiSignIn := CodexSignInIdentity{APIKeyMode: true}
	p := newDialAuthWSProvider(t, server.wsURL(), map[string]string{"ChatGPT-Account-ID": "acct-user-header"}, apiSignIn, func(context.Context) (CodexAuth, error) {
		// An API-key token may carry an account ID; it is irrelevant here
		// because no account identity is kept.
		return CodexAuth{APIKey: "sk-rotated-key", AccountID: "acct-123"}, nil
	})
	if p.dialAuth == nil || p.dialAuth.signIn != apiSignIn {
		t.Fatalf("dial auth = %+v, want the API-key sign-in identity", p.dialAuth)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.ChatCompletion(ctx, wsAuthRequest()); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	dialHeaders := server.dialHeaders(0)
	if got := dialHeaders.Get("Authorization"); got != "Bearer sk-rotated-key" {
		t.Errorf("Authorization = %q, want the resolved API key", got)
	}
	// A user-configured header is passed through, but it never decides the
	// sign-in mode.
	if got := dialHeaders.Get("ChatGPT-Account-ID"); got != "acct-user-header" {
		t.Errorf("ChatGPT-Account-ID = %q, want the configured header", got)
	}
}

// TestCodexWSRequiresSignInIdentityWithResolver pins that a resolver without an
// explicit sign-in identity is rejected instead of inferred from headers.
func TestCodexWSRequiresSignInIdentityWithResolver(t *testing.T) {
	_, err := NewCodexResponsesWS(ClientConfig{
		BaseURL: "http://127.0.0.1:1",
		APIKey:  "static-bearer",
		Headers: map[string]string{"ChatGPT-Account-ID": "acct-123"},
		Model:   "test-model",
		DialBearerResolver: func(context.Context) (CodexAuth, error) {
			return CodexAuth{AccessToken: "token", AccountID: "acct-123"}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "sign-in identity") {
		t.Fatalf("NewCodexResponsesWS error = %v, want a missing sign-in identity error", err)
	}
}

// TestCodexWSDialRejectsChangedSignIn proves a redial fails closed instead of
// pairing new credentials with the build-time account headers, and that the
// failure is neither retried nor leaking the credentials.
func TestCodexWSDialRejectsChangedSignIn(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		signIn  CodexSignInIdentity
		auth    CodexAuth
		want    error
	}{
		{
			name:    "different ChatGPT account",
			headers: map[string]string{"ChatGPT-Account-ID": "acct-123"},
			signIn:  CodexSignInIdentity{AccountID: "acct-123"},
			auth:    CodexAuth{AccessToken: "token-other-account", AccountID: "acct-999"},
			want:    errCodexSignInChanged,
		},
		{
			name:    "ChatGPT sign-in replaced by an API key",
			headers: map[string]string{"ChatGPT-Account-ID": "acct-123"},
			signIn:  CodexSignInIdentity{AccountID: "acct-123"},
			auth:    CodexAuth{APIKey: "sk-live-new", AccountID: "acct-123"},
			want:    errCodexSignInChanged,
		},
		{
			name:   "API-key sign-in replaced by a ChatGPT sign-in",
			signIn: CodexSignInIdentity{APIKeyMode: true},
			auth:   CodexAuth{AccessToken: "token-chatgpt", AccountID: "acct-123"},
			want:   errCodexSignInChanged,
		},
		{
			name:    "ChatGPT sign-in without an access token",
			headers: map[string]string{"ChatGPT-Account-ID": "acct-123"},
			signIn:  CodexSignInIdentity{AccountID: "acct-123"},
			auth:    CodexAuth{AccountID: "acct-123"},
			want:    errCodexSignInNoToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newWSDialRecorder(t)
			p := newDialAuthWSProvider(t, server.wsURL(), tt.headers, tt.signIn, func(context.Context) (CodexAuth, error) {
				return tt.auth, nil
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := p.ChatCompletion(ctx, wsAuthRequest())
			if err == nil {
				t.Fatal("ChatCompletion succeeded, want the changed sign-in to fail the dial")
			}
			if !strings.Contains(err.Error(), tt.want.Error()) {
				t.Errorf("error = %q, want it to report %q", err, tt.want)
			}
			var authErr *wsAuthError
			if !errors.As(err, &authErr) {
				t.Errorf("error = %v, want a *wsAuthError so no dial retry follows", err)
			}
			if got := server.dialCount(); got != 0 {
				t.Errorf("dial count = %d, want 0", got)
			}
			for _, secret := range []string{tt.auth.AccessToken, tt.auth.APIKey} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks a credential: %v", err)
				}
			}
		})
	}
}

// TestCodexWSDialStaticTokenWithoutResolver covers the unchanged path: without
// a resolver every dial uses the configured static bearer.
func TestCodexWSDialStaticTokenWithoutResolver(t *testing.T) {
	server := newWSDialRecorder(t)
	p := newTestWSProvider(t, server.wsURL())
	if p.dialAuth != nil {
		t.Fatalf("dialAuth = %+v, want nil without a resolver", p.dialAuth)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := wsAuthRequest()
	if _, err := p.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	ReleaseConnection(p)
	if _, err := p.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("ChatCompletion after release: %v", err)
	}

	if got := server.dialCount(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		if got := server.dialHeaders(i).Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("dial %d Authorization = %q, want the static bearer", i+1, got)
		}
	}
}

// TestCodexWSDialCredentialResolutionCancellation proves a cancelled resolution
// dials nothing, is not retried, and stays recognizable as a cancellation.
func TestCodexWSDialCredentialResolutionCancellation(t *testing.T) {
	server := newWSDialRecorder(t)
	resolveStarted := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	calls := 0
	p := newDialAuthWSProvider(t, server.wsURL(), map[string]string{"ChatGPT-Account-ID": "acct-123"}, CodexSignInIdentity{AccountID: "acct-123"}, func(ctx context.Context) (CodexAuth, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		once.Do(func() { close(resolveStarted) })
		<-ctx.Done()
		return CodexAuth{}, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.ChatCompletion(ctx, wsAuthRequest())
		done <- err
	}()

	select {
	case <-resolveStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the resolver to run")
	}
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the cancelled request")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a wrapped context.Canceled", err)
	}
	if !isContextCanceledErr(err) {
		t.Fatalf("error = %v, want it classified as a cancellation", err)
	}
	var authErr *wsAuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error = %v, want a *wsAuthError", err)
	}
	mu.Lock()
	resolved := calls
	mu.Unlock()
	if resolved != 1 {
		t.Errorf("resolver calls = %d, want 1 (no dial retry)", resolved)
	}
	if got := server.dialCount(); got != 0 {
		t.Errorf("dial count = %d, want 0", got)
	}
}

// TestCachingCodexWSEvictsOnSignInChange proves a credential failure evicts the
// cached entry, so the next lookup rebuilds and re-resolves the sign-in.
func TestCachingCodexWSEvictsOnSignInChange(t *testing.T) {
	server := newWSDialRecorder(t)
	p := newDialAuthWSProvider(t, server.wsURL(), map[string]string{"ChatGPT-Account-ID": "acct-123"}, CodexSignInIdentity{AccountID: "acct-123"}, func(context.Context) (CodexAuth, error) {
		return CodexAuth{AccessToken: "token-other-account", AccountID: "acct-999"}, nil
	})
	cache := NewCodexWSCache()
	t.Cleanup(func() { _ = cache.Close() })
	cached, err := NewCachingCodexWS(cache, "gen-1", "key", func() (Provider, error) { return p, nil })
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cached.ChatCompletion(ctx, wsAuthRequest()); err == nil {
		t.Fatal("ChatCompletion succeeded, want the changed sign-in to fail the dial")
	}

	cache.mu.Lock()
	_, retained := cache.instances["key"]
	cache.mu.Unlock()
	if retained {
		t.Error("credential failure kept the cached entry, want it evicted for a rebuild")
	}
	if got := server.dialCount(); got != 0 {
		t.Errorf("dial count = %d, want 0", got)
	}
}

// TestCachingCodexWSKeepsEntryOnDialCancellation proves a cancellation during
// credential resolution does not evict the cached entry.
func TestCachingCodexWSKeepsEntryOnDialCancellation(t *testing.T) {
	server := newWSDialRecorder(t)
	resolveStarted := make(chan struct{})
	var once sync.Once
	p := newDialAuthWSProvider(t, server.wsURL(), map[string]string{"ChatGPT-Account-ID": "acct-123"}, CodexSignInIdentity{AccountID: "acct-123"}, func(ctx context.Context) (CodexAuth, error) {
		once.Do(func() { close(resolveStarted) })
		<-ctx.Done()
		return CodexAuth{}, ctx.Err()
	})
	cache := NewCodexWSCache()
	t.Cleanup(func() { _ = cache.Close() })
	cached, err := NewCachingCodexWS(cache, "gen-1", "key", func() (Provider, error) { return p, nil })
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cached.ChatCompletion(ctx, wsAuthRequest())
		done <- err
	}()

	select {
	case <-resolveStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the resolver to run")
	}
	cancel()

	var callErr error
	select {
	case callErr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the cancelled request")
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("error = %v, want a wrapped context.Canceled", callErr)
	}

	cache.mu.Lock()
	_, retained := cache.instances["key"]
	cache.mu.Unlock()
	if !retained {
		t.Error("cancellation evicted the cached entry, want it kept")
	}
}
