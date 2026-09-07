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

// spawnFailureBackoff is how long a server that failed to start is left alone
// before another request retries it. It is deliberately distinct from
// IdleTimeout, which governs how long a healthy but unused session is kept.
const spawnFailureBackoff = 30 * time.Second

// Manager owns the language server processes for a session.
type Manager struct {
	cfg       config.LSPConfig
	workspace string
	wrap      WrapFn
	warnFn    func(string)
	stderr    io.Writer

	mgrCtx      context.Context
	mgrCancel   context.CancelFunc
	spawnCtx    context.Context
	spawnCancel context.CancelFunc

	mu       sync.Mutex
	sessions map[sessionKey]*entry

	reapTicker *time.Ticker
	stopReaper chan struct{}

	resultCache *resultCache
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
}

// NewManager creates a Manager with the given configuration.
// wrap is optional and transforms the command before launch (e.g. sandbox wrapping).
// warnFn is called on recoverable errors (e.g. spawn failure).
// workspace is the default workspace root when no markers are found.
func NewManager(cfg config.LSPConfig, workspace string, wrap WrapFn, warnFn func(string), stderr io.Writer) *Manager {
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	spawnCtx, spawnCancel := context.WithCancel(context.Background())

	m := &Manager{
		cfg:         cfg,
		workspace:   workspace,
		wrap:        wrap,
		warnFn:      warnFn,
		stderr:      stderr,
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
		go m.reapIdle()
	}

	return m
}

// entryFor returns or spawns a live entry (server session wrapper) for the given file.
// It returns errNoServer if no enabled server declares the file's extension.
// The caller must not hold m.mu when calling this method.
func (m *Manager) entryFor(ctx context.Context, file string) (*entry, session, error) {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	if serverName == "" {
		m.mu.Unlock()
		return nil, nil, errNoServer
	}

	srv := m.cfg.Servers[serverName]
	m.mu.Unlock()

	// Resolve the root.
	root := resolveRoot(file, m.workspace, srv.RootMarkers)

	key := sessionKey{server: serverName, root: root}

	m.mu.Lock()
	ent := m.sessions[key]
	if ent == nil {
		ent = &entry{
			state: ServerState{
				Name:      serverName,
				Root:      root,
				Status:    ServerStatusDeclared,
				StartedAt: time.Now(),
				LastUsed:  time.Now(),
			},
		}
		m.sessions[key] = ent
	} else {
		ent.state.LastUsed = time.Now()
	}
	m.mu.Unlock()

	// Loop until we have a session or a definite error.
	for {
		ent.mu.Lock()
		switch ent.state.Status {
		case ServerStatusReady:
			sess := ent.session
			ent.mu.Unlock()
			return ent, sess, nil
		case ServerStatusStarting:
			ready := ent.ready
			ent.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		case ServerStatusFailed:
			if time.Since(ent.state.StartedAt) < spawnFailureBackoff {
				err := ent.state.Err
				ent.mu.Unlock()
				return nil, nil, err
			}
			ent.state.Status = ServerStatusDeclared
			ent.state.StartedAt = time.Now()
			fallthrough
		case ServerStatusDeclared, ServerStatusStopped:
			ent.state.Status = ServerStatusStarting
			ent.state.StartedAt = time.Now()
			ent.ready = make(chan struct{})
			ready := ent.ready
			ent.mu.Unlock()

			sess, err := m.spawnServer(ctx, serverName, srv, root)

			ent.mu.Lock()
			close(ready)
			if err != nil {
				ent.state.Status = ServerStatusFailed
				ent.state.Err = err
				ent.mu.Unlock()
				m.warnFn(fmt.Sprintf("spawn server %s: %v", serverName, err))
				return nil, nil, err
			}

			ent.state.Status = ServerStatusReady
			ent.session = sess
			r := newReadiness(m.cfg)
			ent.readiness = r
			ent.mu.Unlock()

			go m.trackReadiness(ent, sess, r)

			return ent, sess, nil
		default:
			status := ent.state.Status
			ent.mu.Unlock()
			return nil, nil, fmt.Errorf("unexpected server status: %s", status)
		}
	}
}

// sessionFor returns or spawns a live session for the given file.
// It returns errNoServer if no enabled server declares the file's extension.
func (m *Manager) sessionFor(ctx context.Context, file string) (session, error) {
	_, sess, err := m.entryFor(ctx, file)
	return sess, err
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

// serverForExtension returns the first enabled server that declares the file extension.
// Must be called with m.mu held.
func (m *Manager) serverForExtension(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	for name, srv := range m.cfg.Servers {
		if !srv.Enabled {
			continue
		}
		for _, e := range srv.FileExtensions {
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

// Close gracefully shuts down all live sessions and stops the reaper.
// It is idempotent: calling it multiple times is safe.
func (m *Manager) Close() error {
	m.spawnCancel()
	defer m.mgrCancel()

	if m.resultCache != nil {
		m.resultCache.clear()
	}

	if m.reapTicker != nil {
		m.reapTicker.Stop()
		select {
		case <-m.stopReaper:
			// Already closed.
		default:
			close(m.stopReaper)
		}
	}

	m.mu.Lock()
	entries := make([]*entry, 0, len(m.sessions))
	for _, ent := range m.sessions {
		entries = append(entries, ent)
	}
	m.mu.Unlock()

	// Close all sessions concurrently.
	var wg sync.WaitGroup
	for _, ent := range entries {
		wg.Add(1)
		go func(e *entry) {
			defer wg.Done()

			e.mu.Lock()
			sess := e.session
			if e.state.Status != ServerStatusReady {
				e.mu.Unlock()
				return
			}
			e.state.Status = ServerStatusStopped
			e.mu.Unlock()

			if sess != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = sess.Close(ctx)
				cancel()
			}
		}(ent)
	}
	wg.Wait()

	return nil
}

// reapIdle is the ticker goroutine that terminates idle sessions.
func (m *Manager) reapIdle() {
	for {
		select {
		case <-m.reapTicker.C:
			m.doReap()
		case <-m.stopReaper:
			return
		}
	}
}

// doReap closes sessions whose LastUsed exceeds IdleTimeout.
func (m *Manager) doReap() {
	m.mu.Lock()
	now := time.Now()
	idleThreshold := now.Add(-time.Duration(m.cfg.IdleTimeout.Duration()))

	// Snapshot the sessions under ent.mu; reading ent.session without the lock
	// would race with entryFor assigning it after a spawn.
	var toClose []session
	for _, ent := range m.sessions {
		ent.mu.Lock()
		if ent.state.Status == ServerStatusReady && ent.state.LastUsed.Before(idleThreshold) {
			toClose = append(toClose, ent.session)
			ent.state.Status = ServerStatusStopped
		}
		ent.mu.Unlock()
	}
	m.mu.Unlock()

	for _, sess := range toClose {
		if sess != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = sess.Close(ctx)
			cancel()
		}
	}
}
