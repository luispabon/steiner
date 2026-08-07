package tui

import (
	"sort"

	"github.com/luispabon/steiner/internal/output"
)

// MCPServerStatus is the TUI's display-only view of one configured MCP server.
type MCPServerStatus struct {
	// Name is the server's config key.
	Name string
	// State is the server's connection outcome: "connected", "failed" or "disabled".
	State string
	// Transport is the server's transport, e.g. "stdio".
	Transport string
	// Tools lists the MCP-side tool names offered by this server; connected only, may be empty.
	Tools []string
	// Error is the failure text; failed only.
	Error string
}

// MCPToolOrigin identifies the MCP server a registry tool name came from.
type MCPToolOrigin struct {
	// Server is the config key of the MCP server that provided the tool.
	Server string
	// Tool is the original, unsanitised tool name as the server reported it.
	Tool string
}

// mcpServersFromStatusEvent converts an MCPStatusEvent server map into the
// display slice, sorted by server name so the sidebar and /mcp overlay keep a
// stable ordering across refreshes.
func mcpServersFromStatusEvent(states map[string]output.MCPServerState) []MCPServerStatus {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]MCPServerStatus, 0, len(names))
	for _, name := range names {
		s := states[name]
		servers = append(servers, MCPServerStatus{
			Name:      name,
			State:     s.State,
			Transport: s.Transport,
			Tools:     append([]string(nil), s.Tools...),
			Error:     s.Error,
		})
	}
	return servers
}

// mcpToolOriginsFromStatusEvent converts an MCPStatusEvent origin map into the
// TUI's display type.
func mcpToolOriginsFromStatusEvent(origins map[string]output.MCPToolOrigin) map[string]MCPToolOrigin {
	if len(origins) == 0 {
		return nil
	}
	out := make(map[string]MCPToolOrigin, len(origins))
	for name, o := range origins {
		out[name] = MCPToolOrigin{Server: o.Server, Tool: o.Tool}
	}
	return out
}
