package oauth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	// codexRefreshTimeout bounds one detached token refresh, including its wait
	// for the cross-process token lock. It is deliberately independent of caller
	// contexts: one caller giving up must not abort a refresh that other callers
	// are waiting for.
	codexRefreshTimeout = 30 * time.Second

	// stableReadAttempts bounds the stat/load/stat attempts of one stable read
	// before it reports errTokenFileUnstable.
	stableReadAttempts = 3

	// refreshRounds bounds the read/refresh/save rounds of one flight, so a file
	// that other writers keep replacing cannot spin.
	refreshRounds = 3
)

// errTokenFileUnstable reports a token file that kept changing on every attempt
// to read or refresh it consistently.
var errTokenFileUnstable = errors.New("token file changed while resolving")

// AuthResolver caches OAuth tokens per token-store path for the life of the
// process and shares one refresh between concurrent callers. A cached token is
// reused only while the token file keeps the same identity, size, and
// modification time, so a rewrite or deletion is picked up on the next call.
//
// A cached token is always paired with the stamp of one stable read: the file is
// statted before and after the load and the read restarts when the stamps
// differ. No stamp is ever taken straight after a write, so a concurrent
// replacement cannot pair an old token with a newer file version.
//
// A refresh runs detached from caller contexts, under a fixed 30-second
// deadline. The HTTP client of the caller that starts the flight serves the
// refresh; callers that join the flight do not change it.
//
// A token without expiry is treated as valid and is never refreshed, matching
// oauth2.Token.Valid; it is served as stored.
//
// A refreshed token that cannot be saved is kept in memory and served by later
// calls; the next refresh starts from it and retries the save, and the save
// error is reported to the caller that triggered it.
type AuthResolver struct {
	mu      sync.Mutex
	entries map[string]*authEntry
	// refreshTimeout overrides codexRefreshTimeout when positive.
	refreshTimeout time.Duration
	// newTokenFile overrides the file access used for a store; tests set it.
	newTokenFile func(*TokenStore) tokenFile
}

// NewAuthResolver returns an empty AuthResolver.
func NewAuthResolver() *AuthResolver {
	return &AuthResolver{entries: make(map[string]*authEntry)}
}

// authEntry holds the cache and the in-flight refresh for one token path. The
// store is shared so loads and saves on the same path serialize on its mutex.
type authEntry struct {
	store *TokenStore

	mu        sync.Mutex
	token     *oauth2.Token
	stamp     fileStamp
	valid     bool
	persisted bool
	flight    *authFlight
}

// authFlight is one detached refresh. Its fields are written before done is
// closed, so waiters read them only after receiving from done.
type authFlight struct {
	done  chan struct{}
	token *oauth2.Token
	err   error
}

// Resolve returns a token for store, loading it from disk when the file changed
// since the last call and refreshing it when it is expired or within
// proactiveRefreshWindow of expiring. A token without expiry is valid, matching
// oauth2.Token.Valid, so it is never refreshed.
//
// httpClient, when non-nil, serves the refresh started by this call. A caller
// that joins a refresh already in flight uses the client of the caller that
// started it. ctx cancels only this caller's wait for the refresh; the refresh
// itself runs to completion for the other callers.
func (r *AuthResolver) Resolve(ctx context.Context, store *TokenStore, conf *oauth2.Config, httpClient *http.Client) (*oauth2.Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	entry := r.entry(store.path)
	file := r.tokenFileFor(store)

	stamp, err := file.stat()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			entry.invalidate()
			return nil, ErrNoToken
		}
		// A transient stat failure says nothing about the file, so the cache is
		// kept: dropping it here would discard an unpersisted refresh whose
		// rotated refresh token is the only usable credential.
		return nil, fmt.Errorf("stat token file: %w", err)
	}
	if token, ok := entry.cached(stamp); ok {
		return token, nil
	}
	return r.waitForRefresh(ctx, entry, file, conf, httpClient)
}

// tokenFile is the token file access a stable read needs. TokenStore backs it in
// production.
type tokenFile interface {
	stat() (fileStamp, error)
	load() (*oauth2.Token, error)
}

// storeTokenFile adapts a TokenStore to tokenFile.
type storeTokenFile struct {
	store *TokenStore
}

func (f storeTokenFile) stat() (fileStamp, error) { return statTokenFile(f.store.path) }

func (f storeTokenFile) load() (*oauth2.Token, error) { return f.store.Load() }

// tokenFileFor returns the file access for store.
func (r *AuthResolver) tokenFileFor(store *TokenStore) tokenFile {
	if r.newTokenFile != nil {
		return r.newTokenFile(store)
	}
	return storeTokenFile{store: store}
}

// entry returns the shared cache entry for path, creating it when needed.
func (r *AuthResolver) entry(path string) *authEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.entries[path]
	if entry == nil {
		entry = &authEntry{store: NewTokenStore(path)}
		r.entries[path] = entry
	}
	return entry
}

// cached returns the cached token when stamp still describes the file version
// it was loaded from and the token does not need a refresh.
func (e *authEntry) cached(stamp fileStamp) (*oauth2.Token, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.flight != nil || !e.valid || !e.stamp.equal(stamp) {
		return nil, false
	}
	if tokenNeedsRefresh(e.token, time.Now()) {
		return nil, false
	}
	return cloneToken(e.token), true
}

// invalidate drops the cached token after the file disappeared or could not be
// inspected.
func (e *authEntry) invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.token = nil
	e.stamp = fileStamp{}
	e.valid = false
	e.persisted = false
}

// waitForRefresh joins the in-flight refresh for entry, starting one when none
// is running.
func (r *AuthResolver) waitForRefresh(ctx context.Context, entry *authEntry, file tokenFile, conf *oauth2.Config, httpClient *http.Client) (*oauth2.Token, error) {
	entry.mu.Lock()
	flight := entry.flight
	if flight == nil {
		flight = &authFlight{done: make(chan struct{})}
		entry.flight = flight
		entry.mu.Unlock()
		go r.refresh(entry, flight, file, conf, httpClient)
	} else {
		entry.mu.Unlock()
	}

	select {
	case <-flight.done:
		if flight.err != nil {
			return nil, flight.err
		}
		return cloneToken(flight.token), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// refresh publishes the result of one detached load and refresh to the entry's
// waiters. A token is cached whenever it comes with a stable stamp, including
// the token of a refresh that could not be saved: the refresh token was rotated
// server-side, so that token is the only usable credential left for this
// process even though the save error is still reported to the caller. An unsaved
// token is marked unpersisted, so the next refresh starts from it and retries
// the save instead of using the older refresh token on disk.
func (r *AuthResolver) refresh(entry *authEntry, flight *authFlight, file tokenFile, conf *oauth2.Config, httpClient *http.Client) {
	retained, retainedStamp := entry.retained()
	token, stamp, persisted, err := r.loadOrRefresh(entry.store, file, conf, httpClient, retained, retainedStamp)

	entry.mu.Lock()
	switch {
	case token != nil && stamp.exists:
		entry.token, entry.stamp, entry.valid, entry.persisted = cloneToken(token), stamp, true, persisted
	case errors.Is(err, ErrNoToken):
		entry.token, entry.stamp, entry.valid, entry.persisted = nil, fileStamp{}, false, false
	}
	entry.flight = nil
	entry.mu.Unlock()

	flight.token, flight.err = token, err
	close(flight.done)
}

// retained returns the cached token that a previous refresh could not persist,
// so the next refresh uses its rotated refresh token rather than the older one
// still on disk.
func (e *authEntry) retained() (*oauth2.Token, fileStamp) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.valid || e.persisted || e.token == nil {
		return nil, fileStamp{}
	}
	return cloneToken(e.token), e.stamp
}

// loadOrRefresh returns a token, the stamp of the file version it belongs to,
// whether that version is on disk, and any error.
//
// The first round uses retained when the file still holds the version retained
// was refreshed from; a file written since then wins. Every stamp comes from a
// stable read, including after a save: the next round re-reads the file, so a
// replacement that lands after the save is adopted when it holds a valid token
// or refreshed again. A token that is merely inside the proactive window still
// ends the round after a save, so a short-lived token costs one refresh. The
// round count is bounded so a file that keeps changing cannot spin.
func (r *AuthResolver) loadOrRefresh(store *TokenStore, file tokenFile, conf *oauth2.Config, httpClient *http.Client, retained *oauth2.Token, retainedStamp fileStamp) (*oauth2.Token, fileStamp, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout())
	defer cancel()

	unlock, err := acquireTokenLock(ctx, store.path)
	if err != nil {
		return nil, fileStamp{}, false, err
	}
	defer unlock()

	saved := false
	for round := 0; round < refreshRounds; round++ {
		token, stamp, persisted, err := currentToken(file, retained, retainedStamp)
		retained = nil
		if err != nil {
			return nil, fileStamp{}, false, err
		}

		now := time.Now()
		if saved && tokenValid(token, now) {
			return token, stamp, persisted, nil
		}
		if !saved && !tokenNeedsRefresh(token, now) {
			return token, stamp, persisted, nil
		}

		refreshed, err := refreshToken(ctx, conf, httpClient, token)
		if err != nil {
			return nil, fileStamp{}, false, err
		}

		// The save is skipped when the check below sees a removed file, but a
		// removal racing the write can still be overwritten: login and other
		// writers do not take the lock.
		if _, err := file.stat(); err != nil {
			return nil, fileStamp{}, false, statError(err)
		}
		if err := store.Save(refreshed); err != nil {
			return refreshed, stamp, false, fmt.Errorf("save refreshed token: %w", err)
		}
		saved = true
	}
	return nil, fileStamp{}, false, errTokenFileUnstable
}

// currentToken returns the credential to use: retained while the file still
// holds the version it came from, otherwise a stable read of the file.
func currentToken(file tokenFile, retained *oauth2.Token, retainedStamp fileStamp) (*oauth2.Token, fileStamp, bool, error) {
	if retained != nil {
		stamp, err := file.stat()
		if err != nil {
			return nil, fileStamp{}, false, statError(err)
		}
		if stamp.equal(retainedStamp) {
			return retained, retainedStamp, false, nil
		}
	}
	token, stamp, err := readStable(file)
	if err != nil {
		return nil, fileStamp{}, false, err
	}
	return token, stamp, true, nil
}

// readStable loads the token with the stamp of the file version it came from. It
// restarts when the file changes between the stat, the load, and the next stat,
// so the returned pair always describes one version. A file that disappears
// while it is read reports ErrNoToken.
func readStable(file tokenFile) (*oauth2.Token, fileStamp, error) {
	for attempt := 0; attempt < stableReadAttempts; attempt++ {
		before, err := file.stat()
		if err != nil {
			return nil, fileStamp{}, statError(err)
		}
		token, err := file.load()
		if err != nil {
			return nil, fileStamp{}, tokenFileError(err)
		}
		after, err := file.stat()
		if err != nil {
			return nil, fileStamp{}, statError(err)
		}
		if before.equal(after) {
			return token, after, nil
		}
	}
	return nil, fileStamp{}, errTokenFileUnstable
}

// statError maps a missing token file to ErrNoToken and wraps other stat
// failures.
func statError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoToken
	}
	return fmt.Errorf("stat token file: %w", err)
}

// tokenFileError maps a load of a missing token file to ErrNoToken.
func tokenFileError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoToken
	}
	return err
}

// refreshToken exchanges token's refresh token for a new token and keeps stored
// metadata the response omits.
func refreshToken(ctx context.Context, conf *oauth2.Config, httpClient *http.Client, token *oauth2.Token) (*oauth2.Token, error) {
	inner := conf.TokenSource(withHTTPClient(ctx, httpClient), token)
	refreshed, err := oauth2.ReuseTokenSourceWithExpiry(token, inner, proactiveRefreshWindow).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return preserveTokenMetadata(refreshed, token), nil
}

// timeout returns the shared refresh deadline.
func (r *AuthResolver) timeout() time.Duration {
	if r.refreshTimeout > 0 {
		return r.refreshTimeout
	}
	return codexRefreshTimeout
}

// withHTTPClient returns ctx carrying client for the token endpoint request. A
// nil client leaves the default transport in place.
func withHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

// tokenValid reports whether token can be used: it has an access token and is
// not expired. A token without expiry is valid, matching oauth2.Token.Valid.
func tokenValid(token *oauth2.Token, now time.Time) bool {
	if token == nil || token.AccessToken == "" {
		return false
	}
	if token.Expiry.IsZero() {
		return true
	}
	return token.Expiry.Round(0).After(now)
}

// tokenNeedsRefresh reports whether token is unusable or inside the proactive
// refresh window. A token without expiry never needs a refresh.
func tokenNeedsRefresh(token *oauth2.Token, now time.Time) bool {
	if !tokenValid(token, now) {
		return true
	}
	if token.Expiry.IsZero() {
		return false
	}
	return !token.Expiry.Round(0).Add(-proactiveRefreshWindow).After(now)
}

// fileStamp identifies one version of the token file. exists reports whether the
// stamp came from a successful stat.
type fileStamp struct {
	modTime time.Time
	size    int64
	inode   uint64
	exists  bool
}

// statTokenFile captures the current version stamp of path.
func statTokenFile(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{
		modTime: info.ModTime(),
		size:    info.Size(),
		inode:   fileInode(info),
		exists:  true,
	}, nil
}

// equal reports whether both stamps describe the same file version. The inode
// catches atomic replacements that keep size and modification time; platforms
// that expose no inode fall back to those two fields.
func (s fileStamp) equal(other fileStamp) bool {
	if !s.exists || !other.exists {
		return false
	}
	return s.inode == other.inode && s.size == other.size && s.modTime.Equal(other.modTime)
}
