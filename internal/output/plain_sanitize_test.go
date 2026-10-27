package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestPlainRendererSanitizesToolAndFileOutput checks that raw controls in tool
// results and display_file output never reach the terminal, for both live and
// replayed tool results.
func TestPlainRendererSanitizesToolAndFileOutput(t *testing.T) {
	t.Parallel()

	const raw = "\x1b]0;pwned\x07\x1b[2Jvisible\r"

	tests := []struct {
		name   string
		events []Event
	}{
		{
			name: "live tool call",
			events: []Event{
				NewToolCallStartedEvent(1, "bash", "call-1", map[string]any{"command": "echo " + raw}),
				NewToolCallFinishedEvent(1, "bash", "call-1", raw, nil),
			},
		},
		{
			name: "replayed tool result",
			events: []Event{
				NewToolCallFinishedEventWithAdmission(0, "bash", "call-1", raw, nil, ToolPreview{}, nil),
			},
		},
		{
			name: "display file",
			events: []Event{
				NewDisplayFileEvent(DisplayFilePayload{
					Path:    "f" + raw + ".txt",
					Preview: FormatFilePreview("f.txt", raw+"\n"),
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			renderer := NewPlainRenderer(&buf, WithTheme(Theme{}))
			for _, event := range tt.events {
				renderer.OnEvent(event)
			}

			out := buf.String()
			requireNoControls(t, "plain output", out)
			if !strings.Contains(out, "visible") {
				t.Errorf("plain output = %q, want visible text kept", out)
			}
		})
	}
}

// TestPlainRendererLeavesToolResultEventUnchanged checks that sanitizing for
// display does not change the stored event payload.
func TestPlainRendererLeavesToolResultEventUnchanged(t *testing.T) {
	t.Parallel()

	const raw = "\x1b[31mred\x1b[0m\r\nnext"
	event := NewToolCallFinishedEvent(1, "bash", "call-1", raw, nil)

	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf, WithTheme(Theme{}))
	renderer.OnEvent(event)

	payload, ok := event.Payload.(ToolCallFinishedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want ToolCallFinishedEvent", event.Payload)
	}
	if payload.Result != raw {
		t.Errorf("Result = %q, want unchanged %q", payload.Result, raw)
	}
}
