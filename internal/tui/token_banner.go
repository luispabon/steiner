package tui

import (
	"strings"
	"unicode/utf8"
)

const (
	bannerRows = 5
	bannerGap  = "  "
)

// bannerGlyphs is a 5-row block font for token amounts. Every row of a glyph
// has the same width, so concatenated rows stay rectangular.
var bannerGlyphs = map[rune][bannerRows]string{
	'0': {
		" ███ ",
		"█   █",
		"█   █",
		"█   █",
		" ███ ",
	},
	'1': {
		"  █  ",
		" ██  ",
		"  █  ",
		"  █  ",
		" ███ ",
	},
	'2': {
		" ███ ",
		"    █",
		" ███ ",
		"█    ",
		"█████",
	},
	'3': {
		"█████",
		"    █",
		" ███ ",
		"    █",
		"█████",
	},
	'4': {
		"█   █",
		"█   █",
		"█████",
		"    █",
		"    █",
	},
	'5': {
		"█████",
		"█    ",
		"████ ",
		"    █",
		"████ ",
	},
	'6': {
		" ███ ",
		"█    ",
		"████ ",
		"█   █",
		" ███ ",
	},
	'7': {
		"█████",
		"    █",
		"   █ ",
		"  █  ",
		"  █  ",
	},
	'8': {
		" ███ ",
		"█   █",
		" ███ ",
		"█   █",
		" ███ ",
	},
	'9': {
		" ███ ",
		"█   █",
		" ████",
		"    █",
		" ███ ",
	},
	'.': {
		" ",
		" ",
		" ",
		" ",
		"█",
	},
	'k': {
		"█   █",
		"█  █ ",
		"███  ",
		"█  █ ",
		"█   █",
	},
	'M': {
		"█   █",
		"██ ██",
		"█ █ █",
		"█   █",
		"█   █",
	},
	'B': {
		"████ ",
		"█   █",
		"████ ",
		"█   █",
		"████ ",
	},
	' ': {
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
	},
}

// renderBanner renders text as bannerRows rows of block glyphs, separated by
// bannerGap. Runes outside the font are skipped; rows are always equal width.
func renderBanner(text string) []string {
	var rows [bannerRows]strings.Builder
	wrote := false
	for _, r := range text {
		glyph, ok := bannerGlyphs[r]
		if !ok {
			continue
		}
		if wrote {
			for i := range rows {
				rows[i].WriteString(bannerGap)
			}
		}
		for i := range rows {
			rows[i].WriteString(glyph[i])
		}
		wrote = true
	}
	out := make([]string, bannerRows)
	for i := range rows {
		out[i] = rows[i].String()
	}
	return out
}

// bannerWidth returns the display width of the rendered banner, so callers can
// fall back to a plain line when the overlay is narrower.
func bannerWidth(text string) int {
	width := 0
	for _, row := range renderBanner(text) {
		width = max(width, utf8.RuneCountInString(row))
	}
	return width
}
