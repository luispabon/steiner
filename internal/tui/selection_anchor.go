package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selectionAnchor pins a selection endpoint to a specific rendered row of a
// content segment. rowText is the ansi.Strip'ed text of that row at anchor
// time, so the endpoint can be remapped onto the same text row after a
// same-width content change moves rows around.
type selectionAnchor struct {
	segIndex  int
	rowInSeg  int
	rowText   string // ansi.Strip of the rendered line at anchor time
	renderGen int    // records contentSegment.renderGen at capture time
	ok        bool
}

// segmentRenderedLines returns the lines of a segment's cached render, split
// on newlines with trailing newlines trimmed. An empty or unrendered segment
// yields one empty line so row math stays consistent.
func (b *contentBuffer) segmentRenderedLines(segIndex int) []string {
	if segIndex < 0 || segIndex >= len(b.segments) {
		return nil
	}
	trimmed := strings.TrimRight(b.segments[segIndex].cachedRender, "\n")
	return strings.Split(trimmed, "\n")
}

// selectionAnchorForSegmentRow builds a selectionAnchor for a rendered row of
// a segment, recording the stripped row text at anchor time.
func (b *contentBuffer) selectionAnchorForSegmentRow(segIndex, rowInSeg int) selectionAnchor {
	lines := b.segmentRenderedLines(segIndex)
	if rowInSeg < 0 || rowInSeg >= len(lines) {
		return selectionAnchor{}
	}
	return selectionAnchor{
		segIndex:  segIndex,
		rowInSeg:  rowInSeg,
		rowText:   ansi.Strip(lines[rowInSeg]),
		renderGen: b.segments[segIndex].renderGen,
		ok:        true,
	}
}

// viewportSelectionEndpoint returns the content line and anchor to store for a
// viewport selection endpoint at line. Mappable lines anchor to their segment
// row. Blank unmappable lines (user separators, padding) snap to the nearest
// mappable line at capture time so the endpoint stays anchored when content
// above shifts; non-blank unmappable lines (e.g. the streaming preview) stay
// unanchored and clear on content change.
func (m *Model) viewportSelectionEndpoint(line int) (int, selectionAnchor) {
	if segIndex, rowInSeg, ok := m.content.segmentAtContentLine(line); ok {
		return line, m.content.selectionAnchorForSegmentRow(segIndex, rowInSeg)
	}
	lines := strings.Split(m.fmtBgCacheInput, "\n")
	if m.viewportLineBlank(lines, line) {
		if snapped, ok := m.nearestMappableContentLine(lines, line); ok {
			if segIndex, rowInSeg, ok := m.content.segmentAtContentLine(snapped); ok {
				return snapped, m.content.selectionAnchorForSegmentRow(segIndex, rowInSeg)
			}
		}
	}
	return line, selectionAnchor{}
}

// viewportLineBlank reports whether the rendered viewport content line is blank
// (empty or whitespace after ANSI stripping). Lines outside the rendered
// content count as blank so padding rows snap the same way.
func (m *Model) viewportLineBlank(lines []string, line int) bool {
	if line < 0 || line >= len(lines) {
		return true
	}
	return strings.TrimSpace(ansi.Strip(lines[line])) == ""
}

// nearestMappableContentLine returns the content line nearest to line that maps
// to a segment, preferring the line above on ties.
func (m *Model) nearestMappableContentLine(lines []string, line int) (int, bool) {
	maxLine := len(lines) - 1
	if maxLine < 0 {
		return 0, false
	}
	if line < 0 {
		line = 0
	}
	if line > maxLine {
		line = maxLine
	}
	for offset := 0; offset <= maxLine; offset++ {
		if up := line - offset; up >= 0 {
			if _, _, ok := m.content.segmentAtContentLine(up); ok {
				return up, true
			}
		}
		if down := line + offset; down <= maxLine {
			if _, _, ok := m.content.segmentAtContentLine(down); ok {
				return down, true
			}
		}
	}
	return 0, false
}

// remapViewportSelection re-anchors the stored viewport selection onto the
// current content after a same-width content change, so a live selection
// follows the text it was made against instead of being cleared. If either
// endpoint can no longer be matched (segment hidden, dropped, or ambiguous),
// the selection and any in-flight drag are cleared.
func (m *Model) remapViewportSelection() {
	if m.activeRegion != regionViewport || !m.selection.hasSelection() {
		return
	}
	if m.remapEndpoint(&m.selection.start, &m.selection.startAnchor) &&
		m.remapEndpoint(&m.selection.end, &m.selection.endAnchor) {
		return
	}
	m.clearViewportSelectionAndDrag()
}

// remapEndpoint moves a selection endpoint from its anchored segment row to the
// current content line of the same row text. Columns are left untouched because
// same-width content changes do not reflow rows. Returns false when the anchor
// is stale or the row text can no longer be located unambiguously. When the
// segment's render generation is unchanged, the rows are identical and only the
// absolute content line needs recomputing.
func (m *Model) remapEndpoint(p *selectionPoint, anchor *selectionAnchor) bool {
	if !anchor.ok {
		return false
	}
	if anchor.segIndex < 0 || anchor.segIndex >= len(m.content.segments) || m.content.isSegmentHidden(anchor.segIndex) {
		return false
	}
	seg := &m.content.segments[anchor.segIndex]
	newRow := anchor.rowInSeg
	if seg.renderGen != anchor.renderGen {
		lines := m.content.segmentRenderedLines(anchor.segIndex)
		var ok bool
		newRow, ok = matchRow(lines, anchor.rowText, anchor.rowInSeg)
		if !ok {
			return false
		}
	}
	line, ok := m.content.contentLineForSegmentRow(anchor.segIndex, newRow)
	if !ok {
		return false
	}
	anchor.rowInSeg = newRow
	anchor.renderGen = seg.renderGen
	p.line = line
	return true
}

// clearViewportSelectionAndDrag drops the viewport selection and cancels any
// in-flight drag (press, edge auto-scroll, and its tick epoch).
func (m *Model) clearViewportSelectionAndDrag() {
	if m.selection.hasSelection() {
		m.selection = m.selection.clear()
	}
	m.clearDragState()
}

// clearDragState cancels any in-flight mouse drag and bumps the tick epoch so
// stale auto-scroll ticks are ignored.
func (m *Model) clearDragState() {
	m.mousePressX = -1
	m.mousePressY = -1
	m.dragScrollDir = 0
	m.dragScrollTicking = false
	m.dragScrollEpoch++
}

// matchRow locates the rendered line whose stripped text equals rowText,
// preferring the anchor's old row and otherwise the nearest row, rejecting
// equidistant alternatives as ambiguous. Returns false when no row matches.
func matchRow(lines []string, rowText string, oldRow int) (int, bool) {
	if oldRow >= 0 && oldRow < len(lines) && ansi.Strip(lines[oldRow]) == rowText {
		return oldRow, true
	}
	best := -1
	bestDist := -1
	ambiguous := false
	for i, line := range lines {
		if ansi.Strip(line) != rowText {
			continue
		}
		d := i - oldRow
		if d < 0 {
			d = -d
		}
		if best == -1 || d < bestDist {
			best = i
			bestDist = d
			ambiguous = false
		} else if d == bestDist {
			ambiguous = true
		}
	}
	if best == -1 || ambiguous {
		return 0, false
	}
	return best, true
}

// anchorViewportSelectionEnd replaces the viewport selection end with its
// segment-anchored endpoint. Non-viewport regions keep their screen-anchored
// coordinates.
func (m *Model) anchorViewportSelectionEnd() {
	if m.activeRegion == regionViewport {
		m.selection.end.line, m.selection.endAnchor = m.viewportSelectionEndpoint(m.selection.end.line)
	}
}
