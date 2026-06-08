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
	accentFg := s.styles.Accent.Background(lipgloss.Color(theme.Black))
	out := make([]string, 0, len(logo))
	for i, line := range logo {
		if i < 2 {
			out = append(out, accentFg.Render(line))
			continue
		}
		out = append(out, bg.Render(line))
	}

	ver := s.styles.FgMute.Background(lipgloss.Color(theme.Black)).Render(s.version)
	third := out[2] + bg.Render(" ") + ver
	if pad := width - lipgloss.Width(third); pad > 0 {
		third += bg.Render(strings.Repeat(" ", pad))
	}
	out[2] = third
	return out
}

func cardLabel(label string, styles theme.Styles) string {
	return styles.CardLabel.Background(lipgloss.Color(theme.Black)).Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles theme.Styles) string {
	return cardFieldN(key, 7, valStyle, value, styles)
}

func cardFieldN(key string, keyWidth int, valStyle lipgloss.Style, value string, styles theme.Styles) string {
	keyStyle := styles.FgFaint.Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key))
	return keyStr + valStyleWithBg.Render(value)
}
