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
	bg := lipgloss.NewStyle().Background(lipgloss.Color(s.styles.Palette.SidebarBG))
	accentFg := s.styles.Accent.Background(lipgloss.Color(s.styles.Palette.SidebarBG))
	out := make([]string, 0, len(logo))
	for i, line := range logo {
		if i < 2 {
			out = append(out, accentFg.Render(line))
			continue
		}
		out = append(out, bg.Render(line))
	}

	// Wrap the version here rather than letting the sidebar style soft-wrap it:
	// roster click hit-testing counts these lines, so each must be one screen row.
	verStyle := s.styles.FgMute.Background(lipgloss.Color(s.styles.Palette.SidebarBG))
	chunks := wrapRunes(s.version, max(1, width-lipgloss.Width(out[2])-1), max(1, width))
	padRow := func(row string) string {
		if pad := width - lipgloss.Width(row); pad > 0 {
			row += bg.Render(strings.Repeat(" ", pad))
		}
		return row
	}
	out[2] = padRow(out[2] + bg.Render(" ") + verStyle.Render(chunks[0]))
	for _, chunk := range chunks[1:] {
		out = append(out, padRow(verStyle.Render(chunk)))
	}

	if s.updateAvailable && s.latestVersion != "" {
		// Shortened from "↑ %s available · steiner upgrade" (Part D's spec
		// text) so long dev-channel tags (e.g. dev-8-g8bd663f) still fit the
		// sidebar's 32-column inner width without fitText truncating the
		// call-to-action.
		text := fitText(fmt.Sprintf("↑ %s · upgrade", s.latestVersion), width)
		msg := s.styles.Accent.Background(lipgloss.Color(s.styles.Palette.SidebarBG)).Render(text)
		if pad := width - lipgloss.Width(msg); pad > 0 {
			msg += bg.Render(strings.Repeat(" ", pad))
		}
		out = append(out, msg)
	}
	return out
}

func cardLabel(label string, styles *theme.Styles) string {
	return styles.CardLabel.Background(lipgloss.Color(styles.Palette.SidebarBG)).Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	return cardFieldN(key, 7, valStyle, value, styles)
}

func cardFieldN(key string, keyWidth int, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	keyStyle := styles.FgFaint.Background(lipgloss.Color(styles.Palette.SidebarBG))
	valStyleWithBg := valStyle.Background(lipgloss.Color(styles.Palette.SidebarBG))
	keyStr := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key))
	return keyStr + valStyleWithBg.Render(value)
}

// cardFieldAccent renders a field row whose key uses the accent card-label
// style (same as the REPOSITORY/PERFORMANCE headers) instead of the faint key
// style, keeping the value inline. The key is padded to the status block's
// shared key width (SANDBOX/ORCHESTRATION/SKILL/MCP/LSP).
//
//nolint:unparam // keyWidth is the status block's single shared column width today (statusSection's keyW), kept explicit rather than hardcoded so the block's rows can't drift out of alignment.
func cardFieldAccent(key string, keyWidth int, valStyle lipgloss.Style, value string, styles *theme.Styles) string {
	keyStyle := styles.CardLabel.Background(lipgloss.Color(styles.Palette.SidebarBG))
	valStyleWithBg := valStyle.Background(lipgloss.Color(styles.Palette.SidebarBG))
	keyStr := keyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key))
	return keyStr + valStyleWithBg.Render(value)
}

// wrapRunes hard-wraps text into a first chunk of at most first cells and
// further chunks of at most rest cells. It always returns at least one chunk
// and no chunk exceeds its limit: a wide rune that cannot fit the first chunk
// leaves it empty, and one wider than rest truncates the text there.
func wrapRunes(text string, first, rest int) []string {
	var chunks []string
	limit := first
	var cur strings.Builder
	curW := 0
	for _, r := range text {
		rw := lipgloss.Width(string(r))
		if curW+rw > limit {
			if curW > 0 || (len(chunks) == 0 && limit != rest) {
				chunks = append(chunks, cur.String())
				cur.Reset()
				curW = 0
				limit = rest
			}
			if rw > limit {
				break
			}
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 || len(chunks) == 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}
