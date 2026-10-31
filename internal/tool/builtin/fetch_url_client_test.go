package builtin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/wonton/httpguard"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func newFetchURLTestEnv(t *testing.T, httpClient func() *http.Client) Env {
	t.Helper()
	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	return Env{WorkDir: workDir, PathPolicy: &policy, httpClient: httpClient}
}

// fetchURLTestSafeClient returns the default guarded client with its per-dial
// address check removed, so tests can run concurrency and cookie cases against
// loopback httptest servers. Use it only where the dial guard is not the
// subject; the guard itself is covered by
// TestFetchURLDefaultClientRefusesNonPublicAddresses.
func fetchURLTestSafeClient() *http.Client {
	client := newHTTPGuardClient(15 * time.Second)
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

// fetchURLTestLoopbackGuardedClient returns a client built like fetch_url's
// default (same redirect bounds, guarded dial) with a single loopback exception
// for originURL's host, httpguard's documented escape hatch for reaching one
// private host. Every other address, redirect targets included, stays guarded.
func fetchURLTestLoopbackGuardedClient(t *testing.T, originURL string) *http.Client {
	t.Helper()
	origin, err := url.Parse(originURL)
	if err != nil {
		t.Fatalf("parse origin URL: %v", err)
	}
	originIP := net.ParseIP(origin.Hostname())
	if originIP == nil {
		t.Fatalf("origin host %q is not an IP literal", origin.Hostname())
	}
	client := httpguard.NewClient(
		httpguard.WithMaxRedirects(fetchRedirectLimit),
		httpguard.WithHTTPRedirects(),
		httpguard.WithAddressValidator(func(ip net.IP) error {
			if ip.Equal(originIP) {
				return nil
			}
			return httpguard.ValidatePublicIP(ip)
		}),
	)
	client.Timeout = 15 * time.Second
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

func TestFetchURLDefaultClientRedirectPolicy(t *testing.T) {
	client := newFetchURLClient(Env{})
	if client.Jar != nil {
		t.Errorf("default client Jar = %v, want nil", client.Jar)
	}
	if client.Timeout != 15*time.Second {
		t.Errorf("default client Timeout = %v, want 15s", client.Timeout)
	}
	if client.CheckRedirect == nil {
		t.Fatal("default client CheckRedirect = nil, want bounded redirects")
	}

	newReq := func(t *testing.T, target string) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("build request %s: %v", target, err)
		}
		return req
	}
	chain := func(t *testing.T, hops int) []*http.Request {
		t.Helper()
		via := make([]*http.Request, 0, hops)
		for i := range hops {
			via = append(via, newReq(t, fmt.Sprintf("https://example.com/%d", i)))
		}
		return via
	}

	tests := []struct {
		name    string
		target  string
		hops    int
		blocked bool
	}{
		// Literal hop counts rather than fetchRedirectLimit arithmetic: nine
		// followed and the tenth refused is the contract being pinned.
		{name: "ninth hop within the limit", target: "https://example.com/next", hops: 9},
		{name: "plain http hop is allowed", target: "http://example.com/next", hops: 1},
		{name: "tenth hop is refused", target: "https://example.com/next", hops: 10, blocked: true},
		{name: "non-http scheme", target: "ftp://example.com/next", hops: 1, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.CheckRedirect(newReq(t, tt.target), chain(t, tt.hops))
			if tt.blocked && err == nil {
				t.Errorf("CheckRedirect(%s) after %d hops = nil, want refusal", tt.target, tt.hops)
			}
			if !tt.blocked && err != nil {
				t.Errorf("CheckRedirect(%s) after %d hops = %v, want nil", tt.target, tt.hops, err)
			}
		})
	}
}

// TestFetchURLDefaultClientRefusesNonPublicAddresses pins the guarded dial
// against address space the standard library's IP predicates alone would miss
// (CGNAT shared space and IPv6 unique-local) plus loopback and the link-local
// metadata address. Literal addresses are refused before a connection is
// attempted, so no network access is needed.
func TestFetchURLDefaultClientRefusesNonPublicAddresses(t *testing.T) {
	client := newFetchURLClient(Env{})
	tests := []struct {
		name   string
		target string
	}{
		{name: "loopback", target: "http://127.0.0.1/portal"},
		{name: "CGNAT shared space", target: "http://100.64.12.7/"},
		{name: "IPv6 unique local", target: "http://[fd00::1]/"},
		{name: "link-local metadata", target: "http://169.254.169.254/latest/meta-data/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tt.target, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			if !errors.Is(err, httpguard.ErrNonPublicAddress) {
				t.Errorf("client.Do(%s) = %v, want httpguard.ErrNonPublicAddress", tt.target, err)
			}
		})
	}
}

// TestFetchURLDefaultClientIgnoresAmbientProxy pins the guarded transport to a
// nil proxy function, which is what keeps HTTP(S)_PROXY from relaying fetch_url
// requests. It asserts the transport field directly instead of routing a
// request through a live proxy: Go caches proxy environment lookups after the
// first use, and the dial guard refuses a loopback proxy, so a hit counter
// could not tell proxy use from a blocked dial. The tradeoff is documented in
// docs/user/tools.md.
func TestFetchURLDefaultClientIgnoresAmbientProxy(t *testing.T) {
	client := newFetchURLClient(Env{})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default client transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("default client transport Proxy is set, want nil: ambient proxies must not relay fetch_url requests")
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
		if !strings.Contains(fetchErr.Error, httpguard.ErrNonPublicAddress.Error()) {
			t.Errorf("call %d: error = %q, want an SSRF rejection (%q)", i, fetchErr.Error, httpguard.ErrNonPublicAddress.Error())
		}
	}

	if got := requests.Load(); got != 0 {
		t.Errorf("loopback server requests = %d, want 0: the dial was not blocked before connecting", got)
	}
}

func TestFetchURLGuardedClientBlocksSSRFRedirect(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.1.2.3/secret", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	// The origin is a loopback server, so the guarded client admits exactly
	// that one address. The redirect target is a private address and must
	// still be refused when the client dials it, so a redirect hop goes
	// through the same address check as the first request.
	toolDef := NewFetchURLTool(newFetchURLTestEnv(t, func() *http.Client {
		return fetchURLTestLoopbackGuardedClient(t, origin.URL)
	}))

	res, err := toolDef.Handler(context.Background(), map[string]any{"url": origin.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := res.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want the SSRF redirect rejection as *FetchURLError", res)
	}
	if !strings.Contains(fetchErr.Error, httpguard.ErrNonPublicAddress.Error()) {
		t.Errorf("error = %q, want the redirect target refused with %q", fetchErr.Error, httpguard.ErrNonPublicAddress.Error())
	}
}
