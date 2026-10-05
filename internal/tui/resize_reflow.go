package tui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// resizeReflowDelay is how long the content reflow waits after the last
// width-changing resize. A drag delivers a burst of widths, each of which
// would otherwise render every segment afresh.
const resizeReflowDelay = 80 * time.Millisecond

// resizeReflowFiredMsg ends a resize burst; seq guards against a newer resize.
type resizeReflowFiredMsg struct{ seq int }

func resizeReflowCmd(seq int) tea.Cmd {
	return tea.Tick(resizeReflowDelay, func(time.Time) tea.Msg {
		return resizeReflowFiredMsg{seq: seq}
	})
}

// resizeReflow is the deferred-reflow state. While pending, the viewport has
// its new width but content keeps rendering at renderWidth; the visible window
// is adapted to the viewport when drawn (adaptedWindow).
type resizeReflow struct {
	pending bool
	seq     int
	// renderWidth is the width content is rendered at while pending.
	renderWidth int
	// target is the viewport width the pending reflow will render at.
	target int
}

// contentRenderWidth is the width the transcript is rendered at: the
// viewport width, or the pre-resize width while a reflow is pending.
func (m *Model) contentRenderWidth() int {
	if m.reflow.pending {
		return m.reflow.renderWidth
	}
	return m.viewport.Width()
}

// planReflow decides, after the viewport was resized, whether the content
// reflow runs now or is deferred. Only a window resize to a width that some
// segment has no cached render for is deferred; any other width change
// reflows synchronously and cancels a pending reflow. It returns the timer
// to arm when it defers.
func (m *Model) planReflow(resize bool) tea.Cmd {
	w := m.viewport.Width()
	r := &m.reflow
	if r.pending && w == r.renderWidth {
		m.cancelReflow()
		return nil
	}
	if !resize {
		if r.pending && w != r.target {
			m.cancelReflow()
		}
		return nil
	}
	if r.pending && w == r.target {
		return nil
	}
	if w == m.bgFormat.width || m.bgFormat.width == 0 {
		return nil
	}
	if m.content.cachedAtWidth(w) {
		m.cancelReflow()
		return nil
	}
	if !r.pending {
		r.pending = true
		r.renderWidth = m.bgFormat.width
	}
	r.target = w
	r.seq++
	return resizeReflowCmd(r.seq)
}

func (m *Model) cancelReflow() {
	if m.reflow.pending {
		m.reflow.pending = false
		m.reflow.seq++
	}
}

func (m *Model) handleResizeReflowFiredMsg(msg resizeReflowFiredMsg) (tea.Model, tea.Cmd) {
	if !m.reflow.pending || msg.seq != m.reflow.seq {
		return m, nil
	}
	anchorSeg, anchorRow, anchored := m.scrollAnchor()
	m.cancelReflow()
	m.syncViewport()
	if anchored {
		if line, ok := m.content.contentLineForSegmentRow(anchorSeg, anchorRow); ok {
			m.viewport.SetYOffset(line + m.contentTopPad)
		} else if line, ok := m.content.contentLineForSegmentRow(anchorSeg, 0); ok {
			m.viewport.SetYOffset(line + m.contentTopPad)
		}
	}
	return m, nil
}

// scrollAnchor returns the segment row at the top of a scrolled-up viewport.
func (m *Model) scrollAnchor() (seg, row int, ok bool) {
	if m.autoScroll || m.contentTopPad > 0 {
		return 0, 0, false
	}
	return m.content.segmentAtContentLine(m.viewport.YOffset())
}

// adaptedWindow returns viewport lines [start, end) for drawing while a
// reflow is pending. The viewport holds the transcript formatted at the
// pre-resize width; the window's content lines are re-cut to the viewport
// width and re-padded by the normal background formatter, so every cell keeps
// its explicit background. Only the window is adapted: cutting the whole
// transcript on every resize of a drag would cost a large share of a reflow.
func (m *Model) adaptedWindow(start, end int) []string {
	out := slices.Clone(m.viewport.Lines()[start:end])
	vw := m.viewport.Width()
	bg := m.resolvedPalette().ContentBG
	from := max(start-m.contentTopPad, 0)
	to := end - m.contentTopPad
	body := min(to, m.bgFormat.bodyLines)
	if from < body {
		raw := m.bgFormat.rawLines(from, body)
		for i, l := range raw {
			raw[i] = ansi.Truncate(l, vw, "")
		}
		formatted := theme.FormatBody(strings.Join(raw, "\n"), vw, bg)
		for i := from; i < body; i++ {
			line, after, _ := strings.Cut(formatted, "\n")
			out[i+m.contentTopPad-start] = strings.TrimSuffix(line, "\r")
			formatted = after
		}
	}
	if to > body {
		trailing := theme.FormatContent("", vw, bg)
		for i := max(from, body); i < to; i++ {
			out[i+m.contentTopPad-start] = trailing
		}
	}
	return out
}

// cachedAtWidth reports whether every visible segment can be rendered at
// width from a cached render without calling the segment renderer: its active
// render or a current alternate is for width, and nothing forces a re-render.
func (b *contentBuffer) cachedAtWidth(width int) bool {
	for i := range b.segments {
		if b.isSegmentHidden(i) {
			continue
		}
		seg := &b.segments[i]
		if seg.renderDirty || segmentHasActiveDelegation(seg) || segmentHasActiveCompaction(seg) {
			return false
		}
		if seg.cachedRender != "" && seg.cachedRenderWidth == width {
			continue
		}
		if !b.hasAltRender(seg, width) {
			return false
		}
	}
	return true
}

func (b *contentBuffer) hasAltRender(seg *contentSegment, width int) bool {
	for _, alt := range seg.altRenders {
		if alt.rendered != "" && alt.width == width && b.stampCurrent(seg.kind, alt.stamp) {
			return true
		}
	}
	return false
}

func segmentHasActiveCompaction(seg *contentSegment) bool {
	return seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished
}
