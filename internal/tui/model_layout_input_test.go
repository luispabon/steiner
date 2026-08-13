package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// cursorInLines locates the rendered cursor marker (█) in the composer lines
// returned by renderTypedInputLines / renderInputLines and reports its
// (row, col) position.
func cursorInLines(t *testing.T, lines []string) (row, col int) {
	t.Helper()
	for i, line := range lines {
		if idx := strings.Index(line, "█"); idx >= 0 {
			return i, idx
		}
	}
	t.Fatal("no cursor marker found in rendered composer lines")
	return -1, -1
}

// TestComposerLayoutAcrossInputStates pins the composer layout (input rows,
// viewport height, and rendered cursor position) across input states. These
// values derive from steiner's own hardwrap of m.input.Value() and must not
// depend on the textarea's internal width; the same expectations hold before
// and after the width change.
func TestComposerLayoutAcrossInputStates(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		value         string
		cursorLine    int
		curCol        int
		placeholder   bool
	}{
		{"empty input shows placeholder", 220, 60, "", 0, 0, true},
		{"single line", 220, 60, "hello", 0, 5, false},
		{"wrapped long line", 220, 60, strings.Repeat("x", 500), 0, 500, false},
		{"multi-line with explicit newlines", 220, 60, "aa\nbb\ncc", 1, 2, false},
		{"longer than max visible input lines", 220, 60, strings.TrimRight(strings.Repeat("x\n", 60), "\n"), 59, 1, false},
		{"narrow terminal placeholder", 30, 10, "", 0, 0, true},
		{"narrow terminal wrapped line", 30, 10, strings.Repeat("x", 100), 0, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: tt.width, Height: tt.height})

			m.input.SetValue(tt.value)
			m.input.SetCursorColumn(tt.curCol)
			for m.input.Line() > tt.cursorLine {
				m.input.CursorUp()
			}

			m.relayoutInput()

			innerWidth := m.inputInnerWidth(m.contentWidth())
			maxVisible := m.maxVisibleInputLines(m.contentWidth())

			// Expected rendered rows: hardwrap each logical line at innerWidth.
			// All non-placeholder fixtures are pure-x lines, so hardwrap breaks
			// them at exact innerWidth boundaries.
			wantRows := 0
			if tt.placeholder {
				wantRows = len(renderPlaceholderLines(m.input.Placeholder, innerWidth))
			} else {
				for _, line := range strings.Split(tt.value, "\n") {
					wantRows += max(1, (len(line)+innerWidth-1)/innerWidth)
				}
			}
			visibleRows := wantRows
			if !tt.placeholder {
				visibleRows = min(visibleRows, maxVisible)
			}
			wantInputRows := visibleRows + 2*inputPadY
			wantViewportH := max(1, tt.height-3-wantInputRows-1)

			inputRows, activityRows := m.computeInputRows(m.contentWidth())
			if inputRows != wantInputRows {
				t.Fatalf("inputRows = %d, want %d (activityRows=%d, inputChromeHeight=%d, wantRows=%d)",
					inputRows, wantInputRows, activityRows, m.inputChromeHeight(m.contentWidth()), wantRows)
			}
			if got := m.viewport.Height(); got != wantViewportH {
				t.Fatalf("viewport height = %d, want %d", got, wantViewportH)
			}

			lines, isPlaceholder, _ := m.renderInputLines(innerWidth)
			if isPlaceholder != tt.placeholder {
				t.Fatalf("isPlaceholder = %v, want %v", isPlaceholder, tt.placeholder)
			}
			if len(lines) != wantRows {
				t.Fatalf("rendered lines = %d, want %d", len(lines), wantRows)
			}
			if tt.placeholder {
				return
			}

			// Cursor: the absolute rune offset within the logical line maps onto
			// hardwrapped segments at exact innerWidth boundaries.
			valueLines := strings.Split(tt.value, "\n")
			wantCursorRow := tt.curCol / innerWidth
			for i := 0; i < tt.cursorLine; i++ {
				wantCursorRow += max(1, (len(valueLines[i])+innerWidth-1)/innerWidth)
			}
			wantCursorCol := tt.curCol % innerWidth
			row, col := cursorInLines(t, lines)
			if row != wantCursorRow || col != wantCursorCol {
				t.Fatalf("cursor at row %d col %d, want row %d col %d", row, col, wantCursorRow, wantCursorCol)
			}
		})
	}
}

// TestComposerCursorPlacementIndependentOfTextareaWidth pins the invariant the
// width fix relies on: cursor placement derives from m.input.Column(), so it
// must be identical no matter what internal wrap width the textarea runs at.
// Before the fix, a narrow width made LineInfo().ColumnOffset row-relative and
// the cursor was placed in the wrong hardwrapped segment.
func TestComposerCursorPlacementIndependentOfTextareaWidth(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	val := strings.Repeat("x", 100)
	m.input.SetValue(val)

	for _, width := range []int{36, 200, 99999} {
		m.input.MaxWidth = 0
		m.input.SetWidth(width)
		m.input.SetCursorColumn(54)
		lines, _ := m.renderTypedInputLines(36)
		row, col := cursorInLines(t, lines)
		if row != 1 || col != 18 {
			t.Fatalf("width %d: cursor at row %d col %d, want row 1 col 18", width, row, col)
		}
	}
}

// TestComposerUpDownNavigatesVisualRows pins the one deliberate behaviour
// change from running the textarea at its natural width: up/down move through
// the hardwrapped rows the composer renders instead of jumping to the previous
// logical line. Before the fix, pressing up at the start of the third wrapped
// row jumped to the start of the logical line (the textarea could not see the
// wrap), which disagreed with what was rendered.
func TestComposerUpDownNavigatesVisualRows(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	val := strings.Repeat("x", 100)
	m.input.SetValue(val)
	m.input.SetCursorColumn(72) // start of third hardwrapped row (36 + 36)

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Line(); got != 0 {
		t.Fatalf("cursor line = %d, want 0 (up stayed within the wrapped logical line)", got)
	}
	// The cursor ends one visual row up, at the start of the second wrapped row.
	if got := m.input.Column(); got != 36 {
		t.Fatalf("cursor column = %d, want 36 (start of second wrapped row)", got)
	}
	lines, _ := m.renderTypedInputLines(m.inputInnerWidth(m.contentWidth()))
	row, col := cursorInLines(t, lines)
	if row != 1 || col != 0 {
		t.Fatalf("rendered cursor at row %d col %d, want row 1 col 0", row, col)
	}
}
