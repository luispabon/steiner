package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func renderModelBadge(styles *theme.Styles, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	label := styles.FgMute.Render("model ")
	value := lipgloss.NewStyle().Foreground(styles.AccentColor).Render(model)
	return label + value
}

// renderModeBadge renders the execution mode badge with a fixed-width label
// so the status bar cell stays stable across mode switches.
func renderModeBadge(styles *theme.Styles, mode string) string {
	switch strings.TrimSpace(mode) {
	case "plan":
		return styles.ModePlanStyle.Render("plan ")
	case "build":
		return styles.ModeBuildStyle.Render("build")
	default:
		return ""
	}
}

// renderSandboxBadge renders the sandbox status badge for the status bar.
func renderSandboxBadge(styles *theme.Styles, status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	switch status {
	case "active":
		return styles.Added.Render("sbx active")
	case "unavailable":
		return styles.Warn.Render("sbx unavailable")
	case "bypassed":
		return styles.Removed.Render("sbx bypassed")
	default:
		return styles.FgDim.Render("sbx " + status)
	}
}
