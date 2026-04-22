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
	stream.Emit(NewStopReasonEvent(3, "max_turns", nil))

	got := buf.String()
	for _, want := range []string{
		"status: model turn=1 started",
		"tool: turn=1 start tool=read",
		"approval: turn=1 requested",
		"approval: turn=1 accepted",
		"approval: turn=1 denied",
		"tool: turn=1 end tool=read",
		"status: run complete after 2 turns",
		"status: stopped after 3 turns: reached the max turn limit next: increase limits.max_turns or continue in a new prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestStreamFormatsContextDiagnosticsEvents(t *testing.T) {
	var buf bytes.Buffer
	stream := NewStream(&buf)

	stream.Emit(NewContextCompactionEvent(4, 2, 6, 1, 3, 128, true, "compacted conversation history", "user: earlier request | assistant: earlier reply"))
	stream.Emit(NewContextBudgetEvent("project_context", 4, 900, 512, true, "trimmed extra files"))

	got := buf.String()
	for _, want := range []string{
		`context: compaction turn 4 compacted 1 turn/3 messages; retained 2 turns/6 messages; kept summary "compacted conversation history: user: earlier request | assistant: earlier reply"; summary 128 bytes; summary truncated`,
		"context: budget project context used 900/512 bytes; turn 4; truncated; notes trimmed extra files",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestStreamWritesAssistantChunksAsSingleTranscriptLine(t *testing.T) {
	var buf bytes.Buffer
	stream := NewStream(&buf)

	stream.WriteAssistantChunk("Hello")
	stream.WriteAssistantChunk(", world")
	stream.Emit(NewToolCallStartedEvent(1, "read", "call_1", nil))

	got := buf.String()
	if !strings.Contains(got, "assistant> Hello, world\n") {
		t.Fatalf("stream output %q missing streamed assistant transcript", got)
	}
	if !strings.Contains(got, "tool: turn=1 start tool=read id=call_1") {
		t.Fatalf("stream output %q missing tool event after assistant transcript", got)
	}
}

func TestSummarizeInspectionBuildsConciseSnapshot(t *testing.T) {
	events := []Event{
		NewToolCallStartedEvent(1, "read", "call_1", nil),
		NewContextCompactionEvent(2, 3, 8, 2, 4, 144, false, "compacted conversation history"),
		NewStopReasonEvent(2, "max_tokens", nil),
		NewContextBudgetEvent("project_context", 2, 800, 512, true, "trimmed extra files"),
	}

	got := SummarizeInspection(events, 2)

	if got.TotalDiagnostics != 4 {
		t.Fatalf("TotalDiagnostics = %d, want 4", got.TotalDiagnostics)
	}
	if got.ContextDiagnostics != 2 {
		t.Fatalf("ContextDiagnostics = %d, want 2", got.ContextDiagnostics)
	}
	for _, want := range []string{
		"stopped at turn 2: reached the max token limit",
		"budget project context used 800/512 bytes; turn 2; truncated; notes trimmed extra files",
		"compaction turn 2 compacted 2 turns/4 messages; retained 3 turns/8 messages; kept summary \"compacted conversation history\"; summary 144 bytes",
	} {
		if !strings.Contains(got.LastStopReason+got.LastBudget+got.LastCompaction, want) {
			t.Fatalf("snapshot = %#v, want substring %q", got, want)
		}
	}
	if len(got.Recent) != 2 {
		t.Fatalf("len(Recent) = %d, want 2", len(got.Recent))
	}
	if len(got.RecentContext) != 2 {
		t.Fatalf("len(RecentContext) = %d, want 2", len(got.RecentContext))
	}
	if strings.Contains(strings.Join(got.Recent, "\n"), "tool: turn=1 start tool=read") {
		t.Fatalf("Recent = %#v, want capped recent lines", got.Recent)
	}
}
