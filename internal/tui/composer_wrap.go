package tui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// composerLineRow is one drawn row of a logical composer line: the row text
// with the wrap's reserved trailing spaces trimmed (so a drawn row never
// exceeds the composer's inner width) and the grid runes the text is built
// from, which carry the logical-line offsets used to place the caret.
type composerLineRow struct {
	text  string
	runes []composerGridRune
}

// composerGridRune is one rune of a wrapped row: the rune and the offset it came
// from in the logical line, or -1 for the trailing space the wrap reserves so
// the caret can sit after the last character. Whitespace is materialized as an
// ASCII space, matching what the textarea draws, while value keeps the offset of
// the whitespace rune it came from.
type composerGridRune struct {
	r     rune
	value int
}

// wrapComposerLine splits a logical input line into the rows the composer
// draws. The rule mirrors the textarea's soft wrap exactly: whitespace is
// unicode whitespace, a word keeps the whitespace that follows it until the two
// no longer fit together, a word wider than the row is broken at the width, and
// a row that reaches the width reserves a trailing space (and a row for it).
// Drawing the textarea's rows is what keeps Up/Down, which move through those
// rows, in step with what is on screen.
func wrapComposerLine(line string, width int) []composerLineRow {
	if width < 1 {
		width = 1
	}
	value := []rune(line)
	var (
		grid   = [][]composerGridRune{{}}
		word   []composerGridRune
		spaces []composerGridRune
		row    int
	)

	for i, r := range value {
		if unicode.IsSpace(r) {
			spaces = append(spaces, composerGridRune{r: ' ', value: i})
		} else {
			word = append(word, composerGridRune{r: r, value: i})
		}

		if len(spaces) > 0 {
			if composerGridWidth(grid[row])+composerGridWidth(word)+len(spaces) > width {
				row++
				grid = append(grid, nil)
			}
			grid[row] = append(grid[row], word...)
			grid[row] = append(grid[row], spaces...)
			word, spaces = nil, nil
			continue
		}
		if len(word) > 0 {
			// A word that has reached the width starts a new row, unless the
			// current row is still empty.
			if composerGridWidth(word)+ansi.StringWidth(string(word[len(word)-1].r)) > width {
				if len(grid[row]) > 0 {
					row++
					grid = append(grid, nil)
				}
				grid[row] = append(grid[row], word...)
				word = nil
			}
		}
	}

	if composerGridWidth(grid[row])+composerGridWidth(word)+len(spaces) >= width {
		row++
		grid = append(grid, nil)
	}
	grid[row] = append(grid[row], word...)
	grid[row] = append(grid[row], spaces...)
	grid[row] = append(grid[row], composerGridRune{r: ' ', value: -1})

	rows := make([]composerLineRow, 0, len(grid))
	for _, rowRunes := range grid {
		rows = append(rows, composerLineRow{
			text:  strings.TrimRight(composerGridText(rowRunes), " "),
			runes: rowRunes,
		})
	}
	return rows
}

func composerGridWidth(row []composerGridRune) int {
	return ansi.StringWidth(composerGridText(row))
}

func composerGridText(row []composerGridRune) string {
	var b strings.Builder
	for _, g := range row {
		b.WriteRune(g.r)
	}
	return b.String()
}

// composerGridCol returns the cell column the caret occupies on a row: the
// width of the row's content before the logical-line offset offset, measured on
// the grid so whitespace counts as the ASCII space the wrap materialized.
func composerGridCol(row []composerGridRune, offset int) int {
	col := 0
	for _, g := range row {
		if g.value < 0 {
			continue
		}
		if g.value >= offset {
			break
		}
		col += ansi.StringWidth(string(g.r))
	}
	return col
}

// composerGridStart returns the logical-line offset of the row's first rune,
// falling back to end for a row that holds only reserved spaces.
func composerGridStart(row []composerGridRune, end int) int {
	for _, g := range row {
		if g.value >= 0 {
			return g.value
		}
	}
	return end
}

// stripTrailingReset removes the trailing ANSI reset sequence added by lipgloss Style.Render.
func stripTrailingReset(s string) string {
	s = strings.TrimSuffix(s, "\x1b[0m")
	return strings.TrimSuffix(s, "\x1b[m")
}

// applyComposerCursorAnsi highlights the character at the given visible
// column position in an ANSI-styled string using reverse video (SGR 7/27),
// so the cursor overlays the existing glyph instead of displacing it. SGR
// 7/27 compose with whatever styling is already active and scope off
// cleanly without emitting a reset, unlike a lipgloss-rendered span (see
// stripTrailingReset for why that distinction matters in this file).
// When on is false the string is returned with no highlight applied at all
// (the character renders in its normal, already-applied style).
func applyComposerCursorAnsi(s string, pos int, on bool) string {
	if !on {
		return s
	}
	if pos < 0 {
		pos = 0
	}
	var result strings.Builder
	currentCol := 0
	inEsc := false
	var escSeq strings.Builder
	placed := false
	for _, r := range s {
		if inEsc {
			escSeq.WriteRune(r)
			// Only apply terminator check after we've seen the CSI introducer byte.
			// A standard CSI is: ESC [, then parameter bytes (0x30-0x3F: digits, ;, etc.),
			// then a final byte (0x40-0x7E). The introducer '[' itself must never be
			// treated as a terminator, even though it's in the 0x40-0x7E range.
			// Once escSeq.Len() > 2, we've written ESC and the introducer, so we can
			// safely check if this byte is a terminator.
			if escSeq.Len() > 2 && ((r >= '@' && r <= '~') || r == '\\') {
				inEsc = false
				result.WriteString(escSeq.String())
				escSeq.Reset()
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			escSeq.WriteRune(r)
			continue
		}
		if currentCol == pos {
			placed = true
			result.WriteString("\x1b[7m")
			result.WriteRune(r)
			result.WriteString("\x1b[27m")
		} else {
			result.WriteRune(r)
		}
		currentCol += ansi.StringWidth(string(r))
	}
	if !placed {
		result.WriteString("\x1b[7m \x1b[27m")
	}
	return result.String()
}

func styleImageMarkers(line string, markerStyle lipgloss.Style, labels map[string]struct{}) string {
	return imageMarkerPattern.ReplaceAllStringFunc(line, func(match string) string {
		if _, ok := labels[match]; !ok {
			return match
		}
		return markerStyle.Render(match)
	})
}

func renderInputLine(line string, width int, markerStyle lipgloss.Style, labels map[string]struct{}) string {
	if styled := styleImageMarkers(line, markerStyle, labels); styled != line {
		return theme.WithBg(lipgloss.NewStyle().Width(width).Render(styled), theme.UserSoft)
	}
	return lipgloss.NewStyle().Width(width).Render(line)
}
