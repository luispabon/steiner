package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestProfilePickerOpen(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	names := []string{"default", "fast", "careful"}
	p := newProfilePickerOverlay(styles)
	p = p.Open(names, "fast")

	if !p.IsOpen() {
		t.Fatal("expected picker to be open")
	}
	if len(p.allNames) != len(names) {
		t.Fatalf("expected %d entries, got %d", len(names), len(p.allNames))
	}
	if len(p.candidates) != len(names) {
		t.Fatalf("expected %d candidates, got %d", len(names), len(p.candidates))
	}
	selected, ok := p.SelectedName()
	if !ok || selected != "fast" {
		t.Fatalf("SelectedName() = %q, %v, want fast, true", selected, ok)
	}
}

func TestProfilePickerFilter(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	names := []string{"default", "fast", "careful"}
	p := newProfilePickerOverlay(styles).Open(names, "")

	// Simulate typing "ast", a subsequence only present in "fast"
	for _, r := range "ast" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		p, _ = p.Update(msg)
	}

	if len(p.candidates) != 1 {
		t.Fatalf("expected 1 candidate after filtering 'ast', got %d: %v", len(p.candidates), p.candidates)
	}
	if p.candidates[0] != "fast" {
		t.Fatalf("expected candidate 'fast', got %q", p.candidates[0])
	}
}

func TestProfilePickerSelect(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	names := []string{"default", "fast", "careful"}
	p := newProfilePickerOverlay(styles).Open(names, "")

	idx := slices.Index(p.candidates, "careful")
	if idx < 0 {
		t.Fatal("careful not found in candidates")
	}
	for i := 0; i < idx; i++ {
		msg := tea.KeyPressMsg{Code: tea.KeyDown}
		p, _ = p.Update(msg)
	}

	selected, ok := p.SelectedName()
	if !ok || selected != "careful" {
		t.Fatalf("SelectedName() = %q, %v, want careful, true", selected, ok)
	}
}

func TestProfilePickerClose(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	names := []string{"default", "fast", "careful"}
	p := newProfilePickerOverlay(styles).Open(names, "default")
	p = p.Close()

	if p.IsOpen() {
		t.Fatal("expected picker to be closed after Close()")
	}
}

func TestProfilePickerOpenNoCurrentMatch(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	names := []string{"default", "fast", "careful"}

	p := newProfilePickerOverlay(styles).Open(names, "nonexistent")
	if !p.IsOpen() {
		t.Fatal("expected picker to be open")
	}
	if p.selection != 0 {
		t.Fatalf("selection = %d, want 0 when current has no match", p.selection)
	}

	p2 := newProfilePickerOverlay(styles).Open(names, "")
	if p2.selection != 0 {
		t.Fatalf("selection = %d, want 0 when current is empty", p2.selection)
	}
}
