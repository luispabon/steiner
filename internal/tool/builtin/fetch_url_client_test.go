package builtin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func newFetchURLTestEnv(t *testing.T, httpClient func() *http.Client) Env {
	t.Helper()
	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	return Env{WorkDir: workDir, PathPolicy: &policy, httpClient: httpClient}
}

// fetchURLTestSafeClient returns the default SSRF-safe client with its per-dial
// address guard removed, so tests can run redirect and concurrency cases
// against loopback httptest servers while keeping the client's SSRF redirect
// validation intact.
func fetchURLTestSafeClient() *http.Client {
	client := toolkit.SafeHTTPClient(15 * time.Second)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		// No *testing.T here; the factory is passed as a plain func value.
		panic(fmt.Sprintf("fetchURLTestSafeClient: default client transport = %T, want *http.Transport", client.Transport))
	}
	dialTransport := transport.Clone()
	dialTransport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	client.Transport = dialTransport
	return client
}

func TestFetchURLToolReusesClientAcrossCalls(t *testing.T) {
	var newConns atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text body"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	var factoryCalls atomic.Int32
	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, func() *http.Client {
		factoryCalls.Add(1)
		// Proxy is left nil so an HTTP(S)_PROXY in the test environment
		// cannot intercept the loopback requests.
		return &http.Client{Transport: &http.Transport{}}
	}))

	for i := 1; i <= 2; i++ {
		res, err := toolDef.Handler(context.Background(), map[string]any{"url": server.URL})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("call %d: result type = %T, want *FetchURLResult", i, res)
		}
		if result.Content != "plain text body" {
			t.Errorf("call %d: Content = %q, want %q", i, result.Content, "plain text body")
		}
	}

	if got := factoryCalls.Load(); got != 1 {
		t.Errorf("httpClient factory calls = %d, want 1 for one tool instance", got)
	}
	if got := newConns.Load(); got != 1 {
		t.Errorf("new connections = %d, want 1 (connections not reused across calls)", got)
	}
}

func TestFetchURLToolBuildsDefaultClientOncePerInstance(t *testing.T) {
	var newConns atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text body"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	// Swap the package-level default constructor to count constructions. It
	// returns the test safe client (unguarded dialer) because the production
	// default refuses to dial a loopback server. No t.Parallel: this test
	// mutates a package variable other tests read.
	original := newSafeFetchClient
	t.Cleanup(func() { newSafeFetchClient = original })
	var builds atomic.Int32
	var timeout time.Duration
	newSafeFetchClient = func(d time.Duration) *http.Client {
		builds.Add(1)
		timeout = d
		return fetchURLTestSafeClient()
	}

	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, nil))

	for i := 1; i <= 3; i++ {
		res, err := toolDef.Handler(context.Background(), map[string]any{"url": server.URL})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("call %d: result type = %T, want *FetchURLResult", i, res)
		}
		if result.Content != "plain text body" {
			t.Errorf("call %d: Content = %q, want %q", i, result.Content, "plain text body")
		}
	}

	if got := builds.Load(); got != 1 {
		t.Errorf("default client constructions = %d, want 1 for one tool instance", got)
	}
	if timeout != 15*time.Second {
		t.Errorf("default client timeout = %v, want 15s", timeout)
	}
	if got := newConns.Load(); got != 1 {
		t.Errorf("new connections = %d, want 1 (connections not reused across calls)", got)
	}
}

func TestFetchURLToolDoesNotShareCookiesAcrossCalls(t *testing.T) {
	var requestsWithCookie atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			requestsWithCookie.Add(1)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret", Path: "/"})
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text body"))
	}))
	t.Cleanup(server.Close)

	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, func() *http.Client {
		// A seam-supplied client carrying a jar must not leak its cookies
		// from one fetch_url call into the next.
		client := *server.Client()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		client.Jar = jar
		return &client
	}))

	for i := 1; i <= 2; i++ {
		if _, err := toolDef.Handler(context.Background(), map[string]any{"url": server.URL}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if got := requestsWithCookie.Load(); got != 0 {
		t.Errorf("requests carrying a cookie = %d, want 0", got)
	}
}

func TestFetchURLToolConcurrentCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret", Path: "/"})
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text body"))
	}))
	t.Cleanup(server.Close)

	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, func() *http.Client {
		return server.Client()
	}))

	const calls = 8
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := toolDef.Handler(context.Background(), map[string]any{"url": server.URL})
			if err != nil {
				errs <- err
				return
			}
			result, ok := res.(*FetchURLResult)
			if !ok {
				errs <- fmt.Errorf("result type = %T, want *FetchURLResult", res)
				return
			}
			if result.Content != "plain text body" {
				errs <- fmt.Errorf("Content = %q, want %q", result.Content, "plain text body")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestFetchURLDefaultClientChecksRedirects(t *testing.T) {
	client := newFetchURLClient(Env{})
	if client.Jar != nil {
		t.Errorf("default client Jar = %v, want nil", client.Jar)
	}
	if client.Timeout != 15*time.Second {
		t.Errorf("default client Timeout = %v, want 15s", client.Timeout)
	}

	tests := []struct {
		name    string
		target  string
		blocked bool
	}{
		{name: "loopback", target: "http://127.0.0.1/portal", blocked: true},
		{name: "private range", target: "http://10.1.2.3/admin", blocked: true},
		{name: "link-local metadata", target: "http://169.254.169.254/latest/meta-data/", blocked: true},
		{name: "file scheme", target: "file:///etc/passwd", blocked: true},
		{name: "public literal IP", target: "http://93.184.216.34/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tt.target, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			err = client.CheckRedirect(req, []*http.Request{req})
			if tt.blocked && err == nil {
				t.Errorf("CheckRedirect(%s) = nil, want SSRF rejection", tt.target)
			}
			if !tt.blocked && err != nil {
				t.Errorf("CheckRedirect(%s) = %v, want nil", tt.target, err)
			}
		})
	}
}

func TestFetchURLDefaultClientRejectsLoopbackOnEveryCall(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("internal secret"))
	}))
	t.Cleanup(server.Close)

	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, nil))

	for i := 1; i <= 2; i++ {
		res, err := toolDef.Handler(context.Background(), map[string]any{"url": server.URL})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("call %d: result type = %T, want the SSRF rejection as *FetchURLError", i, res)
		}
		if !strings.Contains(fetchErr.Error, "not allowed") {
			t.Errorf("call %d: error = %q, want an SSRF rejection (\"not allowed\")", i, fetchErr.Error)
		}
	}

	if got := requests.Load(); got != 0 {
		t.Errorf("loopback server requests = %d, want 0: the dial was not blocked before connecting", got)
	}
}

func TestFetchURLSafeClientBlocksSSRFRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("internal secret"))
	}))
	t.Cleanup(target.Close)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/secret", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	// The origin is a loopback server, so the seam client's per-dial guard is
	// swapped for an unguarded dialer to let the call reach it. The redirect
	// validation under test is the one fetchURLTestSafeClient keeps from
	// toolkit.SafeHTTPClient, exercised through fetch_url's env.httpClient
	// seam. The production default's redirect checks are covered by
	// TestFetchURLDefaultClientChecksRedirects.
	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, fetchURLTestSafeClient))

	res, err := toolDef.Handler(context.Background(), map[string]any{"url": origin.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := res.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want the SSRF redirect rejection as *FetchURLError", res)
	}
	if !strings.Contains(fetchErr.Error, "not allowed") {
		t.Errorf("error = %q, want an SSRF rejection (\"not allowed\")", fetchErr.Error)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Errorf("redirect target requests = %d, want 0: the redirect was not blocked", got)
	}
}
