package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// highlightRowKey holds every input that determines one highlighted row.
// start and end apply only when atStart or atEnd mark the row as the first or
// last selected row; interior rows span the whole line.
type highlightRowKey struct {
	line           string
	atStart, atEnd bool
	start, end     int
	left, right    int
}

type highlightRowEntry struct {
	key highlightRowKey
	out string
}

// highlightCache remembers the highlighted form of each screen row so a drag
// only re-renders the rows whose line or selected column span changed. The
// caller must reset it when the selection style changes.
type highlightCache struct {
	rows []highlightRowEntry
}

func (c *highlightCache) reset() { c.rows = c.rows[:0] }

// apply is applyScreenHighlight with per-row memoisation.
func (c *highlightCache) apply(frame string, state selectionState, selStyle lipgloss.Style, regionLeft, regionRight int) string {
	if !state.hasSelection() {
		return frame
	}
	start, end := state.canonical()
	lines := strings.Split(frame, "\n")
	if len(c.rows) < len(lines) {
		c.rows = append(c.rows, make([]highlightRowEntry, len(lines)-len(c.rows))...)
	}
	for i, line := range lines {
		if i < start.line || i > end.line {
			continue
		}
		key := highlightRowKey{line: line, left: regionLeft, right: regionRight}
		if i == start.line {
			key.atStart, key.start = true, start.col
		}
		if i == end.line {
			key.atEnd, key.end = true, end.col
		}
		if e := &c.rows[i]; e.key == key {
			lines[i] = e.out
			continue
		}
		out := highlightRow(line, key, selStyle)
		c.rows[i] = highlightRowEntry{key: key, out: out}
		lines[i] = out
	}
	return strings.Join(lines, "\n")
}

func highlightRow(line string, key highlightRowKey, selStyle lipgloss.Style) string {
	lineWidth := ansi.StringWidth(line)
	startCol, endCol := 0, lineWidth
	if key.atStart {
		startCol = key.start
	}
	if key.atEnd {
		endCol = key.end
	}
	if key.right > 0 {
		startCol = max(key.left, startCol)
		endCol = min(key.right, endCol)
	}
	if startCol >= endCol {
		return line
	}
	before := ansi.Cut(line, 0, startCol)
	mid := selStyle.Render(ansi.Strip(ansi.Cut(line, startCol, endCol)))
	after := ansi.Cut(line, endCol, lineWidth)
	return before + mid + after
}
