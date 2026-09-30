package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestRenderHelpWidthCapAndMinimum(t *testing.T) {
	t.Parallel()
	styles := theme.Default().LipGlossStyles()
	for _, tc := range []struct {
		width int
		want  int
	}{{10, 20}, {20, 20}, {60, 60}, {120, 100}} {
		got := lipgloss.Width(renderHelp(&styles, tc.width))
		if got != tc.want {
			t.Errorf("renderHelp width %d = %d, want %d", tc.width, got, tc.want)
		}
	}
}

func TestRenderHelpIncludesContextKeybind(t *testing.T) {
	t.Parallel()
	s := theme.Default().LipGlossStyles()
	styles := &s
	help := renderHelp(styles, 60)
	if !strings.Contains(help, "ctrl+t") {
		t.Fatalf("help = %q, want ctrl+t entry", help)
	}
	if !strings.Contains(help, "ctrl+f1") {
		t.Fatalf("help = %q, want ctrl+f1 entry", help)
	}
	headingStyle := lipgloss.NewStyle().Foreground(styles.AccentColor).Bold(true)
	for _, title := range []string{"NAVIGATION", "INPUT", "SESSION", "APPROVAL"} {
		if !strings.Contains(help, headingStyle.Render(title)) {
			t.Fatalf("help = %q, want accent-colored %s heading", help, title)
		}
	}
}
