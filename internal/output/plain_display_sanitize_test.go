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
