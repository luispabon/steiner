package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestPlainRendererSanitizesApprovalPreview checks that an MCP approval preview
// with raw controls is rendered without them. The stored event keeps the
// original preview.
func TestPlainRendererSanitizesApprovalPreview(t *testing.T) {
	t.Parallel()

	const raw = "{\"query\":\"\x1b]0;pwned\x07visible\r\"}"
	event := NewApprovalRequestedEvent(ApprovalEventParams{
		Turn:     1,
		Tool:     "mcp__srv__search",
		CallID:   "call-1",
		Mode:     "ask",
		Preview:  raw,
		Kind:     "mcp",
		Server:   "srv",
		ToolName: "search",
	})

	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf, WithTheme(Theme{}))
	renderer.OnEvent(event)

	out := buf.String()
	requireNoControls(t, "plain approval output", out)
	if !strings.Contains(out, "visible") {
		t.Errorf("plain approval output = %q, want visible text kept", out)
	}
	payload, ok := event.Payload.(ApprovalEvent)
	if !ok {
		t.Fatalf("payload type = %T, want ApprovalEvent", event.Payload)
	}
	if payload.Preview != raw {
		t.Errorf("Preview = %q, want unchanged %q", payload.Preview, raw)
	}
}

// TestPlainRendererSanitizesDisplayFilePlaceholder covers image and binary
// display_file previews. Their path and lines are not built by the normalizing
// formatters, so the renderer must sanitize them itself. The stored payload
// keeps the original bytes.
func TestPlainRendererSanitizesDisplayFilePlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
	}{
		{name: "image", msg: "image preview unavailable in text overlay"},
		{name: "binary", msg: "binary file omitted from text preview"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const raw = "img\x1b]0;pwned\x07\x1b[2J.png"
			line := tt.msg + "\x1b[31m\r"
			event := NewDisplayFileEvent(DisplayFilePayload{
				Path: raw,
				Preview: PreviewDocument{
					Kind:     PreviewFormatKindFile,
					Language: "plain",
					Lines:    []PreviewLine{{Kind: PreviewLineKindText, Spans: []PreviewSpan{{Text: line}}}},
				},
			})

			var buf bytes.Buffer
			renderer := NewPlainRenderer(&buf, WithTheme(Theme{}))
			renderer.OnEvent(event)

			out := buf.String()
			requireNoControls(t, "plain display output", out)
			for _, want := range []string{"img.png", tt.msg} {
				if !strings.Contains(out, want) {
					t.Errorf("plain display output = %q, want %q", out, want)
				}
			}
			if strings.Contains(out, "pwned") {
				t.Errorf("plain display output = %q, OSC payload leaked", out)
			}

			payload, ok := event.Payload.(DisplayFilePayload)
			if !ok {
				t.Fatalf("payload type = %T, want DisplayFilePayload", event.Payload)
			}
			if payload.Path != raw || payload.Preview.Lines[0].Spans[0].Text != line {
				t.Errorf("stored display payload changed: path %q, line %q", payload.Path, payload.Preview.Lines[0].Spans[0].Text)
			}
		})
	}
}
