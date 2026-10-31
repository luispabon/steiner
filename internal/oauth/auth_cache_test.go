package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// testTokenConf returns a client config whose token endpoint is tokenURL.
func testTokenConf(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: "test_client",
		Endpoint: oauth2.Endpoint{TokenURL: tokenURL},
	}
}

// saveValidTestToken writes a token that stays outside the proactive refresh
// window for the rest of the test.
func saveValidTestToken(t *testing.T, store *TokenStore, access string) {
	t.Helper()
	saveTestToken(t, store, access, time.Now().Add(time.Hour))
}

// saveRefreshDueTestToken writes a token inside the proactive refresh window, so
// resolving it triggers a refresh.
func saveRefreshDueTestToken(t *testing.T, store *TokenStore) {
	t.Helper()
	saveTestToken(t, store, "old-token", time.Now().Add(time.Minute))
}

func saveTestToken(t *testing.T, store *TokenStore, access string, expiry time.Time) {
	t.Helper()
	token := &oauth2.Token{
		AccessToken:  access,
		RefreshToken: "refresh_token",
		TokenType:    "Bearer",
		Expiry:       expiry,
	}
	if err := store.Save(token); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// refreshServer returns a token endpoint that counts requests and answers with
// "refreshed-token" after delay.
func refreshServer(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"refreshed-token","refresh_token":"refresh_token","expires_in":3600}`)
	}))
	t.Cleanup(server.Close)
	return server, count
}

func TestAuthResolverCachedTokenSkipsReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveValidTestToken(t, store, "cached-token")

	server, refreshes := refreshServer(t, 0)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	first, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if first.AccessToken != "cached-token" {
		t.Fatalf("AccessToken = %q, want cached-token", first.AccessToken)
	}

	// Rewrite the file with a same-length token and restore its modification
	// time, so identity, size, and mtime all match the cached stamp. The cache
	// must serve its copy instead of reloading.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	replaced := strings.Replace(string(original), "cached-token", "stale!-token", 1)
	if len(replaced) != len(original) {
		t.Fatalf("replacement length = %d, want %d", len(replaced), len(original))
	}
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	second, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if second.AccessToken != "cached-token" {
		t.Errorf("AccessToken = %q, want cached-token from the cache", second.AccessToken)
	}
	if got := refreshes.Load(); got != 0 {
		t.Errorf("refresh requests = %d, want 0", got)
	}
}

func TestAuthResolverReloadsExternalChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveValidTestToken(t, store, "first-token")

	server, refreshes := refreshServer(t, 0)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// Another writer replaces the file atomically, as a login or a refresh in
	// another process does.
	saveValidTestToken(t, NewTokenStore(path), "second-token")

	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if token.AccessToken != "second-token" {
		t.Errorf("AccessToken = %q, want second-token", token.AccessToken)
	}
	if got := refreshes.Load(); got != 0 {
		t.Errorf("refresh requests = %d, want 0", got)
	}
}

func TestAuthResolverInvalidatesOnDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveValidTestToken(t, store, "first-token")

	server, _ := refreshServer(t, 0)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Resolve() error = %v, want ErrNoToken", err)
	}

	saveValidTestToken(t, store, "restored-token")
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if token.AccessToken != "restored-token" {
		t.Errorf("AccessToken = %q, want restored-token", token.AccessToken)
	}
}

func TestAuthResolverSingleFlightRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 100*time.Millisecond)
	resolver := NewAuthResolver()
	resolver.lockAcquire = func(context.Context, string) (func(), error) { return func() {}, nil }
	conf := testTokenConf(server.URL)

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	tokens := make([]*oauth2.Token, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = resolver.Resolve(context.Background(), store, conf, nil)
		}(i)
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Errorf("Resolve() error = %v", errs[i])
			continue
		}
		if tokens[i].AccessToken != "refreshed-token" {
			t.Errorf("AccessToken = %q, want refreshed-token", tokens[i].AccessToken)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverCallerCancellationDoesNotCancelRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 200*time.Millisecond)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := resolver.Resolve(ctx, store, conf, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve() error = %v, want context.DeadlineExceeded", err)
	}

	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after cancellation error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSharedRefreshDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	// The handler outlives the client request so the test can prove the resolver
	// gives up at its own deadline; cleanup releases it before the server closes.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	resolver := NewAuthResolver()
	resolver.refreshTimeout = 100 * time.Millisecond

	start := time.Now()
	if _, err := resolver.Resolve(context.Background(), store, testTokenConf(server.URL), nil); err == nil {
		t.Fatal("Resolve() error = nil, want the shared refresh deadline to fail it")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Resolve() took %v, want it bounded by the shared deadline", elapsed)
	}
}

func TestAuthResolverTwoResolversSerializeRefresh(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 100*time.Millisecond)
	conf := testTokenConf(server.URL)
	resolvers := []*AuthResolver{NewAuthResolver(), NewAuthResolver()}

	start := make(chan struct{})
	errs := make([]error, len(resolvers))
	tokens := make([]*oauth2.Token, len(resolvers))
	var wg sync.WaitGroup
	for i, resolver := range resolvers {
		wg.Add(1)
		go func(i int, resolver *AuthResolver) {
			defer wg.Done()
			<-start
			tokens[i], errs[i] = resolver.Resolve(context.Background(), store, conf, nil)
		}(i, resolver)
	}
	close(start)
	wg.Wait()

	for i := range resolvers {
		if errs[i] != nil {
			t.Errorf("Resolve() error = %v", errs[i])
			continue
		}
		if tokens[i].AccessToken != "refreshed-token" {
			t.Errorf("AccessToken = %q, want refreshed-token", tokens[i].AccessToken)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAcquireTokenLockHonoursContext(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	path := filepath.Join(t.TempDir(), "token.json")
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireTokenLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquireTokenLock() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestAcquireTokenLockFileMode(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	path := filepath.Join(t.TempDir(), "token.json")
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	defer release()

	info, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("lock file mode = %o, want 0o600", mode)
	}
}
