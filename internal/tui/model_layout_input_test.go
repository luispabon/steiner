package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// cursorInLines converts the cursor row and column from renderTypedInputLines
// or renderInputLines into a (display row, display column) for verification.
// The display row is adjusted from the returned cursorRow to account for
// windowing in renderNormalInputView.
func cursorInLines(t *testing.T, lines []string, cursorRow, cursorCol int) (row, col int) {
	t.Helper()
	if cursorRow < 0 || cursorRow >= len(lines) {
		t.Fatalf("cursorRow %d out of range [0, %d)", cursorRow, len(lines))
	}
	return cursorRow, cursorCol
}

// TestComposerLayoutAcrossInputStates pins the composer layout (input rows,
// viewport height, and rendered cursor position) across input states. These
// values derive from steiner's own wrap of m.input.Value() and must not depend
// on the textarea's internal width; the same expectations hold before and after
// the width change.
func TestComposerLayoutAcrossInputStates(t *testing.T) {
	t.Parallel()
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

			// Expected rendered rows: wrap each logical line at innerWidth.
			// All non-placeholder fixtures are pure-x lines, so wrapping breaks
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

			lines, isPlaceholder, cursorRow, cursorCol := m.renderInputLines(innerWidth)
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
			row, col := cursorInLines(t, lines, cursorRow, cursorCol)
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	val := strings.Repeat("x", 100)
	m.input.SetValue(val)

	for _, width := range []int{36, 200, 99999} {
		m.input.MaxWidth = 0
		m.input.SetWidth(width)
		m.input.SetCursorColumn(54)
		lines, cursorRow, cursorCol := m.renderTypedInputLines(36)
		row, col := cursorInLines(t, lines, cursorRow, cursorCol)
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
	t.Parallel()
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
	lines, cursorRow, cursorCol := m.renderTypedInputLines(m.inputInnerWidth(m.contentWidth()))
	row, col := cursorInLines(t, lines, cursorRow, cursorCol)
	if row != 1 || col != 0 {
		t.Fatalf("rendered cursor at row %d col %d, want row 1 col 0", row, col)
	}
}

// assertComposerRowsMatchTextarea fails when the composer draws different rows
// than the textarea soft-wraps a single-line value into, when it puts the caret
// on a different row than the textarea does, or when a drawn row is wider than
// the composer's inner width.
func assertComposerRowsMatchTextarea(t *testing.T, m *Model) {
	t.Helper()
	width := m.inputInnerWidth(m.contentWidth())
	lines, cursorRow, _ := m.renderTypedInputLines(width)
	info := m.input.LineInfo()
	if len(lines) != info.Height {
		t.Fatalf("drawn rows = %d, want %d (textarea soft-wrapped rows)", len(lines), info.Height)
	}
	if cursorRow != info.RowOffset {
		t.Fatalf("drawn cursor row = %d, want %d (textarea caret row)", cursorRow, info.RowOffset)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("drawn row %d width = %d, want <= %d", i, got, width)
		}
	}
}

// TestComposerWrapMatchesTextareaAtExactWidthBoundary pins the textarea's
// reserved-space rule: a line that exactly fills the composer width wraps into
// an extra reserved row, so the caret at the end sits on that row and Up steps
// onto the text row before history recall can start.
func TestComposerWrapMatchesTextareaAtExactWidthBoundary(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	draft := strings.Repeat("x", m.inputInnerWidth(m.contentWidth()))
	m.input.SetValue(draft)
	m.input.CursorEnd()

	if got := m.input.LineInfo().Height; got != 2 {
		t.Fatalf("textarea rows = %d, want 2 (an exact-width line reserves a trailing row)", got)
	}
	assertComposerRowsMatchTextarea(t, m)

	loadComposerHistory(m, "most recent prompt")

	// The caret starts on the reserved row, so the first Up steps onto the text.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.input.Value(); got != draft {
		t.Fatalf("Value() = %q, want unchanged draft %q (a reserved row sat below the caret)", got, draft)
	}
	assertComposerRowsMatchTextarea(t, m)

	// Nothing is above the caret any more, so the next Up recalls.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.input.Value(); got != "most recent prompt" {
		t.Fatalf("Value() = %q, want %q (recall from the top textarea row)", got, "most recent prompt")
	}
}

// TestComposerWrapMatchesTextareaWithTrailingSpaceAndOverflow pins the wrap on
// text that ends in whitespace and overflows the width: rows keep the textarea's
// break points and caret rows, and no drawn row exceeds the inner width.
func TestComposerWrapMatchesTextareaWithTrailingSpaceAndOverflow(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	draft := strings.Repeat("ab cd ", 12)
	m.input.SetValue(draft)

	for _, col := range []int{0, 5, 35, 36, 71, len([]rune(draft))} {
		m.input.SetCursorColumn(col)
		assertComposerRowsMatchTextarea(t, m)
	}
	if got := m.input.LineInfo().Height; got != 3 {
		t.Fatalf("textarea rows = %d, want 3 (trailing space reserves a row)", got)
	}
}

// TestComposerUpWithSpacedDraftStepsThroughTextareaRows pins that the composer
// draws the same rows the textarea navigates for text with spaces: each Up
// while a soft-wrapped row remains above the caret moves the caret up one
// textarea row and one drawn row, and history recall only starts once the caret
// is on the top row of the top logical line.
func TestComposerUpWithSpacedDraftStepsThroughTextareaRows(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	draft := "the quick brown fox jumps over the lazy dog and keeps on running"
	m.input.SetValue(draft)
	m.input.CursorEnd()
	loadComposerHistory(m, "most recent prompt")

	if got := m.input.LineInfo().RowOffset; got == 0 {
		t.Fatalf("test setup invalid: caret starts on the top textarea row")
	}

	for {
		wantRow := m.input.LineInfo().RowOffset - 1
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

		if got := m.input.LineInfo().RowOffset; got != wantRow {
			t.Fatalf("textarea row = %d, want %d after Up", got, wantRow)
		}
		if got := m.input.Value(); got != draft {
			t.Fatalf("Value() = %q, want unchanged draft %q (a row remained above the caret)", got, draft)
		}
		if got := m.fileHistoryIdx; got != -1 {
			t.Fatalf("fileHistoryIdx = %d, want -1", got)
		}
		_, cursorRow, _ := m.renderTypedInputLines(m.inputInnerWidth(m.contentWidth()))
		if cursorRow != wantRow {
			t.Fatalf("drawn cursor row = %d, want %d (drawn rows must match textarea rows)", cursorRow, wantRow)
		}
		if wantRow == 0 {
			break
		}
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "most recent prompt" {
		t.Fatalf("Value() = %q, want %q (recall only from the top textarea row)", got, "most recent prompt")
	}
	if got := m.fileHistoryIdx; got != 0 {
		t.Fatalf("fileHistoryIdx = %d, want 0", got)
	}
}

// TestComposerUpNavigatesVisualRowsWithHistoryPresent pins the boundary of the
// history-recall gate once history is loaded: recall only happens from the top
// visual row. m.input.Line() counts logical lines split on "\n", not wrapped
// display rows, so a single long logical line reports Line() == 0 no matter
// which wrapped row the caret visually sits on. Keying the gate off the rendered
// row keeps Up moving the caret within such a draft instead of jumping into
// history.
func TestComposerUpNavigatesVisualRowsWithHistoryPresent(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	val := strings.Repeat("x", 100)
	m.input.SetValue(val)
	m.input.SetCursorColumn(72)

	loadComposerHistory(m, "most recent prompt")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != val {
		t.Fatalf("Value() = %q, want unchanged draft %q (caret was not on the top visual row)", got, val)
	}
	if got := m.fileHistoryIdx; got != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1 (recall must not start off the top visual row)", got)
	}
	// The cursor ends one visual row up, at the start of the second wrapped row.
	if got := m.input.Column(); got != 36 {
		t.Fatalf("cursor column = %d, want 36 (start of second wrapped row)", got)
	}
}

// TestComposerUpAtTopVisualRowRecallsHistoryWithHistoryPresent covers the other
// side of the gate: the same wrapped draft recalls history once the caret sits
// on the top visual row.
func TestComposerUpAtTopVisualRowRecallsHistoryWithHistoryPresent(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	val := strings.Repeat("x", 100)
	m.input.SetValue(val)
	m.input.SetCursorColumn(0)

	loadComposerHistory(m, "most recent prompt")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "most recent prompt" {
		t.Fatalf("Value() = %q, want history entry %q (caret on the top visual row)", got, "most recent prompt")
	}
	if got := m.fileHistoryIdx; got != 0 {
		t.Fatalf("fileHistoryIdx = %d, want 0", got)
	}
	if got := m.historyDraft; got != val {
		t.Fatalf("historyDraft = %q, want %q", got, val)
	}
}
