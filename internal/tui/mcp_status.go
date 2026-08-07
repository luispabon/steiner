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
	// Tools lists every tool this server advertised, in advertised order, with
	// its access outcome; connected only, may be empty.
	Tools []MCPToolStatus
	// Error is the failure text; failed only.
	Error string
}

// MCPToolStatus is the TUI's display-only view of one advertised MCP tool and
// its access outcome.
type MCPToolStatus struct {
	// Name is the MCP-native advertised tool name.
	Name string
	// Outcome is "registered", "filtered" or "denied".
	Outcome string
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
		tools := make([]MCPToolStatus, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, MCPToolStatus{Name: t.Name, Outcome: t.Outcome})
		}
		servers = append(servers, MCPServerStatus{
			Name:      name,
			State:     s.State,
			Transport: s.Transport,
			Tools:     tools,
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
