package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (s sidebarState) brandLines(width int) []string {
	logo := []string{
		"▄▖▗   ▘",
		"▚ ▜▘█▌▌▛▌█▌▛▘",
		"▄▌▐▖▙▖▌▌▌▙▖▌",
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	accentFg := s.styles.Accent.Copy().Background(lipgloss.Color(theme.Black))
	out := make([]string, 0, len(logo))
	for i, line := range logo {
		if i < 2 {
			out = append(out, accentFg.Render(line))
			continue
		}
		out = append(out, bg.Render(line))
	}

	ver := s.styles.FgMute.Copy().Background(lipgloss.Color(theme.Black)).Render(s.version)
	third := out[2] + bg.Render(" ") + ver
	if pad := width - lipgloss.Width(third); pad > 0 {
		third += bg.Render(strings.Repeat(" ", pad))
	}
	out[2] = third
	return out
}

func cardLabel(label string, styles theme.Styles) string {
	return styles.CardLabel.Copy().Background(lipgloss.Color(theme.Black)).Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles theme.Styles) string {
	keyStyle := styles.FgFaint.Copy().Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Copy().Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-7s", key))
	return keyStr + valStyleWithBg.Render(value)
}

func cardFieldWrapped(key string, valStyle lipgloss.Style, value string, width int, styles theme.Styles) []string {
	valWidth := max(1, width-7)
	wrapped := wrapWords(strings.TrimSpace(value), valWidth, 2)
	if len(wrapped) == 0 {
		return nil
	}
	out := make([]string, 0, len(wrapped))
	keyStyle := styles.FgFaint.Copy().Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Copy().Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-7s", key))
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	indent := bgStyle.Render(strings.Repeat(" ", 7))
	for i, line := range wrapped {
		if i == 0 {
			out = append(out, keyStr+valStyleWithBg.Render(line))
		} else {
			out = append(out, indent+valStyleWithBg.Render(line))
		}
	}
	return out
}
