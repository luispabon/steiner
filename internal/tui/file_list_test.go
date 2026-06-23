package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFileListOverlay_NewClose(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	if f.IsOpen() {
		t.Fatal("expected overlay to start closed")
	}

	f = f.Open(".")
	if !f.IsOpen() {
		t.Fatal("expected overlay to be open after Open()")
	}
	if len(f.entries) == 0 {
		t.Fatal("expected entries from walking current dir")
	}

	f = f.Close()
	if f.IsOpen() {
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
	f.OverlayShell = f.WithDimensions(80, 24)
	f = f.Open(".")
	if !f.IsOpen() {
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
	if !f.IsOpen() {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Esc")
	}
}

func TestFileListOverlay_UpdateEnter(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	if !f.IsOpen() {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Enter")
	}
}

func TestFileListOverlay_UpdateIgnoresOtherKeys(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f = f.Open(".")
	if !f.IsOpen() {
		t.Fatal("expected overlay to be open")
	}

	updated, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if !updated.IsOpen() {
		t.Fatal("expected overlay to stay open on non-close keys")
	}
}

func TestFileListOverlay_UpdateWhenClosed(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	if f.IsOpen() {
		t.Fatal("expected overlay to start closed")
	}

	updated, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected closed overlay to stay closed")
	}
}

func TestFileListOverlay_RootWithError(t *testing.T) {
	s := theme.BuildStyles("#ff0000")
	f := newFileListOverlay(s)
	f.OverlayShell = f.WithDimensions(80, 24)
	f = f.Open("/nonexistent/path/xyzzy")
	if !f.IsOpen() {
		t.Fatal("expected overlay to be open even on error")
	}
	view := f.View()
	if view == "" {
		t.Fatal("expected non-empty view showing error")
	}
	if !strings.Contains(view, "error") {
		t.Fatal("expected view to contain error text")
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
