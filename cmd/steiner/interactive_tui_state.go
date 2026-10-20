package main

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/lsp"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

// mcpTUIState converts MCP manager state and registry tool provenance into
// the TUI's display-only snapshot: whether MCP is enabled, the per-server
// states, and the tool-name-to-origin map.
func mcpTUIState(cfg config.Config, mgr *mcp.Manager, registry *tool.Registry) (bool, []tui.MCPServerStatus, map[string]tui.MCPToolOrigin) {
	enabled := cfg.MCP.Enabled

	states := mgr.ServerStates() // nil-safe: nil when mgr is nil (MCP off, no Manager was constructed)
	if !enabled {
		states = mcp.DeclaredStates(cfg.MCP) // MCP off: no Manager exists
	}

	servers := make([]tui.MCPServerStatus, 0, len(states))
	for _, s := range states {
		tools := make([]tui.MCPToolStatus, 0, len(s.AdvertisedTools))
		for _, t := range s.AdvertisedTools {
			tools = append(tools, tui.MCPToolStatus{Name: t.Name, Outcome: mcpOutcomeLabel(t.Outcome)})
		}
		servers = append(servers, tui.MCPServerStatus{
			Name:      s.Name,
			State:     string(s.Status),
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Err,
		})
	}

	var origins map[string]tui.MCPToolOrigin
	for _, def := range registry.Definitions() {
		if def.MCP.Server == "" {
			continue
		}
		if origins == nil {
			origins = make(map[string]tui.MCPToolOrigin)
		}
		origins[def.Name] = tui.MCPToolOrigin{Server: def.MCP.Server, Tool: def.MCP.ToolName}
	}

	return enabled, servers, origins
}

// lspTUIStates converts internal/lsp's live server states plus declared
// config into the TUI's display-only snapshot. Manager.ServerStates() only
// has an entry for a server once a request has touched it, so servers
// declared in config but never touched get a synthesized "not started" row,
// mirroring mcp.DeclaredStates for the analogous MCP case. The TUI sorts on
// every consumption path, so the order returned here is not significant.
func lspTUIStates(cfg config.Config, mgr *lsp.Manager) []tui.LSPServerStatus {
	var live []lsp.ServerState
	if mgr != nil {
		live = mgr.ServerStates()
	}
	return lspTUIStatesFrom(cfg, live)
}

// lspTUIStatesFrom is the pure conversion lspTUIStates delegates to, split
// out so the config/live-state merge is testable without a real Manager.
func lspTUIStatesFrom(cfg config.Config, live []lsp.ServerState) []tui.LSPServerStatus {
	touched := make(map[string]bool, len(live))
	states := make([]tui.LSPServerStatus, 0, len(live)+len(cfg.LSP.Servers))
	for _, s := range live {
		touched[s.Name] = true
		errText := ""
		if s.Err != nil {
			errText = s.Err.Error()
		}
		states = append(states, tui.LSPServerStatus{
			Name:      s.Name,
			Root:      s.Root,
			Status:    string(s.Status),
			Error:     errText,
			StartedAt: s.StartedAt,
			LastUsed:  s.LastUsed,
		})
	}

	for name, srv := range cfg.LSP.Servers {
		if touched[name] {
			continue
		}
		status := "not started"
		if !srv.Enabled {
			status = "disabled"
		}
		states = append(states, tui.LSPServerStatus{Name: name, Status: status})
	}

	return states
}

// mcpOutcomeLabel maps a manager ToolOutcome to the display label the TUI and
// status snapshots carry: "registered", "filtered" or "denied".
func mcpOutcomeLabel(o mcp.ToolOutcome) string {
	switch o {
	case mcp.ToolFiltered:
		return "filtered"
	case mcp.ToolDenied:
		return "denied"
	default:
		return "registered"
	}
}
