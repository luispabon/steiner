package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func newPaletteForTest(items []paletteItem) paletteModel {
	return newPalette(theme.BuildStyles("#ff0000"), items)
}

func samplePaletteItems(n int) []paletteItem {
	items := make([]paletteItem, n)
	for i := range n {
		items[i] = paletteItem{
			command: "/cmd" + string(rune('a'+i%26)),
			name:    "Item " + string(rune('A'+i%26)),
			desc:    "desc",
		}
	}
	return items
}

func TestPalette_OpenResetsState(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(5))
	p.query = "stale"
	p.cursor = 3
	p.scrollOffset = 2

	p = p.Open()
	if p.query != "" {
		t.Fatalf("query = %q, want empty", p.query)
	}
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", p.cursor)
	}
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0", p.scrollOffset)
	}
	if len(p.filtered) != 5 {
		t.Fatalf("filtered len = %d, want 5", len(p.filtered))
	}
}

func TestPalette_UpdateIgnoredWhenClosed(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(5))
	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatal("expected nil cmd when closed")
	}
	if updated.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", updated.cursor)
	}
}

func TestPalette_Navigation(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(5)).Open()

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", p.cursor)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", p.cursor)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (clamped)", p.cursor)
	}
}

func TestPalette_EscCloses(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(5)).Open()
	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected palette to close on Esc")
	}
}

func TestPalette_CtrlPCloses(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(5)).Open()
	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if updated.IsOpen() {
		t.Fatal("expected palette to close on Ctrl+P")
	}
}

func TestPalette_QueryFiltersAndResetsOnBackspace(t *testing.T) {
	p := newPaletteForTest([]paletteItem{
		{command: "/alpha", name: "Alpha", desc: "first"},
		{command: "/beta", name: "Beta", desc: "second"},
		{command: "/gamma", name: "Gamma", desc: "third"},
	}).Open()

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(p.filtered) != 3 {
		t.Fatalf("all 3 items contain 'a', got %d", len(p.filtered))
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if len(p.filtered) != 1 {
		t.Fatalf("only /alpha matches 'al', got %d", len(p.filtered))
	}
	if p.filtered[0].command != "/alpha" {
		t.Fatalf("filtered[0] = %q, want /alpha", p.filtered[0].command)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if p.query != "a" {
		t.Fatalf("query = %q, want a", p.query)
	}
	if len(p.filtered) != 3 {
		t.Fatalf("back to 'a' should yield 3 items, got %d", len(p.filtered))
	}
}

func TestPalette_ScrollOffsetAdvancesAfterMaxDisplay(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(20)).Open()

	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 initially", p.scrollOffset)
	}

	// maxDisplay = 8. Down 7 times → selection 7, still in first window.
	for range 7 {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 after 7 downs (selection 7 still fits)", p.scrollOffset)
	}
	if p.cursor != 7 {
		t.Fatalf("cursor = %d, want 7", p.cursor)
	}

	// One more down: selection 8 → window slides.
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.scrollOffset != 1 {
		t.Fatalf("scrollOffset = %d, want 1 (selection 8 leaves first window)", p.scrollOffset)
	}

	// Scroll back up enough to bring window back to 0.
	for range 8 {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 after scrolling back to selection 0", p.scrollOffset)
	}
}

func TestPalette_ViewRendersScrolledItems(t *testing.T) {
	p := newPaletteForTest(samplePaletteItems(12)).Open()
	p.width = 80
	p.height = 40

	// Move cursor to index 9 (10th item). With maxDisplay=8 the view must
	// scroll to keep the selected item visible.
	for range 9 {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.cursor != 9 {
		t.Fatalf("cursor = %d, want 9", p.cursor)
	}
	if p.scrollOffset < 1 {
		t.Fatalf("scrollOffset = %d, want >= 1 (selection 9 must scroll past index 7)", p.scrollOffset)
	}

	view := p.View()
	// The 10th command is /cmdj (index 9, 'a'+9 = 'j'). It must appear in the
	// scrolled view.
	if !strings.Contains(view, "/cmdj") {
		t.Fatalf("expected scrolled view to contain /cmdj (index 9), got:\n%s", view)
	}
	// Overflow indicator should show 2 remaining items (indices 2..9 shown, 10-11 remain).
	if !strings.Contains(view, "… and 2 more") {
		t.Fatalf("expected '… and 2 more' overflow indicator, got:\n%s", view)
	}
}

func TestPalette_EnterRunsActionAndCloses(t *testing.T) {
	type sentinelMsg string

	sentinel := func() tea.Msg { return sentinelMsg("sentinel-action-fired") }
	items := samplePaletteItems(5)
	items[2].action = func() tea.Cmd { return sentinel }

	p := newPaletteForTest(items).Open()
	p.width = 80
	p.height = 40

	// Move to index 2.
	for range 2 {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", p.cursor)
	}

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.IsOpen() {
		t.Fatal("expected palette to close on Enter")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from item action")
	}
	if got := cmd(); got == nil {
		t.Fatal("expected non-nil msg from cmd")
	} else if sm, ok := got.(sentinelMsg); !ok || string(sm) != "sentinel-action-fired" {
		t.Fatalf("expected sentinelMsg(\"sentinel-action-fired\"), got %T(%v)", got, got)
	}
}
