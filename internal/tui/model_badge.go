package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func renderModelBadge(styles theme.Styles, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	label := styles.FgMute.Render("model ")
	value := lipgloss.NewStyle().Foreground(styles.AccentColor).Render(model)
	return label + value
}
