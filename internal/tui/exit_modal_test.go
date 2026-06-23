package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestExitModalRenderIsCompactWithSingleFooterDivider(t *testing.T) {
	useTrueColor(t)
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m.openExitModal()

	rendered := stripANSI(m.renderExitModal())
	for _, want := range []string{"Exit steiner?", "Leave the interactive session", "Exit", "Cancel", "confirm"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	lines := strings.Split(rendered, "\n")
	foundTitle := false
	for _, line := range lines {
		if strings.Contains(line, "Exit steiner?") {
			foundTitle = true
			break
		}
	}
	if !foundTitle {
		t.Fatalf("rendered modal = %q, want Exit steiner? on the title line", rendered)
	}
	dividerCount := 0
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "│") && strings.Count(line, "─") >= 20 {
			dividerCount++
		}
	}
	if dividerCount != 1 {
		t.Fatalf("rendered modal = %q, divider count = %d, want 1 footer divider", rendered, dividerCount)
	}
	if lines := strings.Count(rendered, "\n") + 1; lines > 12 {
		t.Fatalf("rendered modal line count = %d, want compact dialog", lines)
	}
}

func TestExitModalRenderKeepsButtonsOnSingleLine(t *testing.T) {
	useTrueColor(t)
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m.openExitModal()

	rendered := m.renderExitModal()
	lines := strings.Split(rendered, "\n")
	wantWidth := m.exitModal.overlayWidth()
	foundButtons := false
	for _, line := range lines {
		if lipgloss.Width(line) != wantWidth {
			t.Fatalf("line width = %d, want %d for line %q", lipgloss.Width(line), wantWidth, stripANSI(line))
		}
		stripped := stripANSI(line)
		if strings.Contains(stripped, "Cancel") && strings.Contains(stripped, "Exit") {
			foundButtons = true
		}
	}
	if !foundButtons {
		t.Fatalf("rendered modal = %q, want a button row", stripANSI(rendered))
	}
}
