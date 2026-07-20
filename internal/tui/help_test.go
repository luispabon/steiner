package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestRenderHelpIncludesContextKeybind(t *testing.T) {
	t.Parallel()
	styles := theme.Default().LipGlossStyles()
	help := renderHelp(styles, 60)
	if !strings.Contains(help, "ctrl+t") {
		t.Fatalf("help = %q, want ctrl+t entry", help)
	}
	headingStyle := lipgloss.NewStyle().Foreground(styles.AccentColor).Bold(true)
	for _, title := range []string{"NAVIGATION", "INPUT", "SESSION", "APPROVAL"} {
		if !strings.Contains(help, headingStyle.Render(title)) {
			t.Fatalf("help = %q, want accent-colored %s heading", help, title)
		}
	}
}
