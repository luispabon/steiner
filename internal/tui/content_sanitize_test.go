package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestToolOutputSanitizedInTUI checks that raw terminal controls in tool
// output, tool args, and display_file output are removed before lipgloss
// renders them. Lipgloss adds its own SGR codes, so the test checks for the
// specific raw sequences instead of a bare ESC.
func TestToolOutputSanitizedInTUI(t *testing.T) {
	t.Parallel()

	const raw = "before\x1b]0;pwned\x07\x1b[2J\rafter"

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-1", map[string]any{"command": "echo " + raw}))
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call-1", raw, nil))
	b.AppendEvent(output.NewDisplayFileEvent(output.DisplayFilePayload{
		Path:    "f\x1b[2J.txt",
		Preview: output.FormatFilePreview("f.txt", raw+"\n"),
	}))

	got := b.String(100)
	for _, bad := range []string{"pwned", "\x1b]", "\x1b[2J", "\x07", "\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("rendered tool output contains %q: %q", bad, got)
		}
	}
	if !strings.Contains(got, "beforeafter") {
		t.Errorf("rendered tool output = %q, want visible text \"beforeafter\"", got)
	}
}
