package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// ColorHex converts a color.Color to a "#RRGGBB" hex string suitable for
// use with HighlightMatch.
// Returns an empty string for nil or zero-alpha colors.
func ColorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	// RGBA returns 16-bit values; shift down to 8-bit.
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// HighlightMatch wraps text in explicit ANSI emphasis so matched glyphs stay
// visible even when rendering without an attached TTY profile.
func HighlightMatch(text string, fg string) string {
	if text == "" {
		return ""
	}
	style := lipgloss.NewStyle().Bold(true).Underline(true)
	if fg != "" {
		style = style.Foreground(lipgloss.Color(fg))
	}
	return style.Render(text)
}
