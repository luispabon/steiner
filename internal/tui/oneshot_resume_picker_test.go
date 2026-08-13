package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestOneshotResumePickerOverlayOpen(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	overlay := newOneshotResumePickerOverlay(styles)

	if overlay.IsOpen() {
		t.Fatal("IsOpen = true, want false before open")
	}

	runs := []oneshot.ResumableRun{
		{
			RunID:       "run-abc123",
			Slug:        "fix-issue",
			Task:        "Fix issue #123",
			ResumePhase: "review",
			Status:      "resume at review",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			RunID:       "run-xyz789",
			Slug:        "feat-dashboard",
			Task:        "Implement dashboard",
			ResumePhase: "implement",
			Status:      "resume at implement",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	opened := overlay.Open(runs)
	if !opened.IsOpen() {
		t.Fatal("IsOpen = false, want true after open")
	}
	if len(opened.allRuns) != 2 {
		t.Fatalf("len(allRuns) = %d, want 2", len(opened.allRuns))
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

func TestOneshotResumePickerOverlayClose(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	overlay := newOneshotResumePickerOverlay(styles)

	runs := []oneshot.ResumableRun{
		{
			RunID:       "run-abc123",
			Slug:        "fix-issue",
			Task:        "Fix issue #123",
			ResumePhase: "review",
			Status:      "resume at review",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	opened := overlay.Open(runs)
	if !opened.IsOpen() {
		t.Fatal("IsOpen = false after open")
	}

	closed := opened.Close()
	if closed.IsOpen() {
		t.Fatal("IsOpen = true after close, want false")
	}
}

func TestOneshotResumePickerOverlayNavigation(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	overlay := newOneshotResumePickerOverlay(styles)

	runs := []oneshot.ResumableRun{
		{
			RunID:       "run-1",
			Slug:        "task-1",
			Task:        "First task",
			ResumePhase: "plan",
			Status:      "resume at plan",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			RunID:       "run-2",
			Slug:        "task-2",
			Task:        "Second task",
			ResumePhase: "implement",
			Status:      "resume at implement",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			RunID:       "run-3",
			Slug:        "task-3",
			Task:        "Third task",
			ResumePhase: "review",
			Status:      "resume at review",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	opened := overlay.Open(runs)

	// Test arrow down
	updated, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if updated.selection != 1 {
		t.Fatalf("selection after KeyDown = %d, want 1", updated.selection)
	}

	// Test arrow up
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if updated.selection != 0 {
		t.Fatalf("selection after KeyUp = %d, want 0", updated.selection)
	}

	// Test boundary: can't go below 0
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if updated.selection != 0 {
		t.Fatalf("selection at boundary = %d, want 0", updated.selection)
	}

	// Test boundary: can't exceed length-1
	updated = overlay.Open(runs)
	updated.selection = 2
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if updated.selection != 2 {
		t.Fatalf("selection at upper boundary = %d, want 2", updated.selection)
	}
}

func TestOneshotResumePickerOverlaySelectedRunID(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentPresets["amber"])
	overlay := newOneshotResumePickerOverlay(styles)

	runs := []oneshot.ResumableRun{
		{
			RunID:       "run-abc123",
			Slug:        "fix-issue",
			Task:        "Fix issue #123",
			ResumePhase: "review",
			Status:      "resume at review",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			RunID:       "run-xyz789",
			Slug:        "feat-dashboard",
			Task:        "Implement dashboard",
			ResumePhase: "implement",
			Status:      "resume at implement",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	opened := overlay.Open(runs)

	// First item selected by default
	if opened.SelectedRunID() != "run-abc123" {
		t.Fatalf("SelectedRunID() = %q, want run-abc123", opened.SelectedRunID())
	}

	// Move to second item
	updated, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if updated.SelectedRunID() != "run-xyz789" {
		t.Fatalf("SelectedRunID() = %q, want run-xyz789", updated.SelectedRunID())
	}
}
