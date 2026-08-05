package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// Manager owns the MCP server sessions for one steiner session.
type Manager struct {
	sessions []*Session
	defs     []tool.ToolDef
}

// Connect dials every enabled server in cfg sequentially and returns a Manager.
// Servers that fail to connect are reported through onWarn and omitted; Connect
// itself returns an error only for programming faults, never for a server failure.
func Connect(ctx context.Context, cfg config.MCPConfig, wrap func(*exec.Cmd) *exec.Cmd, approver tool.ApprovalResponder, onWarn func(string), stderr io.Writer) *Manager {
	m := &Manager{}

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

		// Log the negotiated protocol version.
		onWarn(fmt.Sprintf("MCP server %q connected (protocol %s, %d tools)", name, session.ProtocolVersion(), len(session.Tools())))

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
