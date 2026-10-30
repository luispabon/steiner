package oauth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// scriptedTokenFile is a tokenFile with scripted results, so tests can change
// the file between the reads of one stable load.
type scriptedTokenFile struct {
	statFn func() (fileStamp, error)
	loadFn func() (*oauth2.Token, error)
}

func (f *scriptedTokenFile) stat() (fileStamp, error) { return f.statFn() }

func (f *scriptedTokenFile) load() (*oauth2.Token, error) { return f.loadFn() }

// flakyStatFile wraps a tokenFile and returns queued errors from stat before
// delegating, so tests can inject a transient stat failure.
type flakyStatFile struct {
	inner tokenFile
	mu    sync.Mutex
	errs  []error
}

func (f *flakyStatFile) stat() (fileStamp, error) {
	f.mu.Lock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		f.mu.Unlock()
		return fileStamp{}, err
	}
	f.mu.Unlock()
	return f.inner.stat()
}

func (f *flakyStatFile) load() (*oauth2.Token, error) { return f.inner.load() }

func (f *flakyStatFile) failNextStat(err error) {
	f.mu.Lock()
	f.errs = append(f.errs, err)
	f.mu.Unlock()
}

// scriptedResolver returns a resolver that reads store through file.
func scriptedResolver(file tokenFile) *AuthResolver {
	resolver := NewAuthResolver()
	resolver.newTokenFile = func(*TokenStore) tokenFile { return file }
	return resolver
}

func TestAuthResolverStableReadRetriesConcurrentReplacement(t *testing.T) {
	stampOld := fileStamp{modTime: time.Unix(100, 0), size: 10, inode: 1, exists: true}
	stampNew := fileStamp{modTime: time.Unix(200, 0), size: 20, inode: 2, exists: true}
	stale := &oauth2.Token{AccessToken: "stale-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}
	fresh := &oauth2.Token{AccessToken: "fresh-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}

	var stats, loads atomic.Int32
	file := &scriptedTokenFile{
		statFn: func() (fileStamp, error) {
			// The replacement lands between the first stat of the stable read and
			// the stat after the load. Call 1 is the fast-path stat.
			if stats.Add(1) <= 2 {
				return stampOld, nil
			}
			return stampNew, nil
		},
		loadFn: func() (*oauth2.Token, error) {
			if loads.Add(1) == 1 {
				return stale, nil
			}
			return fresh, nil
		},
	}

	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	server, refreshes := refreshServer(t, 0)
	resolver := scriptedResolver(file)
	conf := testTokenConf(server.URL)

	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if token.AccessToken != "fresh-token" {
		t.Fatalf("AccessToken = %q, want fresh-token: the read must restart when the file changes mid-read", token.AccessToken)
	}

	// The cache must pair fresh-token with the new stamp. A second call at that
	// stamp is served from memory, so it can never return the stale token.
	loadsBefore := loads.Load()
	token, err = resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() second call error = %v", err)
	}
	if token.AccessToken != "fresh-token" {
		t.Errorf("AccessToken = %q, want fresh-token from the cache", token.AccessToken)
	}
	if got := loads.Load(); got != loadsBefore {
		t.Errorf("loads = %d, want %d: the second call should hit the cache", got, loadsBefore)
	}
	if got := refreshes.Load(); got != 0 {
		t.Errorf("refresh requests = %d, want 0", got)
	}
}

func TestAuthResolverAdoptsTokenWrittenAfterSave(t *testing.T) {
	stampBefore := fileStamp{modTime: time.Unix(100, 0), size: 10, inode: 1, exists: true}
	stampAfter := fileStamp{modTime: time.Unix(200, 0), size: 20, inode: 2, exists: true}
	expired := &oauth2.Token{AccessToken: "old-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Minute)}
	winner := &oauth2.Token{AccessToken: "winner-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}

	var stats, loads atomic.Int32
	file := &scriptedTokenFile{
		statFn: func() (fileStamp, error) {
			// The first four stats bracket the read and the pre-save check. From the
			// post-save re-read on, another writer's version is present.
			if stats.Add(1) <= 4 {
				return stampBefore, nil
			}
			return stampAfter, nil
		},
		loadFn: func() (*oauth2.Token, error) {
			if loads.Add(1) == 1 {
				return expired, nil
			}
			return winner, nil
		},
	}

	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	server, refreshes := refreshServer(t, 0)
	resolver := scriptedResolver(file)
	conf := testTokenConf(server.URL)

	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if token.AccessToken != "winner-token" {
		t.Fatalf("AccessToken = %q, want winner-token: the token written after the save must be adopted", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}

	loadsBefore := loads.Load()
	token, err = resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() second call error = %v", err)
	}
	if token.AccessToken != "winner-token" {
		t.Errorf("AccessToken = %q, want winner-token from the cache", token.AccessToken)
	}
	if got := loads.Load(); got != loadsBefore {
		t.Errorf("loads = %d, want %d: the adopted token should be cached", got, loadsBefore)
	}
}

func TestAuthResolverUnstableTokenFile(t *testing.T) {
	var stats atomic.Int32
	file := &scriptedTokenFile{
		statFn: func() (fileStamp, error) {
			n := stats.Add(1)
			return fileStamp{modTime: time.Unix(int64(n), 0), size: 1, inode: uint64(n), exists: true}, nil
		},
		loadFn: func() (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
		},
	}

	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	server, _ := refreshServer(t, 0)

	_, err := scriptedResolver(file).Resolve(context.Background(), store, testTokenConf(server.URL), nil)
	if !errors.Is(err, errTokenFileUnstable) {
		t.Fatalf("Resolve() error = %v, want errTokenFileUnstable", err)
	}
}

func TestAuthResolverZeroExpiryTokenIsValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveTestToken(t, store, "no-expiry-token", time.Time{})

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	server, refreshes := refreshServer(t, 0)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	for i := 0; i < 2; i++ {
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if token.AccessToken != "no-expiry-token" {
			t.Errorf("AccessToken = %q, want no-expiry-token", token.AccessToken)
		}
	}
	if got := refreshes.Load(); got != 0 {
		t.Errorf("refresh requests = %d, want 0 for a token without expiry", got)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Error("token file was rewritten for a token without expiry")
	}
}

func TestAuthResolverDoesNotRecreateDeletedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The user removes the credential while the refresh is in flight.
		if err := os.Remove(path); err != nil {
			t.Errorf("Remove() error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"refreshed-token","refresh_token":"refresh_token","expires_in":3600}`)
	}))
	t.Cleanup(server.Close)

	resolver := NewAuthResolver()
	_, err := resolver.Resolve(context.Background(), store, testTokenConf(server.URL), nil)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("Resolve() error = %v, want ErrNoToken", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat() error = %v, want the token file to stay deleted", err)
	}
}

func TestAuthResolverKeepsRefreshedTokenWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	// Create the lock file while the directory is still writable, then make the
	// directory unwritable so the atomic save fails after the refresh token was
	// already rotated server-side.
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	release()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err == nil {
		t.Fatal("Resolve() error = nil, want the save failure")
	}

	// The rotated token stays usable in this process without a second refresh,
	// and the failed save is not reported as persisted.
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after failed save error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token from memory", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "old-token" {
		t.Errorf("persisted AccessToken = %q, want old-token: the failed save must not be reported as persisted", stored.AccessToken)
	}
}

// rotatingRefreshServer returns a token endpoint that issues a new refresh token
// on every request, answers with a short-lived access token ("access-N"), and
// rejects any refresh token it did not issue. The stored token written by
// saveTestToken starts the chain with "refresh_token".
func rotatingRefreshServer(t *testing.T, lifetime time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	expected := "refresh_token"
	issued := 0
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		want := expected
		mu.Unlock()
		if r.Form.Get("refresh_token") != want {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		mu.Lock()
		issued++
		n := issued
		expected = fmt.Sprintf("refresh-%d", n)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","refresh_token":"refresh-%d","token_type":"Bearer","expires_in":%d}`, n, n, int(lifetime.Seconds()))
	}))
	t.Cleanup(server.Close)
	return server, count
}

func TestAuthResolverShortLivedTokenRefreshesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := rotatingRefreshServer(t, time.Minute)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	for want := 1; want <= 2; want++ {
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		if err != nil {
			t.Fatalf("Resolve() round %d error = %v", want, err)
		}
		if got := token.AccessToken; got != fmt.Sprintf("access-%d", want) {
			t.Fatalf("AccessToken = %q, want access-%d", got, want)
		}
		if got := refreshes.Load(); got != int32(want) {
			t.Fatalf("refresh requests = %d, want %d: a short-lived token must cost one refresh per call", got, want)
		}
	}
}

func TestAuthResolverUsesRetainedTokenAfterFailedSave(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := rotatingRefreshServer(t, time.Minute)
	resolver := NewAuthResolver()
	conf := testTokenConf(server.URL)

	// Create the lock file while the directory is still writable, then make the
	// directory unwritable so the atomic save fails after the refresh token was
	// rotated server-side.
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	release()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err == nil {
		t.Fatal("Resolve() error = nil, want the save failure")
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}

	// The retained token is inside the proactive window, so the next resolve must
	// start from it, present its rotated refresh token, and retry the save now
	// that the directory is writable.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after failed save error = %v", err)
	}
	if token.AccessToken != "access-2" {
		t.Errorf("AccessToken = %q, want access-2", token.AccessToken)
	}
	if got := refreshes.Load(); got != 2 {
		t.Errorf("refresh requests = %d, want 2", got)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.AccessToken != "access-2" {
		t.Errorf("persisted AccessToken = %q, want access-2: the retried save must land", reloaded.AccessToken)
	}
}

func TestAuthResolverStableReadMissingFileIsNoToken(t *testing.T) {
	stamp := fileStamp{modTime: time.Unix(100, 0), size: 10, inode: 1, exists: true}
	var stats atomic.Int32
	file := &scriptedTokenFile{
		statFn: func() (fileStamp, error) {
			if stats.Add(1) == 1 {
				return stamp, nil
			}
			return fileStamp{}, &fs.PathError{Op: "stat", Path: "token.json", Err: fs.ErrNotExist}
		},
		loadFn: func() (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, nil
		},
	}

	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	server, refreshes := refreshServer(t, 0)

	_, err := scriptedResolver(file).Resolve(context.Background(), store, testTokenConf(server.URL), nil)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("Resolve() error = %v, want ErrNoToken for a file that disappears mid-read", err)
	}
	if got := refreshes.Load(); got != 0 {
		t.Errorf("refresh requests = %d, want 0", got)
	}
}

func TestAuthResolverTransientStatErrorKeepsRetainedToken(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := rotatingRefreshServer(t, time.Minute)
	flaky := &flakyStatFile{inner: storeTokenFile{store: store}}
	resolver := NewAuthResolver()
	resolver.newTokenFile = func(*TokenStore) tokenFile { return flaky }
	conf := testTokenConf(server.URL)

	// Create the lock file while the directory is still writable, then make the
	// directory unwritable so the atomic save fails after the refresh token was
	// rotated server-side.
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	release()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err == nil {
		t.Fatal("Resolve() error = nil, want the save failure")
	}

	// A transient stat failure must fail that call without dropping the token that
	// could not be persisted: its rotated refresh token is the only usable one.
	flaky.failNextStat(&fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission})
	if _, err := resolver.Resolve(context.Background(), store, conf, nil); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Resolve() error = %v, want the transient stat error", err)
	}

	// The retained token survived, so the next call presents its rotated refresh
	// token and the retried save lands.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after transient stat error = %v", err)
	}
	if token.AccessToken != "access-2" {
		t.Errorf("AccessToken = %q, want access-2", token.AccessToken)
	}
	if got := refreshes.Load(); got != 2 {
		t.Errorf("refresh requests = %d, want 2", got)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.AccessToken != "access-2" {
		t.Errorf("persisted AccessToken = %q, want access-2", reloaded.AccessToken)
	}
}
