package tui

import "fmt"

// mcpRow renders the sidebar's MCP health value ("2/3"), or "" when there is
// nothing to report (MCP off, or no servers configured/enabled). The caller
// applies styling.
func (s sidebarState) mcpRow() string {
	if s.mcpTotal == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", s.mcpConnected, s.mcpTotal)
}
