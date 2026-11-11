package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// errNoServer is returned when no enabled server declares the file's extension.
var errNoServer = errors.New("no server for file extension")

// errManagerClosed is returned when a request reaches the manager after
// Manager.Close started. It also covers a spawn that finishes after shutdown:
// such a session is closed instead of published.
var errManagerClosed = errors.New("lsp manager closed")

// spawnFailureBackoff is how long a server that failed to start is left alone
// before another request retries it. It is deliberately distinct from
// IdleTimeout, which governs how long a healthy but unused session is kept.
const spawnFailureBackoff = 30 * time.Second

// sessionCloseTimeout bounds every session close the manager owns: idle reaping,
// crash cleanup, late-spawn cleanup and shutdown. Callers must not hold the
// manager or an entry lock while it elapses.
const sessionCloseTimeout = 5 * time.Second

// Manager owns the language server processes for a session.
type Manager struct {
	cfg       config.LSPConfig
	workspace string
	wrap      WrapFn
	release   ReleaseFn
	warnFn    func(string)
	stderr    io.Writer

	mgrCtx      context.Context
	mgrCancel   context.CancelFunc
	spawnCtx    context.Context
	spawnCancel context.CancelFunc

	mu       sync.Mutex
	sessions map[sessionKey]*entry
	// closing is set by Close before it sweeps entries. New lookups and spawn
	// publications read it, so nothing becomes usable after shutdown starts.
	closing bool

	// closeOnce runs Close's shutdown exactly once; concurrent callers wait for
	// it to finish.
	closeOnce sync.Once

	reapTicker *time.Ticker
	stopReaper chan struct{}
	reaperDone chan struct{} // closed when reapIdle returns; nil without a reaper

	resultCache *resultCache
}

// session is one live connection to a language server process.
type session interface {
	Definition(ctx context.Context, file string, line, col int) ([]Location, error)
	Implementation(ctx context.Context, file string, line, col int) ([]Location, error)
	TypeDefinition(ctx context.Context, file string, line, col int) ([]Location, error)
	References(ctx context.Context, file string, line, col int, includeDecl bool) ([]Location, error)
	Hover(ctx context.Context, file string, line, col int) (HoverContent, error)
	WorkspaceSymbol(ctx context.Context, query string) ([]SymbolInfo, error)
	DocumentSymbol(ctx context.Context, file string) ([]SymbolInfo, error)
	DidOpen(ctx context.Context, file, languageID, text string, version int32) error
	DidClose(ctx context.Context, file string) error
	// Diagnostics streams publishDiagnostics notifications. The channel is never
	// closed; select on Exited to observe termination.
	Diagnostics() <-chan PublishedDiagnostics
	// Progress streams $/progress notifications. The channel is never closed;
	// select on Exited to observe termination.
	Progress() <-chan ProgressEvent
	Exited() <-chan struct{}
	Close(ctx context.Context) error
}

// sessionKey uniquely identifies a (server, root) pair.
type sessionKey struct {
	server string
	root   string
}

// entry tracks one active server session.
type entry struct {
	state     ServerState
	session   session
	readiness *readiness
	mu        sync.Mutex
	ready     chan struct{}
	cycleMu   sync.Mutex // serializes open→request→close cycles for the same session
	leases    int        // guarded by mu; nonzero pins the entry against the idle reaper
}

// ManagerOptions holds the optional settings for NewManager. Its zero value is
// valid.
type ManagerOptions struct {
	// Release frees resources owned by a wrapped server command. Optional.
	Release ReleaseFn
	// WarnFn receives recoverable errors such as server spawn failures. Nil
	// means no warnings.
	WarnFn func(string)
	// Stderr becomes each language server process's stderr. Optional.
	Stderr io.Writer
}

// NewManager creates a Manager with the given configuration.
// wrap is optional and transforms the command before launch (e.g. sandbox wrapping).
// opts.WarnFn is called on recoverable errors (e.g. spawn failure).
// workspace is the default workspace root when no markers are found.
func NewManager(cfg config.LSPConfig, workspace string, wrap WrapFn, opts ManagerOptions) *Manager {
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	spawnCtx, spawnCancel := context.WithCancel(context.Background())
	warnFn := opts.WarnFn
	if warnFn == nil {
		warnFn = func(string) {}
	}

	m := &Manager{
		cfg:         cfg,
		workspace:   workspace,
		wrap:        wrap,
		release:     opts.Release,
		warnFn:      warnFn,
		stderr:      opts.Stderr,
		mgrCtx:      mgrCtx,
		mgrCancel:   mgrCancel,
		spawnCtx:    spawnCtx,
		spawnCancel: spawnCancel,
		sessions:    make(map[sessionKey]*entry),
		stopReaper:  make(chan struct{}),
		resultCache: newResultCache(resultCacheMaxEntries),
	}

	// Start idle reaper if IdleTimeout is non-zero.
	if !cfg.IdleTimeout.IsZero() {
		idleNanos := cfg.IdleTimeout.Duration()
		interval := idleNanos / 4
		maxInterval := int64(30 * time.Second)
		if interval > maxInterval {
			interval = maxInterval
		}
		m.reapTicker = time.NewTicker(time.Duration(interval))
		m.reaperDone = make(chan struct{})
		go m.reapIdle()
	}

	return m
}

// acquireEntry returns or spawns a live entry (server session wrapper) for the
// given file and takes a lease on it. The lease keeps the idle reaper from
// closing the session while the caller runs its document cycle; the caller must
// Release it once that whole cycle finishes. It returns errNoServer if no
// enabled server declares the file's extension, and errManagerClosed after
// Close. The caller must not hold m.mu when calling this method.
func (m *Manager) acquireEntry(ctx context.Context, file string) (*entry, session, *entryLease, error) {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	if serverName == "" {
		m.mu.Unlock()
		return nil, nil, nil, errNoServer
	}

	srv := m.cfg.Servers[serverName]
	m.mu.Unlock()

	// Resolve the root.
	root := resolveRoot(file, m.workspace, srv.RootMarkers)

	return m.acquireEntryForKey(ctx, serverName, srv, sessionKey{server: serverName, root: root})
}

// closedErr reports errManagerClosed once Close has started.
func (m *Manager) closedErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return errManagerClosed
	}
	return nil
}

// acquireEntryForKey is acquireEntry's state machine, keyed directly so
// workspace-mode symbol search can reuse it without a file to route through.
func (m *Manager) acquireEntryForKey(ctx context.Context, serverName string, srv config.LSPServerConfig, key sessionKey) (*entry, session, *entryLease, error) {
	ent, err := m.lookupEntry(serverName, key)
	if err != nil {
		return nil, nil, nil, err
	}

	// Loop until we have a session or a definite error.
	for {
		// Close may have started while this call waited on a spawn. Check before
		// every pass: a waiter woken by shutdown must not start a server.
		if err := m.closedErr(); err != nil {
			return nil, nil, nil, err
		}

		ent.mu.Lock()
		switch ent.state.Status {
		case ServerStatusReady:
			sess, lease, retry := m.readyEntry(ent)
			if retry {
				continue
			}
			return ent, sess, lease, nil
		case ServerStatusStarting:
			ready := ent.ready
			ent.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, nil, nil, ctx.Err()
			}
		case ServerStatusFailed:
			if time.Since(ent.state.StartedAt) < spawnFailureBackoff {
				err := ent.state.Err
				ent.mu.Unlock()
				return nil, nil, nil, err
			}
			ent.state.Status = ServerStatusDeclared
			ent.state.StartedAt = time.Now()
			fallthrough
		case ServerStatusDeclared, ServerStatusStopped:
			return m.spawnEntry(ctx, ent, serverName, srv, key.root)
		default:
			status := ent.state.Status
			ent.mu.Unlock()
			return nil, nil, nil, fmt.Errorf("unexpected server status: %s", status)
		}
	}
}

// lookupEntry returns the entry for key, creating a declared one when none
// exists, and refreshes LastUsed on an existing entry. It returns
// errManagerClosed instead of creating an entry once Close has started. The
// caller must not hold m.mu or any entry mutex.
func (m *Manager) lookupEntry(serverName string, key sessionKey) (*entry, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errManagerClosed
	}
	ent := m.sessions[key]
	existing := ent != nil
	if ent == nil {
		ent = &entry{
			state: ServerState{
				Name:      serverName,
				Root:      key.root,
				Status:    ServerStatusDeclared,
				StartedAt: time.Now(),
				LastUsed:  time.Now(),
			},
		}
		m.sessions[key] = ent
	}
	m.mu.Unlock()

	if existing {
		// LastUsed is entry state, so it is written under ent.mu like every
		// other entry.state field below. m.mu is released first (rather than
		// nesting m.mu -> ent.mu) so this path holds only one lock at a time.
		ent.mu.Lock()
		ent.state.LastUsed = time.Now()
		ent.mu.Unlock()
	}
	return ent, nil
}

// readyEntry returns the live session of a Ready entry and takes a lease on it.
// The caller must hold ent.mu, and readyEntry releases it before returning. The
// lease is taken under ent.mu together with the session selection, so the idle
// reaper cannot claim the entry in between. A session whose process has exited
// is retired here: the entry moves to Stopped and retry is true so the caller
// loops to respawn it (a failed respawn lands in ServerStatusFailed and honours
// spawnFailureBackoff).
func (m *Manager) readyEntry(ent *entry) (sess session, lease *entryLease, retry bool) {
	sess = ent.session
	if sess != nil {
		select {
		case <-sess.Exited():
			// The server crashed or was killed: nothing else flips the status.
			ent.state.Status = ServerStatusStopped
			ent.session = nil
			ent.mu.Unlock()
			closeSession(sess)
			return nil, nil, true
		default:
		}
	}
	lease = ent.acquireLease()
	ent.mu.Unlock()
	return sess, lease, false
}

// spawnEntry starts the server for ent. The caller must hold ent.mu with the
// entry in Declared, Stopped or Failed-after-backoff. spawnEntry marks the entry
// Starting, releases ent.mu for the spawn, then publishes the session (Ready)
// with the caller's lease or the failure (Failed) and wakes waiters on
// ent.ready. It returns with ent.mu released.
func (m *Manager) spawnEntry(ctx context.Context, ent *entry, serverName string, srv config.LSPServerConfig, root string) (*entry, session, *entryLease, error) {
	ent.state.Status = ServerStatusStarting
	ent.state.StartedAt = time.Now()
	ent.ready = make(chan struct{})
	ready := ent.ready
	ent.mu.Unlock()

	sess, spawnErr := m.spawnServer(ctx, serverName, srv, root)
	r := newReadiness()

	lease, err := m.completeSpawn(ent, sess, r, spawnErr, ready)
	if err != nil {
		if !errors.Is(err, errManagerClosed) {
			m.warnFn(fmt.Sprintf("spawn server %s: %v", serverName, err))
		}
		return nil, nil, nil, err
	}

	go m.trackReadiness(ent, sess, r)

	return ent, sess, lease, nil
}

// completeSpawn records the outcome of a spawn attempt and wakes the waiters on
// ready. Publication checks the manager's closing flag under m.mu and writes the
// entry under ent.mu, the same order Close, doReap and ServerStates take, so a
// session is either visible to Close's sweep or rejected here. A session that
// arrives after Close started is never published: the entry stays stopped, the
// session is closed outside both locks, and errManagerClosed is returned.
func (m *Manager) completeSpawn(ent *entry, sess session, r *readiness, spawnErr error, ready chan struct{}) (*entryLease, error) {
	m.mu.Lock()
	ent.mu.Lock()
	closing := m.closing
	close(ready)

	var lease *entryLease
	switch {
	case closing:
		ent.state.Status = ServerStatusStopped
		ent.session = nil
	case spawnErr != nil:
		ent.state.Status = ServerStatusFailed
		ent.state.Err = spawnErr
	default:
		ent.state.Status = ServerStatusReady
		ent.session = sess
		ent.readiness = r
		lease = ent.acquireLease()
	}
	ent.mu.Unlock()
	m.mu.Unlock()

	if closing {
		closeSession(sess)
		return nil, errManagerClosed
	}
	return lease, spawnErr
}

// closeSession releases a session's resources (connection, pipes, reaped
// process) using the manager's bounded close timeout. The caller must not hold
// the manager lock or any entry lock.
func closeSession(sess session) {
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionCloseTimeout)
	defer cancel()
	_ = sess.Close(ctx) // best-effort: the manager has already given up on the session
}

// resolveSessionKey computes the (server, root) session key for a file without
// side effects. It is used by the result cache to build cache keys.
// On error, it returns ok=false; the caller should skip the cache (best-effort).
func (m *Manager) resolveSessionKey(file string) (sessionKey, bool) {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	m.mu.Unlock()

	if serverName == "" {
		return sessionKey{}, false // no server for this extension
	}

	root := resolveRoot(file, m.workspace, m.cfg.Servers[serverName].RootMarkers)

	return sessionKey{server: serverName, root: root}, true
}

// existingSessionKeyForServer returns the sessionKey of an already-created entry
// for serverName, if any (lexicographically smallest root if more than one, for
// determinism), so workspace-mode symbol search prefers a running session over
// spawning a second one at a different root for the same server.
func (m *Manager) existingSessionKeyForServer(serverName string) (sessionKey, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found sessionKey
	ok := false
	for key := range m.sessions {
		if key.server != serverName {
			continue
		}
		if !ok || key.root < found.root {
			found = key
			ok = true
		}
	}
	return found, ok
}

// enabledServerNames returns the names of enabled servers in cfg.Servers, sorted.
// Held under m.mu even though cfg is not mutated after construction, matching the
// locking discipline serverForExtension already documents for reading m.cfg.Servers.
func (m *Manager) enabledServerNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.cfg.Servers))
	for name, srv := range m.cfg.Servers {
		if srv.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// spawnServer spawns a single server process with the configured environment.
func (m *Manager) spawnServer(_ context.Context, _ string, srv config.LSPServerConfig, root string) (session, error) {
	// Get cache directory for this root.
	cacheDir, err := cacheDirFor(m.cfg.CacheDir, root)
	if err != nil {
		return nil, fmt.Errorf("cache dir: %w", err)
	}

	env := buildServerEnv(os.Environ(), srv, cacheDir)

	// Create a child context with a handshake timeout.
	handshakeCtx, cancel := context.WithTimeout(m.spawnCtx, time.Duration(m.cfg.ReadyTimeout.Duration()))
	defer cancel()

	spec := TransportSpec{
		Command:               srv.Command,
		Args:                  srv.Args,
		Env:                   env,
		RootPath:              root,
		InitializationOptions: srv.InitializationOptions,
		Stderr:                m.stderr,
		Wrap:                  m.wrap,
		Release:               m.release,
	}

	sess, err := newTransport(handshakeCtx, m.mgrCtx, spec)
	if err != nil {
		return nil, fmt.Errorf("spawn transport: %w", err)
	}

	return sess, nil
}

// buildServerEnv materialises the environment for a spawned language server.
// base is steiner's own environment: servers such as gopls shell out to their
// toolchain constantly and are non-functional without PATH. srv.Env overrides
// the inherited values, and the cache-related defaults apply only when srv.Env
// leaves them unset, so the per-workspace cache directory wins over the host's.
// The result is sorted so repeated spawns produce an identical environment.
func buildServerEnv(base []string, srv config.LSPServerConfig, cacheDir string) []string {
	env := make(map[string]string, len(base)+len(srv.Env)+3)

	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}

	for k, v := range srv.Env {
		env[k] = v
	}

	if _, ok := srv.Env["HOME"]; !ok {
		env["HOME"] = cacheDir
	}
	if _, ok := srv.Env["XDG_CACHE_HOME"]; !ok {
		env["XDG_CACHE_HOME"] = cacheDir
	}
	if _, ok := srv.Env["GOCACHE"]; !ok {
		env["GOCACHE"] = filepath.Join(cacheDir, "go")
	}

	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// serverForExtension returns the enabled server that declares the file
// extension, comparing extensions case-insensitively. When several enabled
// servers declare the extension it returns the lexicographically smallest
// name, so routing stays stable regardless of map iteration order even when a
// caller supplies a config that validation would have rejected.
// Must be called with m.mu held.
func (m *Manager) serverForExtension(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	names := make([]string, 0, len(m.cfg.Servers))
	for name, srv := range m.cfg.Servers {
		if srv.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		for _, e := range m.cfg.Servers[name].FileExtensions {
			if strings.ToLower(e) == ext {
				return name
			}
		}
	}
	return ""
}

// ServerStates returns a snapshot of all server states.
func (m *Manager) ServerStates() []ServerState {
	m.mu.Lock()
	defer m.mu.Unlock()

	var states []ServerState
	for _, ent := range m.sessions {
		ent.mu.Lock()
		states = append(states, ent.state)
		ent.mu.Unlock()
	}
	return states
}

// Close gracefully shuts down all live sessions and stops the reaper. It is
// idempotent and safe to call concurrently: the first caller performs the
// shutdown and the rest wait for it to finish. Outstanding entry leases do not
// postpone shutdown; Close overrides them. A session from a spawn that finishes
// after Close started is closed by the goroutine that spawned it rather than
// published.
func (m *Manager) Close() error {
	m.closeOnce.Do(m.close)
	return nil
}

func (m *Manager) close() {
	// Reject new lookups and spawn publications before anything can wait.
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()

	m.spawnCancel()

	if m.resultCache != nil {
		m.resultCache.clear()
	}

	// stopReaper is closed exactly once because close runs under closeOnce. Join
	// the reaper so a reap already in progress finishes its bounded session
	// closes before shutdown returns.
	if m.reapTicker != nil {
		m.reapTicker.Stop()
		close(m.stopReaper)
		<-m.reaperDone
	}

	// Sweep under m.mu then ent.mu (the same order as completeSpawn and doReap),
	// so no spawn can publish a session between the closing check and this mark.
	// Sessions are closed below, outside both locks.
	m.mu.Lock()
	var sessions []session
	for _, ent := range m.sessions {
		ent.mu.Lock()
		sess := ent.session
		if ent.state.Status == ServerStatusReady && sess != nil {
			sessions = append(sessions, sess)
		}
		ent.state.Status = ServerStatusStopped
		ent.mu.Unlock()
	}
	m.mu.Unlock()

	// Close all sessions concurrently.
	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Add(1)
		go func(sess session) {
			defer wg.Done()
			closeSession(sess)
		}(sess)
	}
	wg.Wait()

	m.mgrCancel()
}

// reapIdle is the ticker goroutine that terminates idle sessions.
func (m *Manager) reapIdle() {
	defer close(m.reaperDone)

	for {
		select {
		case <-m.reapTicker.C:
			m.doReap()
		case <-m.stopReaper:
			return
		}
	}
}

// doReap closes sessions whose LastUsed exceeds IdleTimeout. Entries with an
// outstanding lease are skipped: a document cycle is using them, whether it is
// waiting for readiness, queued on cycleMu, or issuing its request. The claim is
// made under ent.mu, so it cannot race a lease acquired with the session.
func (m *Manager) doReap() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	idleThreshold := now.Add(-time.Duration(m.cfg.IdleTimeout.Duration()))

	// Snapshot the sessions under ent.mu; reading ent.session without the lock
	// would race with spawnEntry assigning it after a spawn.
	var toClose []session
	for _, ent := range m.sessions {
		ent.mu.Lock()
		if ent.state.Status == ServerStatusReady && ent.leases == 0 && ent.state.LastUsed.Before(idleThreshold) {
			toClose = append(toClose, ent.session)
			ent.state.Status = ServerStatusStopped
		}
		ent.mu.Unlock()
	}
	m.mu.Unlock()

	for _, sess := range toClose {
		closeSession(sess)
	}
}
