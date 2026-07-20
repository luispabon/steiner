package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestAccentPickerOpen(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	p := newAccentPickerOverlay(styles)
	p = p.Open("amber")

	if !p.IsOpen() {
		t.Fatal("expected picker to be open")
	}
	// 13 concrete presets + "random"
	if len(p.allNames) != 14 {
		t.Fatalf("expected 14 entries, got %d", len(p.allNames))
	}
	if len(p.candidates) != 14 {
		t.Fatalf("expected 14 candidates, got %d", len(p.candidates))
	}
}

func TestAccentPickerFilter(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	p := newAccentPickerOverlay(styles).Open("")

	// Simulate typing "bl"
	msg := tea.KeyPressMsg{Code: 'b', Text: "b"}
	p, _ = p.Update(msg)
	msg = tea.KeyPressMsg{Code: 'l', Text: "l"}
	p, _ = p.Update(msg)

	if len(p.candidates) != 1 {
		t.Fatalf("expected 1 candidate after filtering 'bl', got %d: %v", len(p.candidates), p.candidates)
	}
	if p.candidates[0] != "blue" {
		t.Fatalf("expected candidate 'blue', got %q", p.candidates[0])
	}
}

func TestAccentPickerSelect(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	p := newAccentPickerOverlay(styles).Open("")

	// Navigate to "violet"
	idx := slices.Index(p.candidates, "violet")
	if idx < 0 {
		t.Fatal("violet not found in candidates")
	}
	for i := 0; i < idx; i++ {
		msg := tea.KeyPressMsg{Code: tea.KeyDown}
		p, _ = p.Update(msg)
	}

	if p.SelectedName() != "violet" {
		t.Fatalf("expected SelectedName to be 'violet', got %q", p.SelectedName())
	}
}

func TestAccentPickerRandomEntry(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	p := newAccentPickerOverlay(styles).Open("")

	found := false
	for _, name := range p.allNames {
		if name == "random" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 'random' to be in allNames")
	}
	// random must be the last entry
	if p.allNames[len(p.allNames)-1] != "random" {
		t.Fatalf("expected 'random' to be last, got %q", p.allNames[len(p.allNames)-1])
	}
}

func TestAccentPickerClose(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	p := newAccentPickerOverlay(styles).Open("amber")
	p = p.Close()

	if p.IsOpen() {
		t.Fatal("expected picker to be closed after Close()")
	}
}
