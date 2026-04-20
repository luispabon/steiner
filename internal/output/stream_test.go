package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamFormatsModelToolAndStopEvents(t *testing.T) {
	var buf bytes.Buffer
	stream := NewStream(&buf)

	stream.Emit(NewModelCallStartedEvent(1, "test-model", 5))
	stream.Emit(NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "note.txt"}))
	stream.Emit(NewApprovalRequestedEvent(1, "write", "prompt"))
	stream.Emit(NewApprovalAcceptedEvent(1, "write", "prompt", "ok"))
	stream.Emit(NewApprovalDeniedEvent(1, "bash", "prompt", "blocked"))
	stream.Emit(NewToolCallFinishedEvent(1, "read", "call_1", `{"contents":"hello"}`, nil))
	stream.Emit(NewStopReasonEvent(2, "complete", nil))

	got := buf.String()
	for _, want := range []string{
		"model turn=1 start",
		"tool turn=1 start",
		"approval turn=1 requested",
		"approval turn=1 accepted",
		"approval turn=1 denied",
		"tool turn=1 end",
		"stop reason=complete",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}
