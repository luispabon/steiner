package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlainRendererFormatsModelToolAndStopEvents(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewModelCallStartedEvent(1, "test-model", 5))
	renderer.OnEvent(NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "note.txt"}))
	renderer.OnEvent(NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`))
	renderer.OnEvent(NewApprovalAcceptedEvent(1, "write", "prompt", `{"path":"note.txt"}`, "ok"))
	renderer.OnEvent(NewApprovalDeniedEvent(1, "bash", "prompt", `{"command":"pwd"}`, "blocked"))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "read", "call_1", `{"contents":"hello"}`, nil))
	renderer.OnEvent(NewStopReasonEvent(2, "complete", nil))
	renderer.OnEvent(NewStopReasonEvent(3, "max_turns", nil))

	got := buf.String()
	for _, want := range []string{
		"status: model turn=1 started",
		"tool: turn=1 start tool=read",
		"approval: turn=1 requested tool=write mode=prompt args={\"path\":\"note.txt\"}",
		"approval: turn=1 accepted tool=write mode=prompt args={\"path\":\"note.txt\"} message=ok",
		"approval: turn=1 denied tool=bash mode=prompt args={\"command\":\"pwd\"} message=blocked",
		"tool: turn=1 end tool=read",
		"status: run complete after 2 turns",
		"status: stopped after 3 turns: reached the max turn limit next: increase limits.max_turns or continue in a new prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererFormatsCancelledStopReasonAction(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewStopReasonEvent(2, "cancelled", nil))

	if got := buf.String(); !strings.Contains(got, "status: run cancelled at turn 2 next: inspect /history for retained diagnostics or retry when you are ready to continue") {
		t.Fatalf("stream output %q missing cancelled stop reason", got)
	}
}

func TestPlainRendererFormatsContextDiagnosticsEvents(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewContextCompactionEvent(4, 2, 6, 1, 3, 128, true, "compacted conversation history", "user: earlier request | assistant: earlier reply"))
	renderer.OnEvent(NewContextBudgetEvent("project_context", 4, 900, 512, true, "trimmed extra files"))

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

func TestPlainRendererWritesAssistantChunksAsSingleTranscriptLine(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.WriteAssistantChunk("Hello")
	renderer.WriteAssistantChunk(", world")
	renderer.OnEvent(NewToolCallStartedEvent(1, "read", "call_1", nil))

	got := buf.String()
	if !strings.Contains(got, "assistant> Hello, world\n") {
		t.Fatalf("stream output %q missing streamed assistant transcript", got)
	}
	if !strings.Contains(got, "tool: turn=1 start tool=read id=call_1") {
		t.Fatalf("stream output %q missing tool event after assistant transcript", got)
	}
}

func TestEventStreamDispatchesToSubscribersAndRenderer(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)
	collector := &recordingSubscriber{}

	stream := NewEventStream(collector)
	stream.Subscribe(renderer)
	stream.Emit(NewStopReasonEvent(1, "complete", nil))

	if len(collector.events) != 1 {
		t.Fatalf("collector events = %d, want 1", len(collector.events))
	}
	if got := buf.String(); !strings.Contains(got, "status: run complete after 1 turn") {
		t.Fatalf("stream output %q missing rendered event", got)
	}
}

func TestPlainRendererExecBaseline(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewUserInputEvent("fix the bug", "exec"))
	renderer.OnEvent(NewAPIRequestEvent("test-model", nil, nil))
	renderer.OnEvent(NewAPIResponseEvent(nil, nil, "stop", nil))
	renderer.OnEvent(NewStopReasonEvent(1, "complete", nil))

	const want = "input: mode=exec content=fix the bug\napi: model=test-model\napi: finish=stop\nstatus: run complete after 1 turn\n"
	if got := buf.String(); got != want {
		t.Fatalf("exec baseline mismatch\nwant:\n%s\ngot:\n%s", want, got)
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

type recordingSubscriber struct {
	events []Event
}

func (s *recordingSubscriber) OnEvent(event Event) {
	s.events = append(s.events, event)
}
