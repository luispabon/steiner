package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// loadComposerHistory dispatches a HistoryLoadedEvent so that m.fileHistory[0]
// ends up as the most-recently-used prompt (applyEvent reverses the payload
// order internally), matching what a real history load produces.
func loadComposerHistory(m *Model, mostRecentFirst ...string) {
	oldestFirst := make([]string, len(mostRecentFirst))
	for i, p := range mostRecentFirst {
		oldestFirst[len(mostRecentFirst)-1-i] = p
	}
	_ = m.applyEvent(output.NewHistoryLoadedEvent(oldestFirst))
}

func TestHandleKeyUpEmptyComposerRecallsHistory(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "most recent", "older")

	if got := m.input.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}
	if got := m.input.LineCount(); got != 1 {
		t.Fatalf("LineCount() = %d, want 1", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "most recent" {
		t.Fatalf("Value() = %q, want %q", got, "most recent")
	}
	if got := m.fileHistoryIdx; got != 0 {
		t.Fatalf("fileHistoryIdx = %d, want 0", got)
	}
}

func TestHandleKeyUpSecondPressPagesToOlderEntry(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "most recent", "older")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != m.fileHistory[1] {
		t.Fatalf("Value() = %q, want %q", got, m.fileHistory[1])
	}
	if got := m.fileHistoryIdx; got != 1 {
		t.Fatalf("fileHistoryIdx = %d, want 1", got)
	}
}

func TestHandleKeyUpSingleLineDraftAtColumnZeroRecallsHistory(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "most recent", "older")

	m.input.SetValue("draft")
	m.input.SetCursorColumn(0)
	if got := m.input.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "most recent" {
		t.Fatalf("Value() = %q, want %q", got, "most recent")
	}
	if got := m.historyDraft; got != "draft" {
		t.Fatalf("historyDraft = %q, want %q", got, "draft")
	}
}

func TestHandleKeyUpMultilineDraftCursorOnTopLineRecallsHistory(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "most recent", "older")

	m.input.SetValue("line one\nline two\nline three")
	for m.input.Line() > 0 {
		m.input.CursorUp()
	}
	if got := m.input.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "most recent" {
		t.Fatalf("Value() = %q, want %q", got, "most recent")
	}
}

func TestHandleKeyUpMultilineDraftCursorNotOnTopLineMovesCursor(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "most recent", "older")

	original := "line one\nline two\nline three"
	m.input.SetValue(original)
	m.input.CursorEnd()
	startLine := m.input.Line()
	if startLine == 0 {
		t.Fatalf("Line() = %d, want nonzero before pressing up", startLine)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != original {
		t.Fatalf("Value() = %q, want unchanged %q", got, original)
	}
	if got := m.fileHistoryIdx; got != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1 (unchanged)", got)
	}
	if got := m.input.Line(); got != startLine-1 {
		t.Fatalf("Line() = %d, want %d (moved up one row)", got, startLine-1)
	}
}

func TestHandleKeyUpConsecutivePressesKeepPagingThroughMultilineEntry(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "multi\nline\nentry", "older single line")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "older single line" {
		t.Fatalf("Value() = %q, want %q (consecutive Up must keep paging, not get stuck moving within the recalled entry)", got, "older single line")
	}
	if got := m.fileHistoryIdx; got != 1 {
		t.Fatalf("fileHistoryIdx = %d, want 1", got)
	}
}

func TestHandleKeyLeftAfterRecallExitsBrowseAndUpThenMovesCursor(t *testing.T) {
	m := newModel(Config{}, nil)
	loadComposerHistory(m, "multi\nline\nentry", "older single line")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})

	if got := m.fileHistoryIdx; got != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1 (Left must exit history browse)", got)
	}
	lineBeforeUp := m.input.Line()
	if lineBeforeUp == 0 {
		t.Fatalf("test setup invalid: cursor already on line 0 after Left")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got := m.input.Value(); got != "multi\nline\nentry" {
		t.Fatalf("Value() = %q, want unchanged %q (Up should move the cursor, not recall further history)", got, "multi\nline\nentry")
	}
	if got := m.input.Line(); got != lineBeforeUp-1 {
		t.Fatalf("Line() = %d, want %d (moved up one row)", got, lineBeforeUp-1)
	}
	if got := m.fileHistoryIdx; got != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1", got)
	}
}

func TestHandleKeyDownMultilineDraftCursorOnTopLineMovesCursor(t *testing.T) {
	m := newModel(Config{}, nil)

	original := "line one\nline two\nline three"
	m.input.SetValue(original)
	for m.input.Line() > 0 {
		m.input.CursorUp()
	}
	startLine := m.input.Line()
	if startLine != 0 {
		t.Fatalf("Line() = %d, want 0", startLine)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	if got := m.input.Value(); got != original {
		t.Fatalf("Value() = %q, want unchanged %q", got, original)
	}
	if got := m.fileHistoryIdx; got != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1", got)
	}
	if got := m.input.Line(); got != startLine+1 {
		t.Fatalf("Line() = %d, want %d (moved down one row)", got, startLine+1)
	}
}
