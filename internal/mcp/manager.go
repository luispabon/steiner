package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// WrapFn wraps a server command before launch, e.g. inside the sandbox.
type WrapFn func(*exec.Cmd) *exec.Cmd

// shutdownGrace bounds how long Close waits for each session to tear down. It
// must exceed the stdio transport's TerminateDuration (5s) plus a margin, so a
// server that needs the full SIGTERM wait still finishes inside the grace;
// servers whose process group the shutdown SIGKILLed finish far sooner.
const shutdownGrace = 10 * time.Second

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

	// closeOnce serialises Close: the first call runs the shutdown, later
	// calls (concurrent or repeated) return its result.
	closeOnce sync.Once
	closeErr  error
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
// work once the approver is installed. limits is captured at connect time and
// bounds each MCP tool call's output and duration at call time (D19).
//
// Connect never returns an error for a server failure; a failed server is
// simply marked failed.
func Connect(ctx context.Context, cfg config.MCPConfig, limits config.LimitsConfig, wrap WrapFn, planMode bool, warnFn, infoFn func(string), stderr io.Writer, onStateChange func()) *Manager {
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
		go m.connectServer(spec, srv, transport, limits, wrap, warnFn, infoFn, stderr, onStateChange)
	}

	return m
}

// connectServer runs one server's connection attempt: handshake, tool list,
// and def creation, all bounded by the server's ConnectTimeout, then records
// the final state under the manager mutex and reports the transition. limits is
// captured into each tool definition so per-call output bounds and timeouts
// apply (D19).
func (m *Manager) connectServer(spec ServerSpec, srv config.MCPServerConfig, transport string, limits config.LimitsConfig, wrap WrapFn, warnFn, infoFn func(string), stderr io.Writer, onStateChange func()) {
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
	session, err := ConnectSession(m.ctx, spec, wrap, stderr, timeout, SessionOptions{
		ManagerCtx: m.ctx,
		OnStatus: func(status ServerStatus, reconnectErr error) {
			// Reconnect lifecycle: connected→reconnecting→connected/unavailable.
			// Runs on the session's reconnect worker goroutine.
			m.updateServerStatus(spec.Name, status, reconnectErr, onStateChange)
		},
	})
	if err != nil {
		warnFn(fmt.Sprintf("MCP server %q failed to connect: %v", spec.Name, err))
		m.setServerState(ServerState{Name: spec.Name, Status: ServerStatusFailed, Transport: transport, Err: err.Error()})
		onStateChange()
		return
	}

	// A successful connect is informational, not a warning.
	infoFn(fmt.Sprintf("MCP server %q connected (protocol %s, %d tools)", spec.Name, session.ProtocolVersion(), len(session.Tools())))

	// Publish the session, its tool defs, and the connected state in one
	// critical section guarded by the manager context (Close cancels it under
	// the same lock): a dial that completed concurrently with Close must not
	// swap a new session into a closing manager. The fresh process is bound to
	// the cancelled manager context, so it is already being killed; close the
	// handle so it cannot leak. Deny servers still connect (their state stays
	// visible) but register no tools.
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		_ = session.Close() // best-effort teardown of a session born during Close
		return
	}
	var toolNames []string
	for _, t := range session.Tools() {
		if srv.Approval == "deny" {
			continue
		}
		def := mcpToolDef(session, t, func() tool.ApprovalResponder { return m.currentApprover() }, func() bool { return m.planMode }, srv, limits)
		m.defs = append(m.defs, def)
		toolNames = append(toolNames, t.Name)
	}
	m.sessions = append(m.sessions, session)
	m.setServerStateLocked(ServerState{
		Name:            spec.Name,
		Status:          ServerStatusConnected,
		Transport:       transport,
		Tools:           toolNames,
		ProtocolVersion: session.ProtocolVersion(),
	})
	m.mu.Unlock()
	onStateChange()
}

// updateServerStatus transitions one server's status in place, preserving its
// other state fields (tools, protocol version, transport). onStateChange fires
// only when the status actually changed, so repeated reports of the same status
// emit nothing.
func (m *Manager) updateServerStatus(name string, status ServerStatus, err error, onStateChange func()) {
	m.mu.Lock()
	for i := range m.states {
		if m.states[i].Name == name {
			if m.states[i].Status == status {
				m.mu.Unlock()
				return
			}
			m.states[i].Status = status
			m.states[i].Err = ""
			if err != nil {
				m.states[i].Err = err.Error()
			}
			m.mu.Unlock()
			onStateChange()
			return
		}
	}
	m.mu.Unlock()
}

// setServerState records s under the manager mutex, replacing any earlier
// entry for the same server name.
func (m *Manager) setServerState(s ServerState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setServerStateLocked(s)
}

// setServerStateLocked records s, replacing any earlier entry for the same
// server name. Callers must hold m.mu.
func (m *Manager) setServerStateLocked(s ServerState) {
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

// Close terminates every connected server and cancels in-flight connects. It
// cancels the manager context first so connect goroutines, reconnect workers,
// and their bound stdio process groups observe the shutdown, then closes each
// session concurrently bounded by shutdownGrace. Expected shutdown errors
// (io.EOF, context.Canceled, reaped "signal: killed" processes) are filtered;
// unexpected ones are joined. Close is idempotent and safe to call on a nil
// Manager.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.closeErr = m.close()
	})
	return m.closeErr
}

// close runs the actual shutdown once. The manager context is cancelled before
// any session is closed: every stdio process group bound to it is SIGKILLed
// immediately, so a server that ignores SIGTERM cannot stall the shutdown, and
// a connect goroutine whose dial completed concurrently is rejected by the
// generation guard in connectServer.
func (m *Manager) close() error {
	m.cancel()

	m.mu.Lock()
	sessions := append([]*Session(nil), m.sessions...)
	m.mu.Unlock()

	// The shutdown context is derived from a detached manager context: the
	// manager context itself is already cancelled, so WithoutCancel gives the
	// per-session closes a fresh deadline to race. One stuck server therefore
	// cannot stall the rest, and Close never waits beyond shutdownGrace.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(m.ctx), shutdownGrace)
	defer shutdownCancel()

	errCh := make(chan error, len(sessions))
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			done := make(chan error, 1)
			go func() { done <- s.Close() }()
			select {
			case err := <-done:
				errCh <- err
			case <-shutdownCtx.Done():
				// The session did not finish within the grace; stop waiting for
				// it (its goroutine keeps tearing down in the background).
				errCh <- shutdownCtx.Err()
			}
		}(s)
	}
	wg.Wait()
	close(errCh)

	var unexpected []error
	for err := range errCh {
		if isExpectedShutdownErr(err) {
			continue
		}
		unexpected = append(unexpected, err)
	}
	switch len(unexpected) {
	case 0:
		return nil
	case 1:
		return unexpected[0]
	default:
		return errors.Join(unexpected...)
	}
}

// isExpectedShutdownErr reports whether err is a normal shutdown outcome:
// io.EOF from a server that exited on stdin close, context.Canceled from the
// cancelled manager context, "signal: killed" from reaping a process group
// the shutdown SIGKILLed (exec.Cmd WaitError), or os.ErrProcessDone / "no such
// process" from exec.Cmd.Cancel racing a process that exited on its own during
// concurrent shutdown. Anything else is unexpected and must surface.
func isExpectedShutdownErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrProcessDone) {
		return true
	}
	return strings.Contains(err.Error(), "signal: killed") || strings.Contains(err.Error(), "no such process")
}
