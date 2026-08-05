package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// mcpSection renders the sidebar's MCP health row, or nil when there is
// nothing to report (MCP off, or no servers configured/enabled).
func (s sidebarState) mcpSection(_ int) []string {
	if s.mcpTotal == 0 {
		return nil
	}
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	if s.mcpFailed {
		rowStyle = s.styles.ErrorStyle
	}
	return []string{s.styledWithBg(rowStyle, fmt.Sprintf("MCP %d/%d", s.mcpConnected, s.mcpTotal))}
}
