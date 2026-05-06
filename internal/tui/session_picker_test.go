package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestSessionPickerOverlayOpen(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	if overlay.IsOpen() {
		t.Fatal("IsOpen = true, want false before open")
	}

	entries := []session.IndexEntry{
		{ID: "abc123def456", Title: "First session", Model: "claude-opus", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "xyz789uvw012", Title: "Second session", Model: "claude-sonnet", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)
	if !opened.IsOpen() {
		t.Fatal("IsOpen = false, want true after open")
	}
	if len(opened.allEntries) != 2 {
		t.Fatalf("len(allEntries) = %d, want 2", len(opened.allEntries))
	}
	if len(opened.candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(opened.candidates))
	}
	if opened.selection != 0 {
		t.Fatalf("selection = %d, want 0", opened.selection)
	}
	if opened.query != "" {
		t.Fatalf("query = %q, want empty", opened.query)
	}
}

func TestSessionPickerOverlayClose(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "abc123def456", Title: "First session", Model: "claude-opus", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)
	if !opened.IsOpen() {
		t.Fatal("IsOpen = false after open")
	}

	closed := opened.Close()
	if closed.IsOpen() {
		t.Fatal("IsOpen = true after close, want false")
	}
}

func TestSessionPickerOverlayNavigation(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "First", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id2", Title: "Second", Model: "m2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id3", Title: "Third", Model: "m3", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)

	// Test arrow down
	updated, _ := opened.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.selection != 1 {
		t.Fatalf("selection after KeyDown = %d, want 1", updated.selection)
	}

	// Test arrow up
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.selection != 0 {
		t.Fatalf("selection after KeyUp = %d, want 0", updated.selection)
	}

	// Test boundary: can't go below 0
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.selection != 0 {
		t.Fatalf("selection at boundary = %d, want 0", updated.selection)
	}

	// Test boundary: can't exceed length-1
	updated = overlay.Open(entries)
	updated.selection = 2
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.selection != 2 {
		t.Fatalf("selection at upper boundary = %d, want 2", updated.selection)
	}
}

func TestSessionPickerOverlayEscapeCloses(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "First", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)
	if !opened.IsOpen() {
		t.Fatal("not open after Open()")
	}

	closed, _ := opened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if closed.IsOpen() {
		t.Fatal("IsOpen = true after Esc, want false")
	}
}

func TestSessionPickerOverlaySearchFiltering(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "First meeting notes", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id2", Title: "Project planning", Model: "m2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id3", Title: "Code review", Model: "m3", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)

	// Filter for "project"
	filtered, _ := opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("project")})
	if len(filtered.candidates) != 1 {
		t.Fatalf("filtered candidates count = %d, want 1", len(filtered.candidates))
	}
	if filtered.candidates[0].Title != "Project planning" {
		t.Fatalf("filtered title = %q, want 'Project planning'", filtered.candidates[0].Title)
	}

	// Filter for "code"
	filtered = overlay.Open(entries)
	filtered, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("code")})
	if len(filtered.candidates) != 1 {
		t.Fatalf("code filter count = %d, want 1", len(filtered.candidates))
	}
	if filtered.candidates[0].Title != "Code review" {
		t.Fatalf("code filter title = %q, want 'Code review'", filtered.candidates[0].Title)
	}
}

func TestSessionPickerOverlaySearchCaseInsensitive(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "First Meeting", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id2", Title: "code review", Model: "m2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)

	// Filter for lowercase "first"
	filtered, _ := opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")})
	if len(filtered.candidates) != 1 {
		t.Fatalf("lowercase filter count = %d, want 1", len(filtered.candidates))
	}
	if filtered.candidates[0].Title != "First Meeting" {
		t.Fatalf("filtered title = %q, want 'First Meeting'", filtered.candidates[0].Title)
	}
}

func TestSessionPickerOverlayBackspaceDeletesQuery(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "Project planning", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id2", Title: "Code review", Model: "m2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)

	// Type "project"
	filtered, _ := opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("project")})
	if filtered.query != "project" {
		t.Fatalf("query = %q, want 'project'", filtered.query)
	}

	// Backspace
	filtered, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if filtered.query != "projec" {
		t.Fatalf("query after backspace = %q, want 'projec'", filtered.query)
	}
	if len(filtered.candidates) != 1 {
		t.Fatalf("candidates after backspace = %d, want 1", len(filtered.candidates))
	}

	// Backspace all the way
	for len(filtered.query) > 0 {
		filtered, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if len(filtered.candidates) != 2 {
		t.Fatalf("candidates after clearing query = %d, want 2", len(filtered.candidates))
	}
}

func TestSessionPickerOverlaySelectionResetOnFilter(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	overlay := newSessionPickerOverlay(styles)

	entries := []session.IndexEntry{
		{ID: "id1", Title: "First", Model: "m1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id2", Title: "Second", Model: "m2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "id3", Title: "Third", Model: "m3", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	opened := overlay.Open(entries)

	// Move selection to index 2
	updated, _ := opened.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.selection != 2 {
		t.Fatalf("selection = %d, want 2", updated.selection)
	}

	// Type a filter character
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})

	// Selection should reset to 0
	if updated.selection != 0 {
		t.Fatalf("selection after filter = %d, want 0", updated.selection)
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		age      time.Duration
		expected string
	}{
		{"now", 0, "now"},
		{"30 seconds ago", 30 * time.Second, "now"},
		{"5 minutes ago", 5 * time.Minute, "5m ago"},
		{"1 hour ago", time.Hour, "1h ago"},
		{"23 hours ago", 23 * time.Hour, "23h ago"},
		{"1 day ago", 24 * time.Hour, "1d ago"},
		{"29 days ago", 29 * 24 * time.Hour, "29d ago"},
		{"45 days ago", 45 * 24 * time.Hour, "old"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relativeTime(now.Add(-tt.age))
			if result != tt.expected {
				t.Fatalf("relativeTime(%s) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}
