package tui

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
