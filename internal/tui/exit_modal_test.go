package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestExitModalRenderIsCompactWithSingleFooterDivider(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m.openExitModal()

	rendered := stripANSI(m.renderExitModal())

	for _, want := range []string{"Exit steiner?", "Leave the interactive session", "Exit", "Cancel", "confirm"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	dividerCount := 0
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "│─ ") == "" && strings.Contains(trimmed, "─") {
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
