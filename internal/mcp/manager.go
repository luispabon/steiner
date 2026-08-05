package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// Manager owns the MCP server sessions for one steiner session.
type Manager struct {
	sessions      []*Session
	defs          []tool.ToolDef
	states        []ServerState
	serverConfigs map[string]config.MCPServerConfig
	planMode      bool
	approver      tool.ApprovalResponder
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

// Connect dials every enabled server in cfg sequentially and returns a Manager.
// The reporting channels are distinct so callers can pick a severity per
// channel: onWarn receives connection failures, onInfo receives successful
// connects and their negotiated protocol version. Both are optional; a nil
// callback discards its messages.
//
// stderr is not a reporting channel: it becomes each server process's own
// stderr, which exec copies into from a goroutine per process. Every connected
// server therefore writes to it concurrently for the whole session, so Connect
// serialises those writes and callers may pass an ordinary unsynchronised
// writer. Connect never writes to it directly.
//
// Connect never returns an error for a server failure; a failed server is
// simply omitted.
func Connect(ctx context.Context, cfg config.MCPConfig, wrap func(*exec.Cmd) *exec.Cmd, approver tool.ApprovalResponder, onWarn, onInfo func(string), stderr io.Writer, planMode bool) *Manager {
	m := &Manager{planMode: planMode, approver: approver, serverConfigs: map[string]config.MCPServerConfig{}}

	if onWarn == nil {
		onWarn = func(string) {}
	}
	if onInfo == nil {
		onInfo = func(string) {}
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

	for _, name := range names {
		srv := cfg.Servers[name]

		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}

		if !srv.Enabled {
			m.states = append(m.states, ServerState{Name: name, Status: ServerStatusDisabled, Transport: transport})
			continue
		}

		spec := ServerSpec{
			Name:    name,
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}

		session, err := ConnectSession(ctx, spec, wrap, stderr)
		if err != nil {
			onWarn(fmt.Sprintf("MCP server %q failed to connect: %v", name, err))
			m.states = append(m.states, ServerState{Name: name, Status: ServerStatusFailed, Transport: transport, Err: err.Error()})
			continue
		}

		// A successful connect is informational, not a warning.
		onInfo(fmt.Sprintf("MCP server %q connected (protocol %s, %d tools)", name, session.ProtocolVersion(), len(session.Tools())))

		m.sessions = append(m.sessions, session)
		m.serverConfigs[name] = srv

		// Build ToolDefs for this session. Deny servers still connect (their
		// state stays visible) but register no tools.
		var toolNames []string
		for _, t := range session.Tools() {
			if srv.Approval == "deny" {
				continue
			}
			def := mcpToolDef(session, t, approver, m.planMode, srv)
			m.defs = append(m.defs, def)
			toolNames = append(toolNames, t.Name)
		}
		m.states = append(m.states, ServerState{
			Name:            name,
			Status:          ServerStatusConnected,
			Transport:       transport,
			Tools:           toolNames,
			ProtocolVersion: session.ProtocolVersion(),
		})
	}

	return m
}

// ToolDefs returns the registry definitions for every connected server.
// It is idempotent and performs no I/O: the tool list is frozen at connect time,
// so callers may rebuild the registry any number of times.
func (m *Manager) ToolDefs() []tool.ToolDef {
	if m == nil {
		return nil
	}
	return m.defs
}

// ServerStates returns the recorded connection outcome for every configured
// server, in sorted name order. Safe to call on a nil Manager.
func (m *Manager) ServerStates() []ServerState {
	if m == nil {
		return nil
	}
	return m.states
}

// PlanMode returns whether MCP tools currently run in plan mode.
// Safe to call on a nil Manager.
func (m *Manager) PlanMode() bool {
	if m == nil {
		return false
	}
	return m.planMode
}

// UpdateApprover rebuilds tool definitions with a new approver.
// Call this after the approver becomes available (e.g. after interactive
// session construction) so MCP tools can be invoked. Safe to call with nil.
func (m *Manager) UpdateApprover(approver tool.ApprovalResponder) {
	if m == nil {
		return
	}
	m.approver = approver
	m.defs = nil
	for _, s := range m.sessions {
		srv := m.serverConfigs[s.Name()]
		if srv.Approval == "deny" {
			continue
		}
		for _, t := range s.Tools() {
			def := mcpToolDef(s, t, approver, m.planMode, srv)
			m.defs = append(m.defs, def)
		}
	}
}

// UpdatePlanMode rebuilds tool definitions with a new plan mode so handler
// closures see the current mode. The approver is taken from the last
// UpdateApprover call (or Connect). Safe to call with nil.
func (m *Manager) UpdatePlanMode(planMode bool) {
	if m == nil {
		return
	}
	m.planMode = planMode
	m.defs = nil
	for _, s := range m.sessions {
		srv := m.serverConfigs[s.Name()]
		if srv.Approval == "deny" {
			continue
		}
		for _, t := range s.Tools() {
			def := mcpToolDef(s, t, m.approver, m.planMode, srv)
			m.defs = append(m.defs, def)
		}
	}
}

// Close terminates every connected server. Safe to call on a nil Manager.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	var firstErr error
	for _, s := range m.sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
