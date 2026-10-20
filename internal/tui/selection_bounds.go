package tui

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Compiled regexps for token-aware double-click selection.
//
// pathRegex's two bare-relative alternatives (no leading /, ~, or .) are
// deliberately narrower than "anything with a slash", to avoid swallowing
// prose: a bare relative path qualifies only when it has two or more path
// separators (internal/tui/selection.go) or its final segment carries a file
// extension (docs/user/oneshot.md). This rejects and/or, TCP/IP, 24/7, and 12/25
// while accepting steiner's own path output. A single-separator bare path is
// admitted when one side is long enough to be a branch or namespace ref: a
// final segment of five or more characters (cl/2026-08-05_mcp_approval_model)
// or a first segment of six or more (origin/main). Short prose pairs like
// he/she and src/main stay rejected. A dot-prefixed segment is its own
// path signal: \.[\w\-]+(?:/[\w.\-]+)*/? matches hidden files and dot-dirs
// (.env, .project_planning/x) at any depth. Like the other prefix-marked
// alternatives it must start at a token boundary, so example.com/index.html
// and the foo.bar stay rejected; a prose ".NET" is an accepted trade-off. The
// trailing (?::\d+(?::\d+)?)? group attaches an optional :line or :line:col
// suffix to every alternative.
var (
	urlRegex  = regexp.MustCompile(`(?:https?|ftp|file|git|ssh)://[^\s]+`)
	pathRegex = regexp.MustCompile(
		`(?:` +
			`/(?:[\w.\-]+/)*[\w.\-]+/?` +
			`|~(?:/[\w.\-]+)*/?` +
			`|\.[\w\-]+(?:/[\w.\-]+)*/?` +
			`|\.\.?(?:/[\w.\-]+)*/?` +
			`|[\w.\-]+(?:/[\w.\-]+){2,}/?` +
			`|[\w\-]+(?:/[\w.\-]+)*/[\w\-]+\.\w+` +
			`|[\w\-]+/[\w.\-]{5,}` +
			`|[\w\-]{6,}/[\w.\-]+` +
			`)(?::\d+(?::\d+)?)?`)
)

// isTokenBoundaryRune reports whether r may precede a path token: whitespace,
// common quoting/bracketing punctuation, or a box-drawing character (so a
// path immediately after viewport chrome such as "│" is still recognised).
func isTokenBoundaryRune(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case ':', '\'', '"', '(', '[', '=', ',':
		return true
	}
	return isBoxDrawing(r)
}

// visualColumns builds the visual-column (terminal cell) start offset and
// width for each rune in runes, along with the total visual width.
func visualColumns(runes []rune) (starts, widths []int, total int) {
	starts = make([]int, len(runes))
	widths = make([]int, len(runes))
	for i, r := range runes {
		starts[i] = total
		w := runewidth.RuneWidth(r)
		widths[i] = w
		total += w
	}
	return starts, widths, total
}

// tokenBoundsAt checks if col falls within a token matched by re in line. If
// requireBoundary is true, a match must start at a token boundary (see
// isTokenBoundaryRune) or the start of the line. Trailing punctuation is
// trimmed from the match. Returns ok=false if no matching token contains col.
//
//nolint:gocyclo // token detection inherently involves multiple regex matches and trim logic
func tokenBoundsAt(line string, col int, re *regexp.Regexp, requireBoundary bool) (startCol, endCol int, ok bool) {
	locs := re.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return 0, 0, false
	}

	runes := []rune(line)
	n := len(runes)
	if n == 0 {
		return 0, 0, false
	}

	visStarts, visWidths, total := visualColumns(runes)

	if col < 0 || col >= total {
		return 0, 0, false
	}

	for _, loc := range locs {
		byteStart, byteEnd := loc[0], loc[1]
		if byteStart >= byteEnd {
			continue
		}

		if requireBoundary && byteStart > 0 {
			r, _ := utf8.DecodeLastRuneInString(line[:byteStart])
			if !isTokenBoundaryRune(r) {
				continue
			}
		}

		// Convert byte offsets to rune indices.
		runeStart := len([]rune(line[:byteStart]))
		runeEnd := len([]rune(line[:byteEnd]))
		if runeStart >= n || runeEnd > n || runeStart >= runeEnd {
			continue
		}

		visStart := visStarts[runeStart]
		visEndOrig := visStarts[runeEnd-1] + visWidths[runeEnd-1]

		if col < visStart || col >= visEndOrig {
			continue
		}

		// Trim trailing punctuation from the match.
		trimmedEnd := runeEnd
		for trimmedEnd > runeStart {
			r := runes[trimmedEnd-1]
			if r == '.' || r == ',' || r == ';' || r == ':' || r == '!' || r == '?' ||
				r == ')' || r == ']' || r == '}' || r == '"' || r == '\'' {
				trimmedEnd--
			} else {
				break
			}
		}

		if trimmedEnd <= runeStart {
			return visStart, visStart, true
		}
		visEnd := visStarts[trimmedEnd-1] + visWidths[trimmedEnd-1]
		return visStart, visEnd, true
	}

	return 0, 0, false
}

// isWordChar reports whether r is part of a "word" for double-click
// word-selection purposes: letters, digits, underscore, and hyphen.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// urlBoundsAt checks if col falls within a URL token in line. If so it returns
// the visual column range [startCol, endCol) of the URL, with trailing
// punctuation trimmed. Returns ok=false if no URL contains col.
func urlBoundsAt(line string, col int) (startCol, endCol int, ok bool) {
	return tokenBoundsAt(line, col, urlRegex, false)
}

// pathBoundsAt checks if col falls within a file path token in line. The
// match must start at a token boundary (see isTokenBoundaryRune) or
// start-of-line. Returns ok=false if no path contains col.
func pathBoundsAt(line string, col int) (startCol, endCol int, ok bool) {
	return tokenBoundsAt(line, col, pathRegex, true)
}

// wordBoundsAt returns the visual column range [startCol, endCol) of the word
// touching visual column col in line (which must already be ANSI-stripped).
// If col lands on a non-word character, the range covers just that character.
// If col is at or beyond the line's visual width, it returns a zero-width
// range at the line's width.
//
//nolint:gocyclo // word bounds plus two token-detection early checks
func wordBoundsAt(line string, col int) (startCol, endCol int) {
	// Try URL token detection first (takes priority over path).
	if startCol, endCol, ok := urlBoundsAt(line, col); ok {
		return startCol, endCol
	}
	// Try path token detection next.
	if startCol, endCol, ok := pathBoundsAt(line, col); ok {
		return startCol, endCol
	}

	runes := []rune(line)
	if len(runes) == 0 {
		if col < 0 {
			col = 0
		}
		return col, col
	}

	starts := make([]int, len(runes))
	widths := make([]int, len(runes))
	total := 0
	for i, r := range runes {
		starts[i] = total
		w := runewidth.RuneWidth(r)
		widths[i] = w
		total += w
	}

	if col < 0 {
		col = 0
	}
	if col >= total {
		return total, total
	}

	idx := -1
	for i := range runes {
		if col >= starts[i] && col < starts[i]+widths[i] {
			idx = i
			break
		}
	}
	if idx == -1 {
		return col, col
	}

	if !isWordChar(runes[idx]) {
		return starts[idx], starts[idx] + widths[idx]
	}

	left := idx
	for left > 0 && isWordChar(runes[left-1]) {
		left--
	}
	right := idx
	for right < len(runes)-1 && isWordChar(runes[right+1]) {
		right++
	}
	return starts[left], starts[right] + widths[right]
}

// lineBoundsAt returns the full visual column range [0, width) of
// lines[lineIdx], ANSI-stripped before measuring width. Returns a zero-width
// range for an out-of-range lineIdx or an empty line.
//
//nolint:unparam // startCol is always 0 by design; kept for symmetry with wordBoundsAt's (startCol, endCol) signature.
func lineBoundsAt(lines []string, lineIdx int) (startCol, endCol int) {
	if lineIdx < 0 || lineIdx >= len(lines) {
		return 0, 0
	}
	return 0, ansi.StringWidth(ansi.Strip(lines[lineIdx]))
}

// logicalLineBounds returns the screen-line and visual-column bounds of the
// full logical (pre-wrap) line touching lines[lineIdx], within the content
// region [regionLeft, regionRight). A screen line is considered "full" (and
// therefore continues onto the next screen line) when its content-region
// width is within a 2-cell threshold of the region width, accommodating
// word-wrap rounding and wide characters. It walks upward and downward from
// lineIdx across contiguous full lines with non-empty continuations to find
// the full extent of the wrapped logical line. Returns a zero-width result
// for an out-of-range lineIdx.
func logicalLineBounds(lines []string, lineIdx, regionLeft, regionRight int) (startLine, endLine, startCol, endCol int) {
	if lineIdx < 0 || lineIdx >= len(lines) {
		return lineIdx, lineIdx, regionLeft, regionLeft
	}

	threshold := regionRight - regionLeft - 2

	lineWidth := func(i int) int {
		raw := ansi.Strip(lines[i])
		segment := truncateByWidth(raw, regionLeft, regionRight)
		return runewidth.StringWidth(strings.TrimRight(segment, " "))
	}
	isFull := func(i int) bool {
		return lineWidth(i) >= threshold
	}
	hasContent := func(i int) bool {
		return lineWidth(i) > 0
	}

	startLine = lineIdx
	for startLine > 0 && isFull(startLine-1) && hasContent(startLine) {
		startLine--
	}

	endLine = lineIdx
	for endLine < len(lines)-1 && isFull(endLine) && hasContent(endLine+1) {
		endLine++
	}

	startCol = regionLeft
	endCol = min(regionRight, regionLeft+lineWidth(endLine))

	return startLine, endLine, startCol, endCol
}
