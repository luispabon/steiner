package output

// MCPServerState is the display-only view of one configured MCP server inside
// an MCPStatusEvent. The map key in MCPStatusEvent.Servers is the server's
// config key.
type MCPServerState struct {
	// State is the server's connection outcome: "connecting", "connected",
	// "reconnecting", "failed", "unavailable" or "disabled".
	State string `json:"state"`
	// Transport is the server's transport, e.g. "stdio".
	Transport string `json:"transport,omitempty"`
	// Tools lists every tool this server advertised, in advertised order, with
	// its access outcome; connected only, may be empty.
	Tools []MCPAdvertisedTool `json:"tools,omitempty"`
	// Error is the failure text; failed/unavailable only.
	Error string `json:"error,omitempty"`
}

// MCPAdvertisedTool is the display-only view of one tool a connected MCP server
// advertised, with its access outcome after filtering or approval denial.
type MCPAdvertisedTool struct {
	// Name is the MCP-native advertised tool name.
	Name string `json:"name"`
	// Outcome is "registered", "filtered" or "denied".
	Outcome string `json:"outcome"`
}

// MCPToolOrigin identifies the MCP server a registry tool name came from.
type MCPToolOrigin struct {
	// Server is the config key of the MCP server that provided the tool.
	Server string `json:"server"`
	// Tool is the original, unsanitised tool name as the server reported it.
	Tool string `json:"tool"`
}

// MCPStatusEvent is the payload for EventTypeMCPStatus: an immutable snapshot
// of the MCP surface (whether MCP is enabled, every configured server's live
// state, and the registry's MCP tool origins) that the TUI can rebuild its MCP
// display from without touching the manager or registry.
type MCPStatusEvent struct {
	Enabled bool                      `json:"enabled"`
	Servers map[string]MCPServerState `json:"servers,omitempty"`
	Origins map[string]MCPToolOrigin  `json:"origins,omitempty"`
}

// NewMCPStatusEvent creates an mcp_status snapshot event carrying an immutable
// view of the MCP surface: whether MCP is enabled, every configured server's
// live state keyed by server name, and the registry's MCP tool origins.
func NewMCPStatusEvent(enabled bool, servers map[string]MCPServerState, origins map[string]MCPToolOrigin) Event {
	return newEvent(EventTypeMCPStatus, MCPStatusEvent{
		Enabled: enabled,
		Servers: cloneMCPServerStates(servers),
		Origins: cloneMCPToolOrigins(origins),
	})
}

// cloneMCPServerStates copies the server map and each server's advertised-tool
// slice so the emitted event owns its payload: callers build the snapshot from
// live manager state and may mutate their own maps or slices afterwards.
func cloneMCPServerStates(servers map[string]MCPServerState) map[string]MCPServerState {
	if servers == nil {
		return nil
	}
	cloned := make(map[string]MCPServerState, len(servers))
	for name, state := range servers {
		if state.Tools != nil {
			state.Tools = append([]MCPAdvertisedTool(nil), state.Tools...)
		}
		cloned[name] = state
	}
	return cloned
}

// cloneMCPToolOrigins copies the tool-origin map so the emitted event does not
// alias the caller's map.
func cloneMCPToolOrigins(origins map[string]MCPToolOrigin) map[string]MCPToolOrigin {
	if origins == nil {
		return nil
	}
	cloned := make(map[string]MCPToolOrigin, len(origins))
	for name, origin := range origins {
		cloned[name] = origin
	}
	return cloned
}
