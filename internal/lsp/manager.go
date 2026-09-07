package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// errNoServer is returned when no enabled server declares the file's extension.
var errNoServer = errors.New("no server for file extension")

// Manager owns the language server processes for a session.
type Manager struct {
	cfg       config.LSPConfig
	workspace string
	wrap      WrapFn
	warnFn    func(string)
	stderr    io.Writer

	mgrCtx    context.Context
	mgrCancel context.CancelFunc

	mu       sync.Mutex
	sessions map[sessionKey]*entry

	reapTicker *time.Ticker
	stopReaper chan struct{}
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
}

// NewManager creates a Manager with the given configuration.
// wrap is optional and transforms the command before launch (e.g. sandbox wrapping).
// warnFn is called on recoverable errors (e.g. spawn failure).
// workspace is the default workspace root when no markers are found.
func NewManager(cfg config.LSPConfig, workspace string, wrap WrapFn, warnFn func(string), stderr io.Writer) *Manager {
	mgrCtx, mgrCancel := context.WithCancel(context.Background())

	m := &Manager{
		cfg:        cfg,
		workspace:  workspace,
		wrap:       wrap,
		warnFn:     warnFn,
		stderr:     stderr,
		mgrCtx:     mgrCtx,
		mgrCancel:  mgrCancel,
		sessions:   make(map[sessionKey]*entry),
		stopReaper: make(chan struct{}),
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

// sessionFor returns or spawns a live session for the given file.
// It returns errNoServer if no enabled server declares the file's extension.
func (m *Manager) sessionFor(ctx context.Context, file string) (session, error) {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	if serverName == "" {
		m.mu.Unlock()
		return nil, errNoServer
	}

	srv := m.cfg.Servers[serverName]
	m.mu.Unlock()

	// Resolve the root.
	root, err := resolveRoot(file, m.workspace, srv.RootMarkers)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

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
			return sess, nil
		case ServerStatusStarting:
			ready := ent.ready
			ent.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case ServerStatusFailed:
			if time.Since(ent.state.StartedAt) < time.Duration(m.cfg.IdleTimeout.Duration()) {
				err := ent.state.Err
				ent.mu.Unlock()
				return nil, err
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
				return nil, err
			}

			ent.state.Status = ServerStatusReady
			ent.session = sess
			r := newReadiness(m.cfg)
			ent.readiness = r
			ent.mu.Unlock()

			go m.trackReadiness(ent, sess, r)

			return sess, nil
		default:
			status := ent.state.Status
			ent.mu.Unlock()
			return nil, fmt.Errorf("unexpected server status: %s", status)
		}
	}
}

// spawnServer spawns a single server process with the configured environment.
func (m *Manager) spawnServer(ctx context.Context, serverName string, srv config.LSPServerConfig, root string) (session, error) {
	// Get cache directory for this root.
	cacheDir, err := cacheDirFor(m.cfg.CacheDir, root)
	if err != nil {
		return nil, fmt.Errorf("cache dir: %w", err)
	}

	// Build environment: start with configured env, add HOME, XDG_CACHE_HOME, GOCACHE.
	env := make([]string, 0, len(srv.Env)+3)

	for k, v := range srv.Env {
		env = append(env, k+"="+v)
	}

	// Add cache-related vars if not already set.
	if _, ok := srv.Env["HOME"]; !ok {
		env = append(env, "HOME="+cacheDir)
	}
	if _, ok := srv.Env["XDG_CACHE_HOME"]; !ok {
		env = append(env, "XDG_CACHE_HOME="+cacheDir)
	}
	if _, ok := srv.Env["GOCACHE"]; !ok {
		env = append(env, "GOCACHE="+filepath.Join(cacheDir, "go"))
	}

	// Create a child context with a handshake timeout, but detached from cancellation.
	spawnCtx, cancel := context.WithTimeout(m.mgrCtx, time.Duration(m.cfg.ReadyTimeout.Duration()))
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

	sess, err := newTransport(spawnCtx, spec)
	if err != nil {
		return nil, fmt.Errorf("spawn transport: %w", err)
	}

	return sess, nil
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
	m.mgrCancel()

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

	var toClose []*entry
	for _, ent := range m.sessions {
		ent.mu.Lock()
		if ent.state.Status == ServerStatusReady && ent.state.LastUsed.Before(idleThreshold) {
			toClose = append(toClose, ent)
			ent.state.Status = ServerStatusStopped
		}
		ent.mu.Unlock()
	}
	m.mu.Unlock()

	for _, ent := range toClose {
		if ent.session != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = ent.session.Close(ctx)
			cancel()
		}
	}
}
