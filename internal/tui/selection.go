package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

type selectionPoint struct {
	line, col int
}

type selectionRegion int

const (
	regionNone selectionRegion = iota
	regionViewport
	regionInput
	regionSidebar
)

type selectionState struct {
	start, end selectionPoint
	active     bool
}

// hasSelection reports whether a non-trivial selection exists.
func (s selectionState) hasSelection() bool {
	return s.start != s.end || s.active
}

// canonical returns (start, end) normalised so start <= end by line, then col.
func (s selectionState) canonical() (selectionPoint, selectionPoint) {
	if s.start.line < s.end.line || (s.start.line == s.end.line && s.start.col <= s.end.col) {
		return s.start, s.end
	}
	return s.end, s.start
}

// clear returns the zero selectionState.
func (s selectionState) clear() selectionState {
	return selectionState{}
}

// extractText extracts plain text from lines for the given selection state.
// Lines may be pre-stripped or contain ANSI sequences (stripped internally).
// Column values are visual column positions (terminal cells), not byte offsets.
// regionLeft and regionRight constrain intermediate lines to the active region's
// content bounds when regionRight > 0. Pass 0, 0 to disable constraining.
func extractText(lines []string, state selectionState, regionLeft, regionRight int) string {
	if !state.hasSelection() {
		return ""
	}
	start, end := state.canonical()
	var parts []string
	for i := start.line; i <= end.line; i++ {
		if i < 0 || i >= len(lines) {
			continue
		}
		raw := ansi.Strip(lines[i])
		sc := 0
		ec := len([]rune(raw))
		if i == start.line {
			sc = start.col
		}
		if i == end.line {
			ec = end.col
		}
		if regionRight > 0 {
			sc = max(regionLeft, sc)
			ec = min(regionRight, ec)
		}
		extracted := truncateByWidth(raw, sc, ec)
		extracted = strings.TrimRight(extracted, " ")
		extracted = stripBoxChrome(extracted)
		extracted = strings.TrimRight(extracted, " ")
		parts = append(parts, extracted)
	}
	return strings.Join(parts, "\n")
}

// truncateByWidth extracts the substring between visual columns start and end.
func truncateByWidth(s string, start, end int) string {
	if start >= end {
		return ""
	}
	col := 0
	byteStart := len(s)
	byteEnd := len(s)
	for i, r := range s {
		if col >= end {
			byteEnd = i
			break
		}
		if col >= start && byteStart == len(s) {
			byteStart = i
		}
		col += runewidth.RuneWidth(r)
	}
	if byteStart > byteEnd {
		byteStart = byteEnd
	}
	return s[byteStart:byteEnd]
}

// isBoxDrawing reports whether r is a box-drawing character from the set
// │┌┐└┘─├┤╭╮╯╰.
func isBoxDrawing(r rune) bool {
	switch r {
	case '│', '┌', '┐', '└', '┘', '─', '├', '┤', '╭', '╮', '╯', '╰':
		return true
	}
	return false
}

// stripBoxChrome removes leading and trailing box-drawing characters and
// adjacent spaces from a string. Only one run of adjacent spaces touching
// each border is consumed as "padding"; interior intentional indentation
// is preserved. Empty string in produces empty string out; a string of only
// border chars/spaces produces empty string out.
func stripBoxChrome(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}

	// Strip leading box-drawing characters.
	left := 0
	for left < len(runes) && isBoxDrawing(runes[left]) {
		left++
	}
	// Strip at most one space after leading box-drawing.
	if left < len(runes) && runes[left] == ' ' {
		left++
	}

	// Strip trailing box-drawing characters.
	right := len(runes) - 1
	for right >= left && isBoxDrawing(runes[right]) {
		right--
	}
	// Strip at most one space before trailing box-drawing.
	if right >= left && runes[right] == ' ' {
		right--
	}

	if left > right {
		return ""
	}

	result := runes[left : right+1]
	// If the result contains only box-drawing characters and spaces, return empty.
	for _, r := range result {
		if !isBoxDrawing(r) && r != ' ' {
			return string(result)
		}
	}
	return ""
}

// applyScreenHighlight applies selStyle highlight to screen-space coordinates.
// regionLeft and regionRight constrain intermediate (fully-selected) lines to
// the active region's content bounds, preventing the highlight from bleeding
// into the sidebar, divider, or padding columns. Pass 0, 0 to disable
// constraining (matches the un-clamped legacy behaviour).
func applyScreenHighlight(frame string, state selectionState, selStyle lipgloss.Style, regionLeft, regionRight int) string {
	if !state.hasSelection() {
		return frame
	}
	start, end := state.canonical()
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		if i < start.line || i > end.line {
			continue
		}
		lineWidth := ansi.StringWidth(line)
		startCol, endCol := 0, lineWidth
		if i == start.line {
			startCol = start.col
		}
		if i == end.line {
			endCol = end.col
		}
		if regionRight > 0 {
			startCol = max(regionLeft, startCol)
			endCol = min(regionRight, endCol)
		}
		if startCol >= endCol {
			continue
		}
		before := ansi.Cut(line, 0, startCol)
		mid := selStyle.Render(ansi.Strip(ansi.Cut(line, startCol, endCol)))
		after := ansi.Cut(line, endCol, lineWidth)
		lines[i] = before + mid + after
	}
	return strings.Join(lines, "\n")
}

// detectRegion classifies a screen coordinate (x, y) into a UI region.
// Returns regionNone for dividers and status bar, regionSidebar for sidebar,
// regionInput for the input area, and regionViewport for viewport content.
func (m Model) detectRegion(x, y int) selectionRegion {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return regionNone
	}

	// The sidebar and its divider column span the full screen height
	// (including the input and status rows), so an x-based sidebar/divider
	// hit must be classified before any row-based (status/input) check.
	if sidebarVisible {
		if m.sidebarPosition == "right" {
			dividerStartX := m.width - sidebarWidth - 1
			if x >= m.width-sidebarWidth {
				return regionSidebar
			}
			if x == dividerStartX {
				return regionNone
			}
		} else {
			if x < sidebarWidth {
				return regionSidebar
			}
			if x == sidebarWidth {
				return regionNone
			}
		}
	}

	statusRow := m.height - 1
	if y == statusRow {
		return regionNone
	}

	inputChromeH := m.inputChromeHeight(contentWidth)
	inputStartRow := statusRow - inputChromeH
	if y >= inputStartRow {
		return regionInput
	}

	if y >= inputStartRow-2 {
		return regionNone
	}

	return regionViewport
}

// regionXBounds returns the [left, right) screen-column bounds of the content
// area for a given selection region (viewport or input), accounting for
// sidebar placement and the scrollbar-aware right padding. Shared by
// clampToRegion (which additionally clamps y and nudges x for click
// placement) and selectionHighlightBounds (which uses the bounds verbatim to
// constrain multi-line highlight). Returns (0, 0) for regionNone and
// regionSidebar.
func (m Model) regionXBounds(region selectionRegion) (left, right int) {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	switch region {
	case regionViewport:
		left = 0
		right = m.width

		if sidebarVisible {
			if m.sidebarPosition == "right" {
				right = m.width - sidebarWidth - 1
			} else {
				left = sidebarWidth + 1
			}
		}

		left += 3
		right -= 3
		if left > right {
			left = right
		}
		return left, right

	case regionInput:
		sidebarOffset := 0

		if sidebarVisible {
			if m.sidebarPosition == "right" {
				sidebarOffset = 0
			} else {
				sidebarOffset = sidebarWidth + 1
			}
		}

		left = sidebarOffset + inputRailWidth + inputPadX
		right = sidebarOffset + contentWidth - 1
		if left > right {
			right = left
		}
		return left, right

	default:
		return 0, 0
	}
}

// clampToRegion clamps screen coordinates (x, y) to the content bounds of a
// given selection region (viewport or input), preventing multi-line selection
// from bleeding into padding, dividers, sidebar, or the other container.
// For regionNone and regionSidebar, returns (x, y) unchanged.
func (m Model) clampToRegion(x, y int, region selectionRegion) (int, int) {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)
	statusRow := m.height - 1
	inputChromeH := m.inputChromeHeight(contentWidth)
	inputStartRow := statusRow - inputChromeH

	switch region {
	case regionViewport:
		left, right := m.regionXBounds(region)
		x = max(left, min(right-1, x))
		y = max(0, min(inputStartRow-3, y))
		return x, y

	case regionInput:
		left, right := m.regionXBounds(region)
		if sidebarVisible {
			x = max(left, min(right, x))
		} else {
			x = max(left, min(right-1, x))
		}
		y = max(inputStartRow, min(statusRow-1, y))
		return x, y

	default:
		return x, y
	}
}

// selectionHighlightBounds returns the [left, right) screen-column bounds of
// the currently active selection region's content area, for use by
// applyScreenHighlight to keep intermediate multi-line selection highlight
// out of the sidebar, dividers, and padding. Returns (0, 0) for regionNone,
// regionSidebar, or when no region is active, which preserves the legacy
// unconstrained behaviour.
func (m Model) selectionHighlightBounds() (left, right int) {
	return m.regionXBounds(m.activeRegion)
}

// Compiled regexps for token-aware double-click selection.
//
// pathRegex's two bare-relative alternatives (no leading /, ~, or .) are
// deliberately narrower than "anything with a slash", to avoid swallowing
// prose: a bare relative path qualifies only when it has two or more path
// separators (internal/tui/selection.go) or its final segment carries a file
// extension (docs/oneshot.md). This rejects and/or, TCP/IP, 24/7, and 12/25
// while accepting steiner's own path output. The trailing (?::\d+(?::\d+)?)?
// group attaches an optional :line or :line:col suffix to every alternative.
var (
	urlRegex  = regexp.MustCompile(`(?:https?|ftp|file|git|ssh)://[^\s]+`)
	pathRegex = regexp.MustCompile(
		`(?:` +
			`/(?:[\w.\-]+/)*[\w.\-]+/?` +
			`|~(?:/[\w.\-]+)*/?` +
			`|\.\.?(?:/[\w.\-]+)*/?` +
			`|[\w.\-]+(?:/[\w.\-]+){2,}/?` +
			`|[\w\-]+(?:/[\w.\-]+)*/[\w\-]+\.\w+` +
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

// populateScreenLines renders the current frame exactly as View() does and
// stores its ANSI-stripped lines in *m.screenLines. This duplicates the
// render+strip steps View() performs during an active selection drag,
// because click handling runs outside the render cycle and needs a freshly
// rendered frame on demand; having View() call this instead would double the
// render cost on every frame with an active selection.
func (m Model) populateScreenLines() {
	if m.screenLines == nil {
		return
	}
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)
	base := m.renderBaseView(contentWidth, sidebarVisible)
	result := m.renderOverlayView(base, contentWidth)
	*m.screenLines = strings.Split(ansi.Strip(result), "\n")
}

// copyToClipboard returns a tea.Cmd that writes text to the system clipboard.
// Tries wl-copy (Wayland), xclip, xsel, then falls back to OSC52.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if clipboardExec(text) {
			return nil
		}
		_, _ = fmt.Fprint(os.Stdout, ansi.SetSystemClipboard(text))
		return nil
	}
}

func clipboardExec(text string) bool {
	type candidate struct {
		name string
		args []string
		env  string
	}
	candidates := []candidate{
		{"wl-copy", nil, "WAYLAND_DISPLAY"},
		{"xclip", []string{"-selection", "clipboard"}, "DISPLAY"},
		{"xsel", []string{"--clipboard", "--input"}, "DISPLAY"},
	}
	for _, c := range candidates {
		if c.env != "" && os.Getenv(c.env) == "" {
			continue
		}
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		if runClipboardCandidate(path, c.args, text) {
			return true
		}
	}
	return false
}

func runClipboardCandidate(path string, args []string, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}
