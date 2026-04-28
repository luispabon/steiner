package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFileListOverlay_NewClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	if f.open {
		t.Fatal("expected overlay to start closed")
	}

	f = f.Open(".")
	if !f.open {
		t.Fatal("expected overlay to be open after Open()")
	}
	if len(f.entries) == 0 {
		t.Fatal("expected entries from walking current dir")
	}

	f = f.Close()
	if f.open {
		t.Fatal("expected overlay to be closed after Close()")
	}
}

func TestFileListOverlay_ViewEmpty(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	view := f.View()
	if view != "" {
		t.Fatal("expected empty view when closed")
	}
}

func TestFileListOverlay_ViewNonEmpty(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f.width = 80
	f.height = 24
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected overlay to be open")
	}
	view := f.View()
	if view == "" {
		t.Fatal("expected non-empty view when open")
	}
}

func TestFileListOverlay_UpdateEsc(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.open {
		t.Fatal("expected overlay to close on Esc")
	}
}

func TestFileListOverlay_UpdateEnter(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.open {
		t.Fatal("expected overlay to close on Enter")
	}
}

func TestFileListOverlay_UpdateIgnoresOtherKeys(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	if !f.open {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if !updated.open {
		t.Fatal("expected overlay to stay open on non-close keys")
	}
}

func TestFileListOverlay_UpdateWhenClosed(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	if f.open {
		t.Fatal("expected overlay to start closed")
	}

	updated, _ := f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.open {
		t.Fatal("expected closed overlay to stay closed")
	}
}

func TestFileListOverlay_RootWithError(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f.width = 80
	f.height = 24
	f = f.Open("/nonexistent/path/xyzzy")
	if !f.open {
		t.Fatal("expected overlay to be open even on error")
	}
	if f.err == "" {
		t.Fatal("expected error message for nonexistent path")
	}
	view := f.View()
	if view == "" {
		t.Fatal("expected non-empty view showing error")
	}
}

func TestFileListOverlay_RespectsExclusions(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	foundExcluded := false
	for _, entry := range f.entries {
		if entry == ".git/" || entry == ".git" {
			foundExcluded = true
			break
		}
	}
	if foundExcluded {
		t.Fatal("expected .git to be excluded from file listing")
	}
}
