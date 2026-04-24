package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestRenderHelpIncludesContextCommand(t *testing.T) {
	help := renderHelp(theme.Default().LipGlossStyles(), 60)
	if !strings.Contains(help, "/context") {
		t.Fatalf("help = %q, want /context entry", help)
	}
}
