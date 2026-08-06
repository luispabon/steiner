package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// WrapFn wraps a server command before launch, e.g. inside the sandbox.
type WrapFn func(*exec.Cmd) *exec.Cmd

// Manager owns the MCP server sessions for one steiner session.
type Manager struct {
	mu       sync.Mutex
	sessions []*Session
	defs     []tool.ToolDef
	states   []ServerState
	planMode bool
	approver tool.ApprovalResponder

	ctx    context.Context
	cancel context.CancelFunc

	initOnce  sync.Once
	initDone  chan struct{}
	connectWG sync.WaitGroup
}

// syncWriter serialises writes from the per-process stderr copier goroutines
// that exec starts, so one shared writer can back every connected server.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Connect starts a connection attempt for every enabled server in cfg and
// returns a Manager immediately. Each enabled server connects on its own
// goroutine, bounded by its per-server ConnectTimeout, so startup latency is
// bounded by the slowest server rather than the sum. Call WaitInit before
// consuming ToolDefs or ServerStates if the resolved (connected/failed) view
// is required; meanwhile ServerStates reports connecting entries.
//
// The reporting channels are distinct so callers can pick a severity per
// channel: warnFn receives connection failures, infoFn receives successful
// connects and their negotiated protocol version. Both are optional; a nil
// callback discards its messages. onStateChange, also optional, is invoked
// after each recorded state transition.
//
// stderr is not a reporting channel: it becomes each server process's own
// stderr, which exec copies into from a goroutine per process. Every connected
// server therefore writes to it concurrently for the whole session, so Connect
// serialises those writes and callers may pass an ordinary unsynchronised
// writer. Connect never writes to it directly.
//
// Connect takes no approver: tool definitions resolve the approver lazily via
// the Manager at call time, so definitions created before UpdateApprover still
// work once the approver is installed. limits is reserved for later lifecycle
// steps that bound MCP tool output and reconnect policy.
//
// Connect never returns an error for a server failure; a failed server is
// simply marked failed.
func Connect(ctx context.Context, cfg config.MCPConfig, limits config.LimitsConfig, wrap WrapFn, planMode bool, warnFn, infoFn func(string), stderr io.Writer, onStateChange func()) *Manager {
	_ = limits // reserved for later lifecycle steps (tool output bounds, reconnect policy)

	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{
		planMode: planMode,
		ctx:      ctx,
		cancel:   cancel,
		initDone: make(chan struct{}),
	}

	if warnFn == nil {
		warnFn = func(string) {}
	}
	if infoFn == nil {
		infoFn = func(string) {}
	}
	if onStateChange == nil {
		onStateChange = func() {}
	}
	if stderr != nil {
		stderr = &syncWriter{w: stderr}
	}

	if !cfg.Enabled {
		m.states = DeclaredStates(cfg)
		return m
	}

	// Iterate servers in sorted key order for determinism.
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	// Pre-record every configured server in sorted order so ServerStates is
	// stable: enabled servers start as connecting and their goroutine later
	// transitions the same entry to connected or failed in place.
	for _, name := range names {
		srv := cfg.Servers[name]
		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}
		if !srv.Enabled {
			m.setServerState(ServerState{Name: name, Status: ServerStatusDisabled, Transport: transport})
			continue
		}
		m.setServerState(ServerState{Name: name, Status: ServerStatusConnecting, Transport: transport})
	}

	for _, name := range names {
		srv := cfg.Servers[name]
		if !srv.Enabled {
			continue
		}
		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}
		spec := ServerSpec{
			Name:      name,
			Transport: transport,
			Command:   srv.Command,
			Args:      srv.Args,
			Env:       srv.Env,
			URL:       srv.URL,
			Headers:   srv.Headers,
		}
		m.connectWG.Add(1)
		go m.connectServer(spec, srv, transport, wrap, warnFn, infoFn, stderr, onStateChange)
	}

	return m
}

// connectServer runs one server's connection attempt: handshake, tool list,
// and def creation, all bounded by the server's ConnectTimeout, then records
// the final state under the manager mutex and reports the transition.
func (m *Manager) connectServer(spec ServerSpec, srv config.MCPServerConfig, transport string, wrap WrapFn, warnFn, infoFn func(string), stderr io.Writer, onStateChange func()) {
	defer m.connectWG.Done()

	timeout := time.Duration(srv.ConnectTimeout.Duration())
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	// ConnectSession applies the per-server timeout to the handshake and tool
	// list internally. The manager's context is passed through, not the
	// timeout-bounded one, because the stdio transport binds the server process
	// to the context it was started with: cancelling it after a successful
	// connect would kill the session.
	session, err := ConnectSession(m.ctx, spec, wrap, stderr, timeout)
	if err != nil {
		warnFn(fmt.Sprintf("MCP server %q failed to connect: %v", spec.Name, err))
		m.setServerState(ServerState{Name: spec.Name, Status: ServerStatusFailed, Transport: transport, Err: err.Error()})
		onStateChange()
		return
	}

	// A successful connect is informational, not a warning.
	infoFn(fmt.Sprintf("MCP server %q connected (protocol %s, %d tools)", spec.Name, session.ProtocolVersion(), len(session.Tools())))

	// Build ToolDefs for this session. Deny servers still connect (their
	// state stays visible) but register no tools.
	var toolNames []string
	for _, t := range session.Tools() {
		if srv.Approval == "deny" {
			continue
		}
		def := mcpToolDef(session, t, func() tool.ApprovalResponder { return m.currentApprover() }, func() bool { return m.planMode }, srv)
		m.mu.Lock()
		m.defs = append(m.defs, def)
		m.mu.Unlock()
		toolNames = append(toolNames, t.Name)
	}

	m.mu.Lock()
	m.sessions = append(m.sessions, session)
	m.mu.Unlock()

	m.setServerState(ServerState{
		Name:            spec.Name,
		Status:          ServerStatusConnected,
		Transport:       transport,
		Tools:           toolNames,
		ProtocolVersion: session.ProtocolVersion(),
	})
	onStateChange()
}

// setServerState records s under the manager mutex, replacing any earlier
// entry for the same server name.
func (m *Manager) setServerState(s ServerState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.states {
		if m.states[i].Name == s.Name {
			m.states[i] = s
			return
		}
	}
	m.states = append(m.states, s)
}

// currentApprover returns the manager's current approver under the mutex.
func (m *Manager) currentApprover() tool.ApprovalResponder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.approver
}

// WaitInit blocks until every enabled server has resolved to connected or
// failed, or until ctx is done. It is idempotent and safe to call from
// multiple goroutines: the first call arms the done channel, later calls just
// wait on it. Safe to call on a nil Manager.
func (m *Manager) WaitInit(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.initOnce.Do(func() {
		go func() {
			m.connectWG.Wait()
			close(m.initDone)
		}()
	})
	select {
	case <-m.initDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ToolDefs returns the registry definitions for every connected server.
// It is idempotent and performs no I/O: the tool list is frozen at connect
// time, so callers may rebuild the registry any number of times. Safe to call
// on a nil Manager.
func (m *Manager) ToolDefs() []tool.ToolDef {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.defs
}

// ServerStates returns a deep copy of the live state for every configured
// server, in sorted name order. Enabled servers appear as connecting while
// their attempt is in flight and resolve to connected or failed. Safe to call
// on a nil Manager.
func (m *Manager) ServerStates() []ServerState {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerState, len(m.states))
	for i, s := range m.states {
		out[i] = s
		out[i].Tools = append([]string(nil), s.Tools...)
	}
	return out
}

// PlanMode returns whether MCP tools currently run in plan mode.
// Safe to call on a nil Manager.
func (m *Manager) PlanMode() bool {
	if m == nil {
		return false
	}
	return m.planMode
}

// UpdateApprover sets the approver that MCP tool handlers resolve lazily at
// call time. Call this after the approver becomes available (e.g. after
// interactive session construction) so MCP tools can be invoked; definitions
// created at connect time pick it up without rebuilding. Safe to call with
// nil.
func (m *Manager) UpdateApprover(approver tool.ApprovalResponder) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.approver = approver
	m.mu.Unlock()
}

// UpdatePlanMode sets the plan mode read by the handler closures at call
// time. Tool definitions are not rebuilt: each handler reads the current mode
// from the closure. Safe to call with nil.
func (m *Manager) UpdatePlanMode(planMode bool) {
	if m == nil {
		return
	}
	m.planMode = planMode
}

// Close terminates every connected server and cancels in-flight connects.
// Sessions are closed first (graceful stdin-close on stdio transports), then
// the manager context is cancelled to abort any connect still running. Safe to
// call on a nil Manager.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()
	var firstErr error
	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.cancel()
	return firstErr
}
