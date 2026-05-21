package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestSlashOverlayOpen(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "clear screen", source: ""},
		{command: "/mykill", name: "Awesome Skill", desc: "kills things", source: "project"},
	}

	overlay = overlay.Open(items)

	if !overlay.IsOpen() {
		t.Fatal("IsOpen = false, want true")
	}
	if len(overlay.candidates) != 2 {
		t.Fatalf("candidates count = %d, want 2", len(overlay.candidates))
	}
	if overlay.selection != 0 {
		t.Fatalf("selection = %d, want 0", overlay.selection)
	}
}

func TestSlashOverlayClose(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)
	items := []slashOverlayItem{{command: "/clear", name: "Clear", desc: "clear", source: ""}}
	overlay = overlay.Open(items)

	overlay = overlay.Close()

	if overlay.IsOpen() {
		t.Fatal("IsOpen = true after Close, want false")
	}
}

func TestSlashOverlayFilterByCommand(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
		{command: "/context", name: "Context", desc: "inspect context", source: ""},
	}

	overlay = overlay.Open(items)
	overlay.query = "/co"
	overlay.filterCandidates()

	if len(overlay.candidates) != 2 {
		t.Fatalf("candidates count = %d, want 2 (for /co prefix)", len(overlay.candidates))
	}
	if overlay.candidates[0].command != "/config" {
		t.Fatalf("first candidate = %q, want /config", overlay.candidates[0].command)
	}
}

func TestSlashOverlaySyncQueryUsesSlashToken(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
		{command: "/context", name: "Context", desc: "inspect context", source: ""},
	}

	overlay = overlay.Open(items)
	overlay.syncQuery("/co")

	if overlay.query != "/co" {
		t.Fatalf("query = %q, want /co", overlay.query)
	}
	if len(overlay.candidates) != 2 {
		t.Fatalf("candidates count = %d, want 2", len(overlay.candidates))
	}
}

func TestSlashOverlayFilterByName(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/skill1", name: "Database Helper", desc: "helps with databases", source: "user"},
		{command: "/skill2", name: "Web Worker", desc: "web stuff", source: "global"},
	}

	overlay = overlay.Open(items)
	overlay.query = "database"
	overlay.filterCandidates()

	if len(overlay.candidates) != 1 {
		t.Fatalf("candidates count = %d, want 1", len(overlay.candidates))
	}
	if overlay.candidates[0].command != "/skill1" {
		t.Fatalf("candidate = %q, want /skill1", overlay.candidates[0].command)
	}
}

func TestSlashOverlayFilterByDesc(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset conversation", source: ""},
		{command: "/compact", name: "Compact", desc: "compress context", source: ""},
	}

	overlay = overlay.Open(items)
	overlay.query = "context"
	overlay.filterCandidates()

	if len(overlay.candidates) != 1 {
		t.Fatalf("candidates count = %d, want 1", len(overlay.candidates))
	}
	if overlay.candidates[0].command != "/compact" {
		t.Fatalf("candidate = %q, want /compact", overlay.candidates[0].command)
	}
}

func TestSlashOverlayNavigateUpDown(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/a", name: "A", desc: "a", source: ""},
		{command: "/b", name: "B", desc: "b", source: ""},
		{command: "/c", name: "C", desc: "c", source: ""},
	}

	overlay = overlay.Open(items)

	if overlay.selection != 0 {
		t.Fatalf("initial selection = %d, want 0", overlay.selection)
	}

	// Move down
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if overlay.selection != 1 {
		t.Fatalf("after down: selection = %d, want 1", overlay.selection)
	}

	// Move down again
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if overlay.selection != 2 {
		t.Fatalf("after down: selection = %d, want 2", overlay.selection)
	}

	// At boundary, shouldn't move further
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if overlay.selection != 2 {
		t.Fatalf("at boundary: selection = %d, want 2", overlay.selection)
	}

	// Move up
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyUp})
	if overlay.selection != 1 {
		t.Fatalf("after up: selection = %d, want 1", overlay.selection)
	}
}

func TestSlashOverlayTypeToFilter(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
		{command: "/context", name: "Context", desc: "inspect context", source: ""},
	}

	overlay = overlay.Open(items)

	// Type "co"
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if len(overlay.candidates) != 2 {
		t.Fatalf("after typing 'co': candidates count = %d, want 2", len(overlay.candidates))
	}
	if overlay.selection != 0 {
		t.Fatalf("after filter: selection = %d, want 0", overlay.selection)
	}
}

func TestSlashOverlayBackspace(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
		{command: "/context", name: "Context", desc: "inspect context", source: ""},
	}

	overlay = overlay.Open(items)

	// Type "config"
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("config"[0:1])})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("config"[1:2])})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("config"[2:3])})

	if len(overlay.candidates) != 2 {
		t.Fatalf("after 'con': candidates = %d, want 2 (matches /config and /context)", len(overlay.candidates))
	}

	// Backspace
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if len(overlay.candidates) != 2 {
		t.Fatalf("after backspace: candidates = %d, want 2", len(overlay.candidates))
	}
}

func TestSlashOverlaySelectedItem(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
	}

	overlay = overlay.Open(items)

	selected := overlay.SelectedItem()
	if selected == nil || selected.command != "/clear" {
		t.Fatalf("selected = %v, want /clear", selected)
	}

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected = overlay.SelectedItem()
	if selected == nil || selected.command != "/config" {
		t.Fatalf("selected = %v, want /config", selected)
	}
}

func TestSlashOverlaySelectedItemNilWhenInvalid(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	// No items selected
	selected := overlay.SelectedItem()
	if selected != nil {
		t.Fatalf("selected = %v, want nil for closed overlay", selected)
	}
}

func TestSlashOverlaySourceIndicator(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/clear", name: "Clear", desc: "reset", source: ""},
		{command: "/skill1", name: "Skill One", desc: "from project", source: "project"},
		{command: "/skill2", name: "Skill Two", desc: "from user", source: "user"},
	}

	overlay = overlay.Open(items)

	// Verify items are created with correct sources
	if overlay.allItems[0].source != "" {
		t.Fatalf("item 0 source = %q, want empty for built-in", overlay.allItems[0].source)
	}
	if overlay.allItems[1].source != "project" {
		t.Fatalf("item 1 source = %q, want project", overlay.allItems[1].source)
	}
	if overlay.allItems[2].source != "user" {
		t.Fatalf("item 2 source = %q, want user", overlay.allItems[2].source)
	}
}

func TestSlashOverlayViewMarksSelectedRowWithPrefix(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{command: "/compact", name: "Compact", desc: "compact context", source: ""},
		{command: "/config", name: "Config", desc: "show config", source: ""},
	}

	overlay = overlay.Open(items)
	overlay.query = "/"

	plain := stripANSI(overlay.View())
	lines := strings.Split(plain, "\n")

	var selectedLine string
	var unselectedLine string
	for _, line := range lines {
		if strings.Contains(line, "/compact") {
			selectedLine = line
		}
		if strings.Contains(line, "/config") {
			unselectedLine = line
		}
	}

	if !strings.Contains(selectedLine, "> /compact") {
		t.Fatalf("selected line = %q, want visible selection marker", selectedLine)
	}
	if strings.Contains(unselectedLine, "> /config") {
		t.Fatalf("unselected line = %q, want no selection marker", unselectedLine)
	}
	if !strings.Contains(unselectedLine, "  /config") {
		t.Fatalf("unselected line = %q, want aligned empty prefix", unselectedLine)
	}
}

func TestSlashOverlayViewRendersSkillRowsAsCommandAndDescriptionOnly(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	overlay := newSlashOverlay(styles)

	items := []slashOverlayItem{
		{
			command: "/review",
			name:    "review",
			desc:    "Review changes for bugs and regressions.",
			source:  "project",
			isSkill: true,
		},
	}

	overlay = overlay.Open(items)

	plain := stripANSI(overlay.View())
	if !strings.Contains(plain, "/review  Review changes for bugs…") {
		t.Fatalf("overlay view = %q, want command plus truncated description", plain)
	}
	if strings.Contains(plain, "[project]") {
		t.Fatalf("overlay view = %q, want no source badge for skill row", plain)
	}
	if strings.Contains(plain, "/review  review") {
		t.Fatalf("overlay view = %q, want no duplicated skill slug", plain)
	}
	if strings.Contains(plain, "\n│ essions.") {
		t.Fatalf("overlay view = %q, want skill description to stay on one line", plain)
	}
}
