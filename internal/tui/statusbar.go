package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type statusState struct {
	model          string
	context        string
	mode           string
	styles         theme.Styles
	streaming      bool
	approvalActive bool
	promptUsed     int
	contextBudget  int
}

func (s statusState) view(width int) string {
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(" │ ")

	var parts []string

	// Segment 1: model (stable left)
	if s.model != "" {
		label := s.styles.FgMute.Render("model ")
		val := lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(s.model)
		parts = append(parts, label+val)
	}

	// Segments 2-5: stable commands and navigation
	parts = append(parts, s.styles.KeyChip.Render("^P")+" commands")
	parts = append(parts, s.styles.KeyChip.Render("^B")+" sidebar")
	parts = append(parts, s.styles.KeyChip.Render("/model")+" switch")
	parts = append(parts, s.styles.KeyChip.Render("?")+" help")

	// Segment 6: ctx (static, infrequently changing)
	if s.contextBudget > 0 || s.promptUsed > 0 {
		pct := 0
		if s.contextBudget > 0 {
			pct = (s.promptUsed * 100) / s.contextBudget
		}
		var ctxColor lipgloss.Color
		switch {
		case pct > 90:
			ctxColor = lipgloss.Color(theme.Removed)
		case pct > 70:
			ctxColor = lipgloss.Color(theme.Warn)
		default:
			ctxColor = s.styles.AccentColor
		}
		ctxStr := fmt.Sprintf("%d/%d · %d%%", s.promptUsed, s.contextBudget, pct)
		label := s.styles.FgMute.Render("ctx ")
		val := lipgloss.NewStyle().Foreground(ctxColor).Render(ctxStr)
		parts = append(parts, label+val)
	}

	// Segment 8: transient action hints (very end)
	if s.approvalActive {
		parts = append(parts, s.styles.KeyChip.Render("tab")+" choice")
		parts = append(parts, s.styles.KeyChip.Render("⏎")+" confirm")
		parts = append(parts, s.styles.KeyChip.Render("esc")+" deny")
	}

	text := strings.Join(parts, sep)
	if width > 0 && lipgloss.Width(text) > width {
		for len(parts) > 1 {
			parts = parts[:len(parts)-1]
			text = strings.Join(parts, sep)
			if lipgloss.Width(text) <= width {
				break
			}
		}
	}
	if width > 0 {
		return theme.WithBg(s.styles.StatusBar.Width(width).Render(text), lipgloss.Color(theme.BgElev))
	}
	return theme.WithBg(s.styles.StatusBar.Render(text), lipgloss.Color(theme.BgElev))
}
