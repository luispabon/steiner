package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

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

	if s.updateAvailable && s.latestVersion != "" {
		text := fitText(fmt.Sprintf("↑ %s available · steiner upgrade", s.latestVersion), width)
		msg := s.styles.Accent.Background(lipgloss.Color(theme.Black)).Render(text)
		if pad := width - lipgloss.Width(msg); pad > 0 {
			msg += bg.Render(strings.Repeat(" ", pad))
		}
		out = append(out, msg)
	}
	return out
}

func cardLabel(label string, styles *theme.Styles) string {
	return styles.CardLabel.Background(lipgloss.Color(theme.Black)).Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	return cardFieldN(key, 7, valStyle, value, styles)
}

func cardFieldN(key string, keyWidth int, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	keyStyle := styles.FgFaint.Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key))
	return keyStr + valStyleWithBg.Render(value)
}

// cardFieldAccent renders a field row whose key uses the accent card-label
// style (same as the REPOSITORY/PERFORMANCE headers) instead of the faint key
// style, keeping the value inline. The key is padded to the status trio's
// fixed 8-column width (SANDBOX/SKILL/MCP).
func cardFieldAccent(key string, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	const keyWidth = 8
	keyStyle := styles.CardLabel.Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key))
	return keyStr + valStyleWithBg.Render(value)
}
