package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// mcpRow renders the sidebar's MCP health value, or "" when there is
// nothing to report (MCP off, or no servers configured/enabled).
func (s sidebarState) mcpRow(_ int) string {
	if s.mcpTotal == 0 {
		return ""
	}
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	if s.mcpFailed {
		rowStyle = s.styles.ErrorStyle
	}
	return s.styledWithBg(rowStyle, fmt.Sprintf("%d/%d", s.mcpConnected, s.mcpTotal))
}
