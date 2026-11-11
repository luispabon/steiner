package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestToolOutputSanitizedInTUI checks that raw terminal controls in tool
// output, tool args, and read file previews are removed before lipgloss renders
// them. Lipgloss adds its own SGR codes, so the test checks for the specific raw
// sequences instead of a bare ESC.
func TestToolOutputSanitizedInTUI(t *testing.T) {
	t.Parallel()

	const raw = "before\x1b]0;pwned\x07\x1b[2J\rafter"

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-1", map[string]any{"command": "echo " + raw}))
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call-1", raw, nil))
	// A generic read preview: its path and contents come from the tool result
	// and reach lipgloss through the normalizing file preview formatter.
	b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call-2", map[string]any{"path": "f\x1b[2J.txt"}))
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call-2",
		`{"path":"f.txt","output":"before\u001b]0;pwned\u0007\u001b[2J\rafter\n"}`, nil))
	group := b.segments[0].toolGroupData
	if group == nil || len(group.entries) != 2 {
		t.Fatalf("segments[0] group = %#v, want bash and read entries", group)
	}
	group.entries[1].collapsed = false

	got := b.String(100)
	for _, bad := range []string{"pwned", "\x1b]", "\x1b[2J", "\x07", "\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("rendered tool output contains %q: %q", bad, got)
		}
	}
	if !strings.Contains(got, "beforeafter") {
		t.Errorf("rendered tool output = %q, want visible text \"beforeafter\"", got)
	}
	if !strings.Contains(got, "read file preview") {
		t.Errorf("rendered tool output = %q, want read file preview", got)
	}
}
