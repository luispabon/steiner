package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// renderMemo remembers the last rendered string of one overlay together with
// the key that produced it. The key must hold everything the render reads, so
// equal keys imply an identical string.
type renderMemo[K comparable] struct {
	valid bool
	key   K
	out   string
}

func (r *renderMemo[K]) lookup(key K) (string, bool) {
	if r.valid && r.key == key {
		return r.out, true
	}
	return "", false
}

func (r *renderMemo[K]) store(key K, out string) string {
	r.valid, r.key, r.out = true, key, out
	return out
}

// sliceID identifies a slice by its backing array start and length. Overlay
// state only ever replaces these slices (never writes elements in place), so
// the identity changes exactly when the content does. The key holds the
// pointer, which keeps the array alive and its address from being reused.
type sliceID[T any] struct {
	first *T
	n     int
}

func idOf[T any](s []T) sliceID[T] {
	if len(s) == 0 {
		return sliceID[T]{}
	}
	return sliceID[T]{first: &s[0], n: len(s)}
}

// overlayRenderMemos holds one memo per overlay whose View is worth skipping
// while its state is unchanged.
type overlayRenderMemos struct {
	slash     renderMemo[slashRenderKey]
	filePick  renderMemo[filePickerRenderKey]
	modelPick renderMemo[modelPickerRenderKey]
	fileList  renderMemo[fileListRenderKey]
	mcp       renderMemo[mcpRenderKey]
	lsp       renderMemo[lspRenderKey]
	context   contextRenderMemo
	help      renderMemo[helpRenderKey]
}

type slashRenderKey struct {
	OverlayShell
	query        string
	candidates   sliceID[slashOverlayItem]
	matchIndexes sliceID[slashOverlayMatch]
	selection    int
	scrollOffset int
	styles       *theme.Styles
}

func (s slashOverlay) renderKey() slashRenderKey {
	return slashRenderKey{s.OverlayShell, s.query, idOf(s.candidates), idOf(s.matchIndexes), s.selection, s.scrollOffset, s.styles}
}

func (s *slashOverlay) memoView(m *renderMemo[slashRenderKey]) string {
	key := s.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, s.View())
}

type filePickerRenderKey struct {
	OverlayShell
	query        string
	candidates   sliceID[string]
	matchIndexes sliceID[[]int]
	selection    int
	scrollOffset int
	styles       *theme.Styles
}

func (f filePickerOverlay) renderKey() filePickerRenderKey {
	return filePickerRenderKey{f.OverlayShell, f.query, idOf(f.candidates), idOf(f.matchIndexes), f.selection, f.scrollOffset, f.styles}
}

func (f *filePickerOverlay) memoView(m *renderMemo[filePickerRenderKey]) string {
	key := f.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, f.View())
}

type modelPickerRenderKey struct {
	OverlayShell
	query        string
	candidates   sliceID[ModelEntry]
	matchIndexes sliceID[[]int]
	selection    int
	scrollOffset int
	currentModel string
	title        string
	mode         modelPickerMode
	styles       *theme.Styles
}

func (p modelPickerOverlay) renderKey() modelPickerRenderKey {
	return modelPickerRenderKey{p.OverlayShell, p.query, idOf(p.candidates), idOf(p.matchIndexes), p.selection, p.scrollOffset, p.currentModel, p.title, p.mode, p.styles}
}

func (p *modelPickerOverlay) memoView(m *renderMemo[modelPickerRenderKey]) string {
	key := p.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, p.View())
}

type fileListRenderKey struct {
	OverlayShell
	root    string
	entries sliceID[string]
	styles  *theme.Styles
}

func (f fileListOverlay) renderKey() fileListRenderKey {
	return fileListRenderKey{f.OverlayShell, f.root, idOf(f.entries), f.styles}
}

func (f *fileListOverlay) memoView(m *renderMemo[fileListRenderKey]) string {
	key := f.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, f.View())
}

type mcpRenderKey struct {
	OverlayShell
	lines        sliceID[string]
	scrollOffset int
	styles       *theme.Styles
}

func (o mcpOverlay) renderKey() mcpRenderKey {
	return mcpRenderKey{o.OverlayShell, idOf(o.lines), o.scrollOffset, o.styles}
}

func (o *mcpOverlay) memoView(m *renderMemo[mcpRenderKey]) string {
	key := o.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, o.View())
}

type lspRenderKey struct {
	OverlayShell
	lines        sliceID[string]
	scrollOffset int
	styles       *theme.Styles
}

func (o lspOverlay) renderKey() lspRenderKey {
	return lspRenderKey{o.OverlayShell, idOf(o.lines), o.scrollOffset, o.styles}
}

func (o *lspOverlay) memoView(m *renderMemo[lspRenderKey]) string {
	key := o.renderKey()
	if out, ok := m.lookup(key); ok {
		return out
	}
	return m.store(key, o.View())
}

type helpRenderKey struct {
	styles *theme.Styles
	width  int
}

// renderHelpMemo renders the help panel once per (styles, width).
func (m *Model) renderHelpMemo(width int) string {
	key := helpRenderKey{m.styles, width}
	if out, ok := m.overlayMemos.help.lookup(key); ok {
		return out
	}
	return m.overlayMemos.help.store(key, renderHelp(m.styles, width))
}

// contextRenderKey covers everything buildContextOverlay reads: the shell with
// the model's current dimensions, the title, the reflowed lines, the scroll
// position and the model's styles.
type contextRenderKey struct {
	OverlayShell
	title         string
	renderedLines sliceID[string]
	scrollOffset  int
	styles        *theme.Styles
}

func (m *Model) contextRenderKey() contextRenderKey {
	s := &m.contextOverlay
	return contextRenderKey{
		OverlayShell:  s.WithDimensions(m.width, m.height),
		title:         s.title,
		renderedLines: idOf(s.renderedLines),
		scrollOffset:  s.scrollOffset,
		styles:        m.styles,
	}
}

// contextMemoSlots is how many recent scroll positions the context overlay
// keeps rendered, so scrolling back over lines just read is free.
const contextMemoSlots = 4

type contextMemoEntry struct {
	valid bool
	key   contextRenderKey
	out   string
	w, h  int
}

// contextRenderMemo keeps the last few renders of the context overlay with
// their measured cell size, plus the styled-line cache they share.
type contextRenderMemo struct {
	entries [contextMemoSlots]contextMemoEntry
	next    int
	styled  contextStyledLines
}

func (c *contextRenderMemo) lookup(key contextRenderKey) *contextMemoEntry {
	for i := range c.entries {
		if e := &c.entries[i]; e.valid && e.key == key {
			return e
		}
	}
	return nil
}

func (c *contextRenderMemo) store(key contextRenderKey, out string, w, h int) {
	c.entries[c.next] = contextMemoEntry{valid: true, key: key, out: out, w: w, h: h}
	c.next = (c.next + 1) % contextMemoSlots
}

// contextStyledLines caches the per-line styling of the report body so a
// scroll step only styles lines it has not styled before at this width.
type contextStyledLines struct {
	lines sliceID[string]
	width int
	out   []string
	done  []bool
}

func (c *contextStyledLines) reset(lines []string, width int) {
	id := idOf(lines)
	if c.lines == id && c.width == width && len(c.out) == len(lines) {
		return
	}
	c.lines, c.width = id, width
	c.out = make([]string, len(lines))
	c.done = make([]bool, len(lines))
}

func (c *contextStyledLines) line(i int, text string) string {
	if !c.done[i] {
		c.out[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(c.width).Render(text)
		c.done[i] = true
	}
	return c.out[i]
}
