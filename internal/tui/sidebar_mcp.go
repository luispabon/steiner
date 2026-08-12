package tui

import "fmt"

// mcpRow renders the sidebar's MCP status as two unstyled pieces: a spinner
// frame (empty when settled, spinning frame when connecting) and a count
// ("N/M" format). Returns ("", "") when MCP is off or no servers configured.
// The caller applies styling to each piece.
func (s sidebarState) mcpRow() (spinner, count string) {
	if s.mcpTotal == 0 {
		return "", ""
	}
	count = fmt.Sprintf("%d/%d", s.mcpConnected, s.mcpTotal)
	if s.mcpConnecting {
		spinner = spinnerFrames[s.tickCount%len(spinnerFrames)]
	}
	return spinner, count
}
