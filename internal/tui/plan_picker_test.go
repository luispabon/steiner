package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPlanPickerOpen(t *testing.T) {
	s := testStyles("#ff0000")
	m := newPlanPickerOverlay(s)
	if m.IsOpen() {
		t.Fatal("expected picker to start closed")
	}

	// Open with non-existent directory — should remain open with empty lists
	m = m.Open("/plan")
	if !m.IsOpen() {
		t.Fatal("expected picker to be open after Open()")
	}
	if len(m.allNames) != 0 {
		t.Fatalf("expected empty allNames for missing dir, got %d", len(m.allNames))
	}
	if len(m.candidates) != 0 {
		t.Fatalf("expected empty candidates for missing dir, got %d", len(m.candidates))
	}

	m = m.Close()
	if m.IsOpen() {
		t.Fatal("expected picker to be closed after Close()")
	}

	// Now create a temp .steiner/plans/ directory with some subdirs
	tmpDir := t.TempDir()
	plansDir := filepath.Join(tmpDir, ".steiner", "plans")
	err := os.MkdirAll(plansDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// Create some plan directories
	for _, name := range []string{"step-1", "step-2", "research", "bugfix"} {
		if err := os.MkdirAll(filepath.Join(plansDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	m = newPlanPickerOverlay(s).Open("/plan")
	if !m.IsOpen() {
		t.Fatal("expected picker to be open after Open()")
	}
	if len(m.allNames) != 4 {
		t.Fatalf("expected 4 plan dirs, got %d: %v", len(m.allNames), m.allNames)
	}
	if len(m.candidates) != 4 {
		t.Fatalf("expected 4 candidates, got %d", len(m.candidates))
	}
}

func TestPlanPickerUpdate(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	m := newPlanPickerOverlay(s)

	// Set up state manually
	m.allNames = []string{".steiner/plans/step-1", ".steiner/plans/step-2", ".steiner/plans/research", ".steiner/plans/bugfix"}
	m.candidates = append([]string(nil), m.allNames...)
	m.OverlayShell = m.openShell()

	// Type a character to filter
	m, _ = m.Update(tea.KeyPressMsg{Text: "step"})
	if len(m.candidates) != 2 {
		t.Fatalf("expected 2 candidates after typing 'step', got %d: %v", len(m.candidates), m.candidates)
	}

	// Type more to narrow further
	m, _ = m.Update(tea.KeyPressMsg{Text: "-2"})
	if len(m.candidates) != 1 {
		t.Fatalf("expected 1 candidate after typing '-2', got %d: %v", len(m.candidates), m.candidates)
	}
	if m.candidates[0] != ".steiner/plans/step-2" {
		t.Fatalf("expected candidate 'step-2', got %q", m.candidates[0])
	}

	// Press backspace to remove last chars
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if len(m.candidates) != 2 {
		t.Fatalf("expected 2 candidates after backspace, got %d: %v", len(m.candidates), m.candidates)
	}
}

func TestPlanPickerSelectedName(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	m := newPlanPickerOverlay(s)

	// Empty state
	if name := m.SelectedName(); name != "" {
		t.Fatalf("expected empty name, got %q", name)
	}

	// Populated state
	m.candidates = []string{".steiner/plans/step-1", ".steiner/plans/step-2", ".steiner/plans/research"}
	m.selection = 1
	if name := m.SelectedName(); name != ".steiner/plans/step-2" {
		t.Fatalf("expected 'step-2', got %q", name)
	}

	// Out of bounds
	m.selection = 10
	if name := m.SelectedName(); name != "" {
		t.Fatalf("expected empty name for out-of-bounds selection, got %q", name)
	}

	m.selection = -1
	if name := m.SelectedName(); name != "" {
		t.Fatalf("expected empty name for negative selection, got %q", name)
	}
}

func TestPlanPickerClose(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	m := newPlanPickerOverlay(s)
	m = m.Open("/plan")
	if !m.IsOpen() {
		t.Fatal("expected picker to be open after Open()")
	}

	m = m.Close()
	if m.IsOpen() {
		t.Fatal("expected picker to be closed after Close()")
	}

	// View should be empty when closed
	if view := m.View(); view != "" {
		t.Fatal("expected empty view after close")
	}

	// Update should be no-op when closed
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected nil cmd after update on closed picker")
	}
	if m.IsOpen() {
		t.Fatal("expected picker to stay closed")
	}
}

func TestPlanPickerViewShowsTriggerCommand(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")

	t.Run("/implement", func(t *testing.T) {
		t.Parallel()
		m := newPlanPickerOverlay(s).Open("/implement")
		if !m.IsOpen() {
			t.Fatal("expected picker to be open")
		}
		view := m.View()
		if !strings.Contains(view, "/implement") {
			t.Fatalf("View() = %q, want it to contain /implement", view)
		}
	})

	t.Run("/review", func(t *testing.T) {
		t.Parallel()
		m := newPlanPickerOverlay(s).Open("/review")
		if !m.IsOpen() {
			t.Fatal("expected picker to be open")
		}
		view := m.View()
		if !strings.Contains(view, "/review") {
			t.Fatalf("View() = %q, want it to contain /review", view)
		}
	})
}
