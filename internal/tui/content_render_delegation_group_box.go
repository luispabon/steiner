package tui

import (
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// groupBoxChrome is the columns renderStyledBox spends on the border and padding:
// one border and one padding column per side.
const groupBoxChrome = 4

// groupBoxMinInner is the smallest inner width at which every line is wrapped
// on its own. It leaves room for a double-width rune.
const groupBoxMinInner = 2

// sgrParams matches an SGR sequence and captures its parameters.
var sgrParams = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// groupBoxCache holds the chrome, the side style, and the interior lines of a
// group box from its previous render. They are valid only for the width and
// colors they were built with; renderGroupBox resets them when any changes.
type groupBoxCache struct {
	width  int
	border color.Color
	bg     color.Color
	top    string
	bottom string
	side   lipgloss.Style
	lines  map[string]string
}

// groupBoxSideStyle is renderStyledBox without the top and bottom border. For
// any content its output equals the lines renderStyledBox draws between the
// top and bottom border, at about a third of the allocations per line.
func groupBoxSideStyle(border, bg color.Color, width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(bg).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, true, false, true).
		BorderForeground(border).
		Width(max(1, width))
}

// renderGroupBox returns renderStyledBox(content, border, bg, width). A frame
// with no line cache (the first render, or after a width, border, or background
// change) is drawn once through renderGroupBoxCold, which may seed the cache.
// Any other frame draws each line missing from the cache on its own and reuses
// the cached interior of the rest, so a spinner frame that changes a few lines
// pays only for those. A per-line result matches renderStyledBox only while lipgloss wraps to a
// positive inner width and the first row keeps the box width, so the full box
// is drawn whenever either check fails.
func renderGroupBox(cache *groupBoxCache, content string, border, bg color.Color, width int) string {
	if width-groupBoxChrome < groupBoxMinInner {
		return renderStyledBox(content, border, bg, width)
	}
	if cache.lines == nil || cache.width != width || cache.border != border || cache.bg != bg {
		chrome := strings.Split(renderStyledBox("", border, bg, width), "\n")
		*cache = groupBoxCache{
			width:  width,
			border: border,
			bg:     bg,
			top:    chrome[0],
			bottom: chrome[len(chrome)-1],
			side:   groupBoxSideStyle(border, bg, width),
		}
	}
	src := strings.Split(content, "\n")
	if cache.lines == nil {
		return renderGroupBoxCold(cache, content, src, border, bg, width)
	}
	next := make(map[string]string, len(src))
	out := make([]string, 0, len(src)+2)
	out = append(out, cache.top)
	for _, line := range src {
		interior, ok := cache.lines[line]
		if !ok {
			if !groupLineClosesSGR(line) {
				return renderStyledBox(content, border, bg, width)
			}
			interior = groupBoxInterior(line, cache)
			if !groupRowFits(interior, width) {
				return renderStyledBox(content, border, bg, width)
			}
		}
		next[line] = interior
		out = append(out, interior)
	}
	cache.lines = next
	out = append(out, cache.bottom)
	return strings.Join(out, "\n")
}

// groupLineClosesSGR reports whether line leaves no SGR style open at its end.
// lipgloss carries an open style into the next line, which a line drawn alone
// cannot reproduce, so renderGroupBox falls back to the full box for such lines.
// A line ending in a carriage return also reports false, because line-local
// rendering cannot reliably reproduce lipgloss's carriage-return handling.
func groupLineClosesSGR(line string) bool {
	if strings.HasSuffix(line, "\r") {
		return false
	}
	m := sgrParams.FindAllStringSubmatch(line, -1)
	if len(m) == 0 {
		return true
	}
	params := m[len(m)-1][1]
	return params == "" || params == "0"
}

// renderGroupBoxCold draws the whole content once through the side style. It
// seeds the cache from that draw only when every content line can be matched to
// its own rows: no line leaves an SGR open, no line holds a tab or carriage
// return, the first row fits the box, and the rows counted per line add up to
// the rows drawn. Otherwise it leaves an empty line cache, so the next frame
// draws line by line. On those frames a line that leaves an SGR open, or that
// ends in a carriage return, makes the frame fall back to the full box. Output
// stays correct either way.
func renderGroupBoxCold(cache *groupBoxCache, content string, src []string, border, bg color.Color, width int) string {
	body := cache.side.Render(content)
	// Every refusal below keeps an empty map: later frames take the per-line path
	// instead of paying the cold side render again.
	cache.lines = map[string]string{}
	if !groupRowFits(body, width) {
		return renderStyledBox(content, border, bg, width)
	}
	full := cache.top + "\n" + body + "\n" + cache.bottom
	rows := strings.Split(body, "\n")
	counts := make([]int, len(src))
	total := 0
	inner := width - groupBoxChrome
	for i, line := range src {
		n := groupLineRows(line, inner)
		if n < 0 || !groupLineClosesSGR(line) {
			return full
		}
		counts[i] = n
		total += n
	}
	if total != len(rows) {
		return full
	}
	cache.lines = make(map[string]string, len(src))
	pos := 0
	for i, line := range src {
		cache.lines[line] = strings.Join(rows[pos:pos+counts[i]], "\n")
		pos += counts[i]
	}
	return full
}

// groupLineRows returns how many rows lipgloss draws for one content line at
// the given inner width, or -1 when the line holds a tab or carriage return,
// which lipgloss rewrites before it wraps.
func groupLineRows(line string, inner int) int {
	if strings.ContainsAny(line, "\t\r") {
		return -1
	}
	if ansi.StringWidth(line) <= inner {
		return 1
	}
	return strings.Count(ansi.Wrap(line, inner, ""), "\n") + 1
}

// groupBoxInterior returns the rows the side style draws for one content line,
// joined by "\n". A line that lipgloss wraps yields several rows.
func groupBoxInterior(line string, cache *groupBoxCache) string {
	return cache.side.Render(line)
}

// groupRowFits reports whether the first row of rows is exactly width columns.
// lipgloss widens the whole box when a line starts with a space and holds an
// unbreakable word longer than the inner width, so a wider first row means the
// chrome no longer matches and the caller draws the full box.
func groupRowFits(rows string, width int) bool {
	first, _, _ := strings.Cut(rows, "\n")
	return ansi.StringWidth(first) == width
}
