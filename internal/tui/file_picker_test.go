package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFilePickerOverlay_NewClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	if f.IsOpen() {
		t.Fatal("expected picker to start closed")
	}

	f = f.Open(".")
	if !f.IsOpen() {
		t.Fatal("expected picker to be open after Open()")
	}
	if len(f.allEntries) == 0 {
		t.Fatal("expected non-empty entries from walking current dir")
	}

	f = f.Close()
	if f.IsOpen() {
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
	if !f.IsOpen() {
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
	if !f.IsOpen() {
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

func TestFilePickerOverlay_SyncQueryFiltersCandidates(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if len(f.candidates) == 0 {
		t.Skip("no entries to filter")
	}

	f.syncQuery(".go")
	if f.query != ".go" {
		t.Fatalf("query = %q, want .go", f.query)
	}
	if len(f.candidates) == 0 {
		t.Fatal("expected at least one candidate matching .go")
	}
	if len(f.matchIndexes) != len(f.candidates) {
		t.Fatalf("match indexes = %d, want %d", len(f.matchIndexes), len(f.candidates))
	}
}

func TestFilePickerOverlay_FuzzyQueryRanksAbbreviatedPathMatches(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.allEntries = []string{
		"internal/tui/file_picker.go",
		"internal/tool/file_policy.go",
		"docs/picker-notes.md",
	}

	f.syncQuery("ifpg")

	if len(f.candidates) == 0 {
		t.Fatal("expected fuzzy matches for abbreviated path query")
	}
	if got := f.candidates[0]; got != "internal/tui/file_picker.go" {
		t.Fatalf("top candidate = %q, want internal/tui/file_picker.go", got)
	}
	if len(f.matchIndexes[0]) == 0 {
		t.Fatal("expected matched indexes for top candidate")
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
	if !f.IsOpen() {
		t.Fatal("expected picker to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected picker to close on Esc")
	}
}

func TestFilePickerOverlay_EnterDoesNotClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	if !f.IsOpen() {
		t.Fatal("expected picker to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.IsOpen() {
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
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}
}

func TestModelFilePicker_EscCloses(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close on Esc")
	}
}

func TestModelFilePicker_EnterInsertsPath(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open")
	}
	if len(m.filePicker.candidates) == 0 {
		t.Skip("no candidates")
	}

	selected := m.filePicker.candidates[m.filePicker.selection]

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after Enter")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected+" ") && val != selected {
		t.Fatalf("expected input to start with %q, got %q", selected+" ", val)
	}
}

func TestModelFilePicker_TypingUpdatesComposerAndPickerQuery(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if got := m.input.Value(); got != "@go" {
		t.Fatalf("input value = %q, want @go", got)
	}
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to stay open")
	}
	if got := m.filePicker.query; got != "go" {
		t.Fatalf("picker query = %q, want go", got)
	}
}

func TestModelFilePicker_EscRemovesActiveToken(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close on Esc")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want empty after Esc", got)
	}
}

func TestFilePickerOverlay_ScrollOffsetAdvancesAfterMaxDisplay(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.candidates = make([]string, 20)
	for i := range 20 {
		f.candidates[i] = string(rune('a' + i))
	}

	if f.scrollOffset != 0 {
		t.Fatalf("expected scrollOffset 0 initially, got %d", f.scrollOffset)
	}

	// Down 7 times: selection 7, last item in the first visible window — no scroll yet
	for range 7 {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if f.scrollOffset != 0 {
		t.Fatalf("expected scrollOffset 0 after 7 downs (selection 7 still fits), got %d", f.scrollOffset)
	}
	if f.selection != 7 {
		t.Fatalf("expected selection 7 after 7 downs, got %d", f.selection)
	}

	// One more down: selection 8, which exceeds scrollOffset+maxDisplay-1 → scrollOffset=1
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.scrollOffset != 1 {
		t.Fatalf("expected scrollOffset 1 when selection (8) leaves the first window, got %d", f.scrollOffset)
	}
	if f.selection != 8 {
		t.Fatalf("expected selection 8, got %d", f.selection)
	}

	// Scroll down one more to verify offset keeps advancing
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.scrollOffset != 2 {
		t.Fatalf("expected scrollOffset 2 after another Down, got %d", f.scrollOffset)
	}
	if f.selection != 9 {
		t.Fatalf("expected selection 9, got %d", f.selection)
	}
}

func TestFilePickerOverlay_ScrollOffsetMovesBackOnUp(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.candidates = make([]string, 20)
	for i := range 20 {
		f.candidates[i] = string(rune('a' + i))
	}
	f.selection = 10
	f.scrollOffset = 3

	// Press Up until selection moves below scrollOffset
	for i := 0; i < 8; i++ {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if f.selection != 2 {
		t.Fatalf("expected selection 2, got %d", f.selection)
	}
	if f.scrollOffset != 2 {
		t.Fatalf("expected scrollOffset 2 (matching selection), got %d", f.scrollOffset)
	}
}

func TestFilePickerOverlay_FilterResetsScrollOffset(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.allEntries = make([]string, 20)
	for i := range 20 {
		f.allEntries[i] = string(rune('a' + i))
	}
	f.selection = 10
	f.scrollOffset = 5
	f.query = "a"
	f.filter()

	if f.selection != 0 {
		t.Fatalf("expected selection 0 after filter, got %d", f.selection)
	}
	if f.scrollOffset != 0 {
		t.Fatalf("expected scrollOffset 0 after filter, got %d", f.scrollOffset)
	}
}

func TestFilePickerOverlay_OpenResetsScrollOffset(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f = f.Open(".")
	f.selection = 5
	f.scrollOffset = 3
	f = f.Close()
	f = f.Open(".")
	if f.scrollOffset != 0 {
		t.Fatalf("expected scrollOffset 0 after reopen, got %d", f.scrollOffset)
	}
}

func TestFilePickerOverlay_ViewRendersScrolledWindow(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	// Use distinctive candidates that won't appear in UI chrome text.
	f.candidates = make([]string, 20)
	for i := range 20 {
		f.candidates[i] = fmt.Sprintf("zzz_file_%02d", i)
	}
	f.selection = 10
	f.scrollOffset = 3

	view := f.View()
	// The visible window includes items at indices 3 through 3+maxDisplay-1 (3..10)
	if !strings.Contains(view, "zzz_file_03") {
		t.Fatal("expected item at scrollOffset to be visible")
	}
	if strings.Contains(view, "zzz_file_00") {
		t.Fatal("expected item before scrollOffset to NOT be visible")
	}
	if strings.Contains(view, "zzz_file_01") {
		t.Fatal("expected second item before scrollOffset to NOT be visible")
	}
	// Check that the "more" indicator appears since 20 > 3+8=11
	if !strings.Contains(view, "more") {
		t.Fatal("expected 'more' indicator since there are items beyond visible window")
	}
}

func TestFilePickerOverlay_ViewHidesMoreIndicatorAtEnd(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = make([]string, 10)
	for i := range 10 {
		f.candidates[i] = fmt.Sprintf("zzz_file_%02d", i)
	}
	f.selection = 9
	f.scrollOffset = 2

	view := f.View()
	// scrollOffset+maxDisplay = 10, len(candidates) = 10, so no "more" needed
	if strings.Contains(view, "more") {
		t.Fatal("expected no 'more' indicator when at end of list")
	}
}

func TestFilePickerOverlay_EmptyQueryShowsSelectedCandidate(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = []string{"alpha.txt", "beta.go", "gamma.md"}
	f.selection = 1

	view := f.View()
	// Should show "beta.go" (the selected candidate) in the header
	if !strings.Contains(view, "beta.go") {
		t.Fatal("expected selected candidate to appear in header when query is empty")
	}
	// Should NOT show placeholder text
	if strings.Contains(view, "search files") {
		t.Fatal("expected no placeholder text when candidates exist")
	}
}

func TestFilePickerOverlay_EmptyQueryShowsPlaceholderWhenNoCandidates(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = nil

	view := f.View()
	if !strings.Contains(view, "search files") {
		t.Fatal("expected placeholder text when no candidates")
	}
}

func TestFilePickerOverlay_QueryMirrorUpdatesOnSelectionChange(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = []string{"alpha.txt", "beta.go", "gamma.md"}

	// At selection 0, the header should mirror "alpha.txt"
	view1 := f.View()
	if !strings.Contains(view1, "alpha.txt") {
		t.Fatal("expected first candidate in header at selection 0")
	}

	// After moving selection to 2, the header should mirror "gamma.md"
	f.selection = 2
	view2 := f.View()
	if !strings.Contains(view2, "gamma.md") {
		t.Fatal("expected third candidate in header after selection changes")
	}
}

func TestFilePickerOverlay_ViewMarksSelectedRowWithPrefix(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = []string{"alpha.txt", "beta.go", "gamma.md"}
	f.selection = 1

	plain := stripANSI(f.View())
	lines := strings.Split(plain, "\n")

	var selectedLine string
	var unselectedLine string
	for _, line := range lines {
		if strings.Contains(line, "beta.go") {
			selectedLine = line
		}
		if strings.Contains(line, "alpha.txt") {
			unselectedLine = line
		}
	}

	if !strings.Contains(selectedLine, "> beta.go") {
		t.Fatalf("selected line = %q, want visible selection marker", selectedLine)
	}
	if strings.Contains(unselectedLine, "> alpha.txt") {
		t.Fatalf("unselected line = %q, want no selection marker", unselectedLine)
	}
	if !strings.Contains(unselectedLine, "  alpha.txt") {
		t.Fatalf("unselected line = %q, want aligned empty prefix", unselectedLine)
	}
}

func TestFilePickerOverlay_ViewHighlightsMatchedCharacters(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = []string{"internal/tui/file_picker.go"}
	f.matchIndexes = [][]int{{0, 9, 13, 18}}
	f.query = "ifpg"

	view := f.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "internal/tui/file_picker.go") {
		t.Fatalf("view = %q, want candidate text", plain)
	}
	if strings.Contains(view, "internal/tui/file_picker.go") {
		t.Fatalf("raw view = %q, want inline escape sequences inside matched candidate", view)
	}
}

func TestFilePickerOverlay_ViewOmitsFooterHelpRow(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s)
	f.OverlayShell = f.openShell()
	f.width = 80
	f.height = 24
	f.candidates = []string{"alpha.txt", "beta.go"}

	plain := stripANSI(f.View())
	for _, unwanted := range []string{"select", "navigate", "close"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("view = %q, want no footer help text %q", plain, unwanted)
		}
	}
}

func TestModelFilePicker_DoesNotOpenOnOtherChars(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to stay closed on non-@")
	}
}

func TestModelFilePicker_OverlayPreservesSidebarContent(t *testing.T) {
	m := newModel(Config{WorkingDir: ".", Model: "test-model", SidebarPosition: "right"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 50})
	m.sidebar.expanded = true

	if !m.sidebar.Visible(m.width) {
		t.Fatal("sidebar should be visible at width 120")
	}

	beforeView := stripANSI(m.View())
	branchRow := ""
	for _, line := range strings.Split(beforeView, "\n") {
		if idx := strings.Index(line, "branch "); idx >= 0 {
			branchRow = strings.TrimSpace(line[idx:])
			break
		}
	}
	if branchRow == "" {
		t.Fatal("expected sidebar branch row before opening file picker overlay")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}

	view := stripANSI(m.View())

	if !strings.Contains(view, "workdir.") {
		t.Fatal("expected sidebar workdir row to survive file picker overlay")
	}
	if !strings.Contains(view, branchRow) {
		t.Fatalf("expected sidebar branch row %q to survive file picker overlay", branchRow)
	}
}

func TestModelFilePicker_OverlayPreservesLeftSidebarContent(t *testing.T) {
	m := newModel(Config{WorkingDir: ".", Model: "test-model"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m.sidebar.expanded = true

	if !m.sidebar.Visible(m.width) {
		t.Fatal("sidebar should be visible at width 120")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}

	view := stripANSI(m.View())

	if !strings.Contains(view, "test-model") {
		t.Fatal("expected sidebar model name to survive file picker overlay")
	}
}

func TestFilePickerOverlay_OpenIncludesSteiner(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, ".steiner/config.yaml", "model: foo\n")
	mustWriteFile(t, root, ".steiner/plans/2026-01-01_x/overview.md", "# x\n")
	mustMkdir(t, root, "node_modules")
	mustWriteFile(t, root, "node_modules/foo.js", "module.exports = 1\n")
	mustWriteFile(t, root, "src/main.go", "package main\n")

	s := theme.BuildStyles("#ff0000")
	f := newFilePickerOverlay(s).Open(root)

	if !f.IsOpen() {
		t.Fatal("expected picker to be open")
	}

	wantIncluded := []string{
		".steiner/",
		".steiner/config.yaml",
		".steiner/plans/",
		".steiner/plans/2026-01-01_x/",
		".steiner/plans/2026-01-01_x/overview.md",
		"src/",
		"src/main.go",
	}
	for _, entry := range wantIncluded {
		if !containsString(f.allEntries, entry) {
			t.Errorf("expected entries to include %q, got %v", entry, f.allEntries)
		}
	}

	wantExcluded := []string{
		"node_modules/",
		"node_modules/foo.js",
	}
	for _, entry := range wantExcluded {
		if containsString(f.allEntries, entry) {
			t.Errorf("expected entries to exclude %q, got %v", entry, f.allEntries)
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func mustWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func mustMkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
}
