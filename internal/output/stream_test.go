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

func TestStreamFormatsContextDiagnosticsEvents(t *testing.T) {
	var buf bytes.Buffer
	stream := NewStream(&buf)

	stream.Emit(NewContextCompactionEvent(4, 2, 6, 1, 3, 128, true, "compacted conversation history"))
	stream.Emit(NewContextBudgetEvent("project_context", 4, 900, 512, true, "trimmed extra files"))

	got := buf.String()
	for _, want := range []string{
		"context diagnostics kind=compaction",
		"turn=4",
		"retained_turns=2",
		"retained_messages=6",
		"compacted_messages=3",
		"summary_bytes=128",
		"summary=compacted conversation history",
		"truncated=true",
		"context diagnostics kind=budget",
		"scope=project_context",
		"used_bytes=900",
		"budget_bytes=512",
		"notes=trimmed extra files",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}
