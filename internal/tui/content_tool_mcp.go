package tui

// mcpOrigin reports the MCP origin of a registry tool name, if any. A miss
// means the tool is a built-in, not an MCP tool — this is a single map
// lookup, never a string split.
func (b *contentBuffer) mcpOrigin(tool string) (MCPToolOrigin, bool) {
	origin, ok := b.mcpToolOrigins[tool]
	return origin, ok
}

// toolDisplayTag returns the transcript label for a tool name: "server →
// tool" for an MCP tool, the tool name unchanged for a built-in.
func (b *contentBuffer) toolDisplayTag(tool string) string {
	origin, ok := b.mcpOrigin(tool)
	if !ok {
		return tool
	}
	return origin.Server + " → " + origin.Tool
}
