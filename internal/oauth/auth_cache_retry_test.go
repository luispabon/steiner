package oauth

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// testClock is a manually advanced clock for save-retry tests.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock { return &testClock{now: time.Now()} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// lockTokenFile creates the token lock file, then makes dir read-only so the
// save of a rotated token fails while the lock can still be taken.
func lockTokenFile(t *testing.T, dir, path string) {
	t.Helper()
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	release()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

// unlockTokenFile makes dir writable again.
func unlockTokenFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
}

// failInitialSave resolves a refresh-due token while dir is read-only, so the
// resolver retains a rotated token whose save failed.
func failInitialSave(t *testing.T, resolver *AuthResolver, store *TokenStore, conf *oauth2.Config, dir, path string) {
	t.Helper()
	lockTokenFile(t, dir, path)
	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err == nil {
		t.Fatal("Resolve() error = nil, want the save failure")
	}
}

// gateRetryLock gates the first flight's token lock acquisition, so a test can
// change the token file, cancel a caller, or let concurrent callers join before
// the save retry proceeds. open releases the gate, after which flights take the
// real lock.
func gateRetryLock(resolver *AuthResolver) (started <-chan struct{}, acquisitions *atomic.Int32, open func()) {
	startedCh := make(chan struct{})
	proceed := make(chan struct{})
	count := &atomic.Int32{}
	var gate sync.Once
	var openOnce sync.Once
	resolver.lockAcquire = func(ctx context.Context, path string) (func(), error) {
		count.Add(1)
		gate.Do(func() {
			close(startedCh)
			<-proceed
		})
		return acquireTokenLock(ctx, path)
	}
	return startedCh, count, func() { openOnce.Do(func() { close(proceed) }) }
}

// observedFile wraps a tokenFile and reports its first stat, so a test knows a
// caller has seen the current file version. Each Resolve call gets its own
// instance, so the first stat is the caller's top-level one.
type observedFile struct {
	inner tokenFile
	once  sync.Once
	seen  func()
}

func (f *observedFile) stat() (fileStamp, error) {
	f.once.Do(f.seen)
	return f.inner.stat()
}

func (f *observedFile) load() (*oauth2.Token, error) { return f.inner.load() }

func TestAuthResolverRetriesRetainedTokenSave(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)

	// Inside the pause between retries the retained token is served without
	// attempting the write, so the still read-only directory cannot affect the
	// call.
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() inside the retry pause error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Fatalf("AccessToken = %q, want refreshed-token from memory", token.AccessToken)
	}

	unlockTokenFile(t, dir)
	clock.Advance(saveRetryBaseDelay)

	token, err = resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() retry error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1: the retry must save without refreshing", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-token", stored.AccessToken)
	}

	// The saved version is cached: the next call neither reloads nor refreshes.
	token, err = resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after retry error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token from the cache", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryServesTokenWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	// The directory stays read-only, so the due retry fails to write.
	failInitialSave(t, resolver, store, conf, dir, path)
	clock.Advance(saveRetryBaseDelay)

	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() retry error = %v, want the retained token served", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "old-token" {
		t.Errorf("persisted AccessToken = %q, want old-token after the failed retry", stored.AccessToken)
	}

	// A writable directory and the next pause let the same token through.
	unlockTokenFile(t, dir)
	clock.Advance(2 * saveRetryBaseDelay)
	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err != nil {
		t.Fatalf("Resolve() after unlocking error = %v", err)
	}
	stored, err = store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-token from the retry", stored.AccessToken)
	}
}

func TestAuthResolverSaveRetryDoesNotWaitForBusyLock(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	// Another process holds the token lock, as it does for the length of a
	// refresh. The due retry must give up after its short wait and serve the
	// retained token instead of waiting for the refresh deadline.
	unlock, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	var unlockOnce sync.Once
	busy := func() { unlockOnce.Do(unlock) }
	defer busy()

	clock.Advance(saveRetryBaseDelay)
	start := time.Now()
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Resolve() with a busy token lock error = %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token", token.AccessToken)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Resolve() took %v, want a bounded short wait rather than the %v refresh deadline", elapsed, codexRefreshTimeout)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "old-token" {
		t.Errorf("persisted AccessToken = %q, want old-token while the lock was busy", stored.AccessToken)
	}

	// The skipped try did not use an attempt, so the next pause is enough to
	// write the retained token once the lock is free.
	busy()
	clock.Advance(saveRetryBaseDelay)
	if _, err := resolver.Resolve(context.Background(), store, conf, nil); err != nil {
		t.Fatalf("Resolve() after the lock freed error = %v", err)
	}
	stored, err = store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-token", stored.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryWaiterWithNewerStampResolves(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	// Another process holds the token lock, as it does for the length of a
	// refresh, so the save-only retry times out on it.
	unlock, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	var unlockOnce sync.Once
	busy := func() { unlockOnce.Do(unlock) }
	defer busy()

	started, _, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	// Report when the second caller has statted the replaced file version.
	var stats atomic.Int32
	observed := make(chan struct{})
	resolver.newTokenFile = func(store *TokenStore) tokenFile {
		return &observedFile{
			inner: storeTokenFile{store: store},
			seen: func() {
				if stats.Add(1) == 2 {
					close(observed)
				}
			},
		}
	}

	type result struct {
		token *oauth2.Token
		err   error
	}

	// The first caller observes the old version and starts the save-only retry.
	first := make(chan result, 1)
	go func() {
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		first <- result{token: token, err: err}
	}()
	<-started

	// A login writes a new version while the retry waits for the lock, and a
	// second caller observes it and joins the retry in flight.
	saveValidTestToken(t, NewTokenStore(path), "replacement-token")
	second := make(chan result, 1)
	go func() {
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		second <- result{token: token, err: err}
	}()
	<-observed

	open()
	res := <-first
	if res.err != nil {
		t.Fatalf("Resolve() of the old version error = %v", res.err)
	}
	if res.token == nil || res.token.AccessToken != "refreshed-token" {
		t.Errorf("old-version AccessToken = %v, want refreshed-token", res.token)
	}

	busy()
	res = <-second
	if res.err != nil {
		t.Fatalf("Resolve() of the new version error = %v", res.err)
	}
	if res.token == nil || res.token.AccessToken != "replacement-token" {
		t.Fatalf("new-version AccessToken = %v, want replacement-token: a waiter that observed the new version must not accept the old retained token", res.token)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "replacement-token" {
		t.Errorf("persisted AccessToken = %q, want replacement-token", stored.AccessToken)
	}
}

func TestAuthResolverFlightErrorForOtherVersionResolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	// The token endpoint fails the refresh of the old version, after a delay that
	// lets the second caller reach the flight before its error is published.
	requested := make(chan struct{})
	release := make(chan struct{})
	var requestedOnce sync.Once
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		requestedOnce.Do(func() { close(requested) })
		<-release
		time.Sleep(50 * time.Millisecond)
		return nil, errors.New("token endpoint failed")
	})}

	resolver := NewAuthResolver()
	conf := testTokenConf("https://token.example")
	// Pin the auth style so the failed refresh costs one request instead of the
	// two oauth2's auth-style probe would send.
	conf.Endpoint.AuthStyle = oauth2.AuthStyleInParams

	// Report when the second caller has statted the replaced file version.
	var stats atomic.Int32
	observed := make(chan struct{})
	resolver.newTokenFile = func(store *TokenStore) tokenFile {
		return &observedFile{
			inner: storeTokenFile{store: store},
			seen: func() {
				if stats.Add(1) == 2 {
					close(observed)
				}
			},
		}
	}

	type result struct {
		token *oauth2.Token
		err   error
	}

	// The first caller refreshes the old version and waits on the endpoint.
	first := make(chan result, 1)
	go func() {
		token, err := resolver.Resolve(context.Background(), store, conf, client)
		first <- result{token: token, err: err}
	}()
	<-requested

	// A login saves a valid replacement while the refresh is in flight, and a
	// second caller observes it and joins the refresh.
	saveValidTestToken(t, NewTokenStore(path), "replacement-token")
	second := make(chan result, 1)
	go func() {
		token, err := resolver.Resolve(context.Background(), store, conf, client)
		second <- result{token: token, err: err}
	}()
	<-observed
	unblock()

	if res := <-first; res.err == nil {
		t.Fatalf("Resolve() of the old version error = nil, want the refresh failure")
	}
	res := <-second
	if res.err != nil {
		t.Fatalf("Resolve() of the replacement error = %v, want the replacement token: a flight error for another version must not be returned", res.err)
	}
	if res.token == nil || res.token.AccessToken != "replacement-token" {
		t.Errorf("AccessToken = %v, want replacement-token", res.token)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("token endpoint requests = %d, want 1: the replacement needs no refresh", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "replacement-token" {
		t.Errorf("persisted AccessToken = %q, want replacement-token", stored.AccessToken)
	}
}

func TestAuthResolverSaveRetryDoesNotOverwriteReplacement(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	started, _, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	type result struct {
		token *oauth2.Token
		err   error
	}
	done := make(chan result, 1)
	go func() {
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		done <- result{token: token, err: err}
	}()
	<-started

	// Another process signs in while the retry waits for the lock. Its save does
	// not take the lock and must win over the retained token.
	saveValidTestToken(t, NewTokenStore(path), "replacement-token")
	open()

	res := <-done
	if res.err != nil {
		t.Fatalf("Resolve() error = %v", res.err)
	}
	if res.token == nil || res.token.AccessToken != "replacement-token" {
		t.Errorf("AccessToken = %v, want replacement-token", res.token)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "replacement-token" {
		t.Errorf("persisted AccessToken = %q, want replacement-token: the retry must not overwrite it", stored.AccessToken)
	}

	// The replacement is cached, so later calls do not refresh it either.
	token, err := resolver.Resolve(context.Background(), store, conf, nil)
	if err != nil {
		t.Fatalf("Resolve() after replacement error = %v", err)
	}
	if token.AccessToken != "replacement-token" {
		t.Errorf("AccessToken = %q, want replacement-token from the cache", token.AccessToken)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryKeepsDeletedTokenDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	started, _, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	done := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(context.Background(), store, conf, nil)
		done <- err
	}()
	<-started

	// The user removes the credential while the retry waits for the lock.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	open()

	if err := <-done; !errors.Is(err, ErrNoToken) {
		t.Fatalf("Resolve() error = %v, want ErrNoToken", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat() error = %v, want the token file to stay deleted", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryBacksOffAfterLockError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	// A lock that cannot be opened is a real failure, not contention: it must use
	// an attempt and back off instead of deferring forever.
	var attempts atomic.Int32
	resolver.lockAcquire = func(context.Context, string) (func(), error) {
		attempts.Add(1)
		return nil, &fs.PathError{Op: "open", Path: path + ".lock", Err: fs.ErrPermission}
	}

	for i := 0; i < saveRetryAttempts+3; i++ {
		clock.Advance(4 * saveRetryBaseDelay)
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		if err != nil {
			t.Errorf("Resolve() call %d error = %v, want the retained token served", i+1, err)
			continue
		}
		if token == nil || token.AccessToken != "refreshed-token" {
			t.Errorf("call %d AccessToken = %v, want the retained refreshed-token", i+1, token)
		}
	}
	if got := attempts.Load(); got != int32(saveRetryAttempts) {
		t.Errorf("lock attempts = %d, want %d: a real lock error must use an attempt and then stop", got, saveRetryAttempts)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryReportsDeletedDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	started, _, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	done := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(context.Background(), store, conf, nil)
		done <- err
	}()
	<-started

	// The user removes the whole config directory while the retry waits for the
	// lock, so the lock file and the token go together.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	open()

	if err := <-done; !errors.Is(err, ErrNoToken) {
		t.Fatalf("Resolve() error = %v, want ErrNoToken: the token went with the directory", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat() error = %v, want the deleted directory to stay deleted", err)
	}
	if _, err := resolver.Resolve(context.Background(), store, conf, nil); !errors.Is(err, ErrNoToken) {
		t.Errorf("Resolve() after deletion error = %v, want ErrNoToken from the cleared cache", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverSaveRetryIsBounded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	var retryLocks atomic.Int32
	resolver.lockAcquire = func(ctx context.Context, path string) (func(), error) {
		retryLocks.Add(1)
		return acquireTokenLock(ctx, path)
	}

	// The directory stays read-only, so every due retry fails. Each one serves
	// the retained token without reporting the failure and leaves the next
	// attempt to the pause; after saveRetryAttempts failures the cache is served
	// without further writes.
	for i := 0; i < saveRetryAttempts+3; i++ {
		clock.Advance(4 * saveRetryBaseDelay)
		token, err := resolver.Resolve(context.Background(), store, conf, nil)
		if err != nil {
			t.Errorf("Resolve() call %d error = %v, want the retained token served", i+1, err)
			continue
		}
		if token == nil || token.AccessToken != "refreshed-token" {
			t.Errorf("call %d AccessToken = %v, want the retained refreshed-token", i+1, token)
		}
	}
	if got := retryLocks.Load(); got != int32(saveRetryAttempts) {
		t.Errorf("retry lock acquisitions = %d, want %d: retries must be bounded", got, saveRetryAttempts)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
}

func TestAuthResolverConcurrentCallersShareSaveRetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	started, acquisitions, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	const callers = 8
	tokens := make([]*oauth2.Token, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			tokens[i], errs[i] = resolver.Resolve(context.Background(), store, conf, nil)
		}(i)
	}
	close(start)
	<-started
	open()
	wg.Wait()

	for i := range tokens {
		if errs[i] != nil {
			t.Errorf("Resolve() caller %d error = %v", i, errs[i])
			continue
		}
		if tokens[i] == nil || tokens[i].AccessToken != "refreshed-token" {
			t.Errorf("caller %d AccessToken = %v, want refreshed-token", i, tokens[i])
		}
	}
	if got := acquisitions.Load(); got != 1 {
		t.Errorf("token lock acquisitions = %d, want 1: concurrent callers must share one save retry", got)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1", got)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-token", stored.AccessToken)
	}
}

func TestAuthResolverSaveRetryCallerCancellation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	server, refreshes := refreshServer(t, 0)
	clock := newTestClock()
	resolver := NewAuthResolver()
	resolver.now = clock.Now
	conf := testTokenConf(server.URL)

	failInitialSave(t, resolver, store, conf, dir, path)
	unlockTokenFile(t, dir)

	started, _, open := gateRetryLock(resolver)
	defer open()
	clock.Advance(saveRetryBaseDelay)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(ctx, store, conf, nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}

	// The retry still runs for the other callers, so this call joins it and gets
	// the rotated token without another refresh.
	open()
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
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("persisted AccessToken = %q, want refreshed-token", stored.AccessToken)
	}
}
