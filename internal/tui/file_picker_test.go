package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFilePickerOverlay_NewClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	if f.open {
		t.Fatal("expected picker to start closed")
	}

	f = f.Open(".")
	if !f.open {
		t.Fatal("expected picker to be open after Open()")
	}
	if len(f.allEntries) == 0 {
		t.Fatal("expected non-empty entries from walking current dir")
	}

	f = f.Close()
	if f.open {
		t.Fatal("expected picker to be closed after Close()")
	}
}

func TestFilePickerOverlay_ViewEmpty(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	view := f.View()
	if view != "" {
		t.Fatal("expected empty view when closed")
	}
}

func TestFilePickerOverlay_ViewNonEmpty(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.width = 80
	f.height = 24
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected picker to be open")
	}
	view := f.View()
	if view == "" {
		t.Fatal("expected non-empty view when open")
	}
}

func TestFilePickerOverlay_QueryFiltersCandidates(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected picker to be open")
	}

	initialCount := len(f.candidates)
	if initialCount == 0 {
		t.Skip("no entries to filter")
	}

	f.query = ".go"
	f.filter()
	if len(f.candidates) == 0 {
		t.Fatal("expected at least one candidate matching .go")
	}

	f.query = "nonexistent_marker_xyzzy"
	f.filter()
	if len(f.candidates) != 0 {
		t.Fatal("expected zero candidates for non-matching query")
	}
}

func TestFilePickerOverlay_EmptyQueryShowsAll(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	allCount := len(f.candidates)

	f.query = ".go"
	f.filter()
	if len(f.candidates) == 0 {
		t.Skip("no .go files to filter")
	}

	f.query = ""
	f.filter()
	if len(f.candidates) != allCount {
		t.Fatalf("expected %d candidates after clearing query, got %d", allCount, len(f.candidates))
	}
}

func TestFilePickerOverlay_Navigation(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if len(f.candidates) == 0 {
		t.Skip("no candidates to navigate")
	}

	if f.selection != 0 {
		t.Fatal("expected initial selection at 0")
	}

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.selection != 1 {
		t.Fatalf("expected selection 1 after Down, got %d", f.selection)
	}

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.selection != 0 {
		t.Fatalf("expected selection 0 after Up, got %d", f.selection)
	}

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.selection != 0 {
		t.Fatalf("expected selection to stay at 0 at top boundary")
	}
}

func TestFilePickerOverlay_EscCloses(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected picker to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.open {
		t.Fatal("expected picker to close on Esc")
	}
}

func TestFilePickerOverlay_EnterDoesNotClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected picker to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.open {
		t.Fatal("expected picker to stay open on Enter (handled by caller)")
	}
}

func TestFilePickerOverlay_BackspaceRemovesQueryChar(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	f.query = "abc"
	f.filter()

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.query != "ab" {
		t.Fatalf("expected query 'ab', got %q", f.query)
	}
}

func TestFilePickerOverlay_KeyRunesAppendsToQuery(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if f.query != "s" {
		t.Fatalf("expected query 's', got %q", f.query)
	}

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if f.query != "sr" {
		t.Fatalf("expected query 'sr', got %q", f.query)
	}

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if f.query != "src" {
		t.Fatalf("expected query 'src', got %q", f.query)
	}
}

func TestFilePickerOverlay_RespectsExclusions(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	foundExcluded := false
	for _, entry := range f.allEntries {
		if entry == ".git/" || entry == ".git" || strings.HasPrefix(entry, ".git/") {
			foundExcluded = true
			break
		}
	}
	if foundExcluded {
		t.Fatal("expected .git to be excluded from picker entries")
	}
}

func TestFilePickerOverlay_ResetsQueryOnOpen(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	f.query = "test"
	f = f.Close()
	f = f.Open(".")
	if f.query != "" {
		t.Fatalf("expected empty query after reopen, got %q", f.query)
	}
	if f.selection != 0 {
		t.Fatalf("expected selection 0 after reopen, got %d", f.selection)
	}
}

func TestModelFilePicker_OpensOnAt(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.open {
		t.Fatal("expected file picker to open after @")
	}
}

func TestModelFilePicker_EscCloses(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.open {
		t.Fatal("expected file picker to open after @")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filePicker.open {
		t.Fatal("expected file picker to close on Esc")
	}
}

func TestModelFilePicker_EnterInsertsPath(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.open {
		t.Fatal("expected file picker to open")
	}
	if len(m.filePicker.candidates) == 0 {
		t.Skip("no candidates")
	}

	selected := m.filePicker.candidates[m.filePicker.selection]

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filePicker.open {
		t.Fatal("expected file picker to close after Enter")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected+" ") && val != selected {
		t.Fatalf("expected input to start with %q, got %q", selected+" ", val)
	}
}

func TestModelFilePicker_DoesNotOpenOnOtherChars(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.filePicker.open {
		t.Fatal("expected file picker to stay closed on non-@")
	}
}
