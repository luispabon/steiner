package theme

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	ansiResetLong  = "\x1b[0m"
	ansiResetShort = "\x1b[m"
	ansiBgReset    = "\x1b[49m"
)

// ColorHex converts a color.Color to a "#RRGGBB" hex string suitable for
// use with HighlightMatch, WithBg, and PadLines.
// Returns an empty string for nil or zero-alpha colors.
func ColorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	// RGBA returns 16-bit values; shift down to 8-bit.
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// bgEscape returns an ANSI escape sequence that sets the background to the
// given color. Unlike lipgloss.Style.Render, this does not depend on the
// terminal's color profile — it always produces a 24-bit truecolor sequence.
func bgEscape(bg string) string {
	hex := strings.TrimPrefix(bg, "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// WithBg ensures every line in s has its background set to bg.
// It re-applies the background after ANSI reset sequences and at the start of
// each logical line. Idempotent for the same bg.
//
//nolint:gocyclo // byte-scanner: complexity is structural, not accidental
func WithBg(s string, bg string) string {
	bgSeq := bgEscape(bg)
	if bgSeq == "" {
		return s
	}

	trailingNewlines := len(s) - len(strings.TrimRight(s, "\n"))
	body := s[:len(s)-trailingNewlines]

	var sb strings.Builder
	sb.Grow(len(body) + len(bgSeq)*(strings.Count(body, "\n")+1)*3)

	atLineStart := true
	lineHasBytes := false

	for i := 0; i < len(body); i++ {
		c := body[i]

		if c == '\n' {
			if !lineHasBytes {
				sb.WriteString(bgSeq)
				sb.WriteByte(' ')
				sb.WriteString(ansiResetLong)
			}
			sb.WriteByte('\n')
			atLineStart = true
			lineHasBytes = false
			continue
		}

		if atLineStart {
			sb.WriteString(bgSeq)
			atLineStart = false
		}

		// Check for ANSI escape sequences we replace.
		if c == '\x1b' && i+1 < len(body) && body[i+1] == '[' {
			// \x1b[49m (5 bytes) — ansiBgReset
			if i+4 < len(body) && body[i+2] == '4' && body[i+3] == '9' && body[i+4] == 'm' {
				sb.WriteString(ansiBgReset)
				sb.WriteString(bgSeq)
				i += 4
				lineHasBytes = true
				continue
			}
			// \x1b[0m (4 bytes) — ansiResetLong
			if i+3 < len(body) && body[i+2] == '0' && body[i+3] == 'm' {
				sb.WriteString(ansiResetLong)
				sb.WriteString(bgSeq)
				i += 3
				lineHasBytes = true
				continue
			}
			// \x1b[m (3 bytes) — ansiResetShort
			if i+2 < len(body) && body[i+2] == 'm' {
				sb.WriteString(ansiResetShort)
				sb.WriteString(bgSeq)
				i += 2
				lineHasBytes = true
				continue
			}
		}

		sb.WriteByte(c)
		lineHasBytes = true
	}

	// Flush the last line if body is non-empty (no trailing \n in body).
	if len(body) > 0 {
		if atLineStart {
			// body ended immediately after a \n — edge case
			sb.WriteString(bgSeq)
			sb.WriteByte(' ')
			sb.WriteString(ansiResetLong)
		} else if !lineHasBytes {
			// atLineStart is false but lineHasBytes is false: only bgSeq was written
			sb.WriteByte(' ')
			sb.WriteString(ansiResetLong)
		}
	}

	if trailingNewlines > 0 {
		sb.WriteString(strings.Repeat("\n", trailingNewlines))
	}
	return sb.String()
}

// PadLines pads every line in s to the given width by appending spaces
// rendered with the specified background color. Lines already at or
// beyond the target width are left unchanged.
func PadLines(s string, width int, bg string) string {
	if width < 1 {
		return s
	}
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))

	var sb strings.Builder
	sb.Grow(len(s) + width)

	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			w := lipgloss.Width(line)
			sb.WriteString(line)
			if w < width {
				sb.WriteString(bgStyle.Render(strings.Repeat(" ", width-w)))
			}
			if i < len(s) {
				sb.WriteByte('\n')
			}
			start = i + 1
		}
	}
	return sb.String()
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

// ApplyPanePadding applies ContentPane or ContentPaneWithScrollbar padding to
// content using string operations, bypassing the lipgloss Wrap/align pipeline.
// ContentPane adds PaddingTop(1), PaddingLeft(3), PaddingRight(3).
// ContentPaneWithScrollbar adds PaddingLeft(3), PaddingRight(2) (no top pad —
// renderViewportWithScrollbar already prepends a leading '\n').
func ApplyPanePadding(content string, contentWidth int, hasScrollbar bool, bg string) string {
	padLeft := 3
	padRight := 3
	padTop := 1
	if hasScrollbar {
		padRight = 2
		padTop = 0
	}

	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))
	leftPad := bgStyle.Render(strings.Repeat(" ", padLeft))
	rightPad := bgStyle.Render(strings.Repeat(" ", padRight))

	var sb strings.Builder
	sb.Grow(len(content) + (padLeft+padRight+2)*strings.Count(content, "\n") + contentWidth*(padTop+1))

	if padTop > 0 {
		sb.WriteString(bgStyle.Render(strings.Repeat(" ", contentWidth)))
		sb.WriteByte('\n')
	}

	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			sb.WriteString(leftPad)
			sb.WriteString(content[start:i])
			sb.WriteString(rightPad)
			if i < len(content) {
				sb.WriteByte('\n')
			}
			start = i + 1
		}
	}
	return sb.String()
}

// TruncateAndPadVertical truncates s to maxHeight lines and pads to exactly
// maxHeight lines with bg-colored empty lines. Avoids the full lipgloss
// Wrap/align pipeline for already-correctly-sized content.
func TruncateAndPadVertical(s string, width, maxHeight int, bg string) string {
	lines := 0
	truncated := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines++
			if lines >= maxHeight {
				s = s[:i]
				truncated = true
				break
			}
		}
	}

	actualLines := lines + 1
	if truncated {
		actualLines = maxHeight
	}

	if actualLines >= maxHeight {
		return s
	}

	// Only allocate the pad line when we actually need to pad.
	padLine := lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(strings.Repeat(" ", width))
	pad := maxHeight - actualLines
	var sb strings.Builder
	sb.Grow(len(s) + pad*(len(padLine)+1))
	sb.WriteString(s)
	for range pad {
		sb.WriteByte('\n')
		sb.WriteString(padLine)
	}
	return sb.String()
}
