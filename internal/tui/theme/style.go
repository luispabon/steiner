package theme

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

// ColorHex converts a color.Color to a "#RRGGBB" hex string suitable for
// use with fgEscape and HighlightMatch.
// Returns an empty string for nil or zero-alpha colors.
func ColorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	// RGBA returns 16-bit values; shift down to 8-bit.
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func fgEscape(fg string) string {
	hex := strings.TrimPrefix(fg, "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// HighlightMatch wraps text in explicit ANSI emphasis so matched glyphs stay
// visible even when rendering without an attached TTY profile.
func HighlightMatch(text string, fg string) string {
	if text == "" {
		return ""
	}
	seq := "\x1b[1;4m"
	if fgSeq := fgEscape(fg); fgSeq != "" {
		seq += fgSeq
	}
	return seq + text + "\x1b[0m"
}
