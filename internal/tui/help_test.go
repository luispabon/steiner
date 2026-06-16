package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestRenderHelpIncludesContextKeybind(t *testing.T) {
	help := renderHelp(theme.Default().LipGlossStyles(), 60)
	if !strings.Contains(help, "ctrl+t") {
		t.Fatalf("help = %q, want ctrl+t entry", help)
	}
}
