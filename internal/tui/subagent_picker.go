package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// subAgentPickerOverlay lists every sub-agent card in the conversation and jumps
// to the selected one. Rows come from a callback so the overlay never holds the
// content buffer.
type subAgentPickerOverlay struct {
	OverlayShell
	query        string
	rows         []subAgentPickerRow
	selection    int // index into rows; -1 when no card row exists
	scrollOffset int
	totalRunning int
	totalDone    int
	build        func(query string) []subAgentPickerRow
	styles       *theme.Styles
}

func newSubAgentPickerOverlay(styles *theme.Styles) subAgentPickerOverlay {
	return subAgentPickerOverlay{styles: styles, selection: -1}
}

func (s subAgentPickerOverlay) withDimensions(width, height int) subAgentPickerOverlay {
	s.OverlayShell = s.WithDimensions(width, height)
	return s
}

// Open shows the picker; build returns the rows for a query ("" = everything).
func (s subAgentPickerOverlay) Open(build func(query string) []subAgentPickerRow) subAgentPickerOverlay {
	s.OverlayShell = s.openShell()
	s.build = build
	s.query = ""
	s.scrollOffset = 0
	s.totalRunning, s.totalDone = 0, 0
	for _, r := range s.rebuild("").rows {
		if !r.selectable() {
			continue
		}
		if r.dd.status == "active" {
			s.totalRunning++
		} else {
			s.totalDone++
		}
	}
	return s.setQuery("")
}

func (s subAgentPickerOverlay) Close() subAgentPickerOverlay {
	s.OverlayShell = s.closeShell()
	s.build = nil
	s.rows = nil
	return s
}

func (s subAgentPickerOverlay) rebuild(query string) subAgentPickerOverlay {
	s.rows = nil
	if s.build != nil {
		s.rows = s.build(query)
	}
	return s
}

// setQuery rebuilds the rows and moves the selection to the first card row.
func (s subAgentPickerOverlay) setQuery(query string) subAgentPickerOverlay {
	s.query = query
	s = s.rebuild(query)
	s.selection = s.nextSelectable(-1, 1)
	s.scrollOffset = 0
	return s.keepSelectionVisible()
}

// nextSelectable returns the nearest card row index from start in direction dir,
// or the unchanged start when there is none (-1 when start is -1).
func (s subAgentPickerOverlay) nextSelectable(start, dir int) int {
	for i := start + dir; i >= 0 && i < len(s.rows); i += dir {
		if s.rows[i].selectable() {
			return i
		}
	}
	return start
}

// SelectedKey returns the occurrence key of the selected card row.
func (s subAgentPickerOverlay) SelectedKey() (occurrenceKey, bool) {
	if s.selection < 0 || s.selection >= len(s.rows) || !s.rows[s.selection].selectable() {
		return occurrenceKey{}, false
	}
	return s.rows[s.selection].key, true
}

// Update handles navigation and query edits. Esc, enter and alt+a are handled by the model.
func (s subAgentPickerOverlay) Update(msg tea.Msg) (subAgentPickerOverlay, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !s.IsOpen() {
		return s, nil
	}
	switch key.Code {
	case tea.KeyUp:
		s.selection = s.nextSelectable(s.selection, -1)
	case tea.KeyDown:
		s.selection = s.nextSelectable(s.selection, 1)
	case tea.KeyBackspace:
		if r := []rune(s.query); len(r) > 0 {
			s = s.setQuery(string(r[:len(r)-1]))
		}
		return s, nil
	default:
		if key.Text != "" && key.Mod&(tea.ModAlt|tea.ModCtrl) == 0 {
			s = s.setQuery(s.query + key.Text)
		}
		return s, nil
	}
	return s.keepSelectionVisible(), nil
}

// visibleRows is how many list lines fit, derived from the terminal height.
func (s subAgentPickerOverlay) visibleRows() int {
	return min(16, max(5, s.height-12))
}

// keepSelectionVisible scrolls so the selected row (and its heading, when it is
// the first row under one) sits inside the window.
func (s subAgentPickerOverlay) keepSelectionVisible() subAgentPickerOverlay {
	n := s.visibleRows()
	if s.selection < 0 {
		s.scrollOffset = 0
		return s
	}
	if s.selection < s.scrollOffset {
		s.scrollOffset = s.selection
	}
	if s.selection >= s.scrollOffset+n {
		s.scrollOffset = s.selection - n + 1
	}
	if s.scrollOffset > 0 && s.selection == s.scrollOffset {
		// Pull context headings above the first selected row into view.
		for s.scrollOffset > 0 && !s.rows[s.scrollOffset-1].selectable() && s.selection-s.scrollOffset+1 < n {
			s.scrollOffset--
		}
	}
	s.scrollOffset = max(0, min(s.scrollOffset, max(0, len(s.rows)-n)))
	return s
}
