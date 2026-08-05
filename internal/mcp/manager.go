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
	sessions []*Session
	defs     []tool.ToolDef
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
func Connect(ctx context.Context, cfg config.MCPConfig, wrap func(*exec.Cmd) *exec.Cmd, approver tool.ApprovalResponder, onWarn, onInfo func(string), stderr io.Writer) *Manager {
	m := &Manager{}

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
		if !srv.Enabled {
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
			continue
		}

		// A successful connect is informational, not a warning.
		onInfo(fmt.Sprintf("MCP server %q connected (protocol %s, %d tools)", name, session.ProtocolVersion(), len(session.Tools())))

		m.sessions = append(m.sessions, session)

		// Build ToolDefs for this session.
		for _, t := range session.Tools() {
			def := mcpToolDef(session, t, approver)
			m.defs = append(m.defs, def)
		}
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

// UpdateApprover rebuilds tool definitions with a new approver.
// Call this after the approver becomes available (e.g. after interactive
// session construction) so MCP tools can be invoked. Safe to call with nil.
func (m *Manager) UpdateApprover(approver tool.ApprovalResponder) {
	if m == nil {
		return
	}
	m.defs = nil
	for _, s := range m.sessions {
		for _, t := range s.Tools() {
			def := mcpToolDef(s, t, approver)
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
