package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

func TestPlainRendererFormatsModelToolAndStopEvents(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewModelCallStartedEvent(1, "test-model", 5))
	renderer.OnEvent(NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	renderer.OnEvent(NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`))
	renderer.OnEvent(NewApprovalAcceptedEvent(1, "write", "prompt", `{"path":"note.txt"}`, "ok"))
	renderer.OnEvent(NewApprovalDeniedEvent(1, "bash", "prompt", `{"command":"pwd"}`, "blocked"))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "bash", "call_1", `{"exit_code":0}`, nil))
	renderer.OnEvent(NewStopReasonEvent(2, "complete", nil))
	renderer.OnEvent(NewStopReasonEvent(3, "max_turns", nil))

	got := buf.String()
	for _, want := range []string{
		"status: model turn=1 started",
		"tool: turn=1 start tool=bash",
		"approval: turn=1 requested tool=write mode=prompt args={\"path\":\"note.txt\"}",
		"approval: turn=1 accepted tool=write mode=prompt args={\"path\":\"note.txt\"} message=ok",
		"approval: turn=1 denied tool=bash mode=prompt args={\"command\":\"pwd\"} message=blocked",
		"tool: turn=1 end tool=bash",
		"status: run complete after 2 turns",
		"status: stopped after 3 turns: reached the max turn limit next: increase limits.max_turns or continue in a new prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererRendersFileWritePreviewInPlainOutput(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	before := false
	renderer.OnEvent(NewToolCallStartedEventWithPreviewState(1, "write", "call_1", map[string]any{
		"path":    "notes.md",
		"content": "hello\nworld\n",
	}, &before))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "write", "call_1", `{"path":"notes.md","bytes_written":12}`, nil))

	got := buf.String()
	for _, want := range []string{
		"tool: turn=1 start tool=write id=call_1",
		"tool: turn=1 end tool=write id=call_1 result={\"path\":\"notes.md\",\"bytes_written\":12}",
		"  notes.md · new file preview · 2 lines",
		"  hello",
		"  world",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain preview output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererRendersReadFilePreviewInPlainOutput(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewToolCallStartedEvent(1, "read", "call_1", map[string]any{
		"path": "README.md",
	}))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "read", "call_1", `{"path":"README.md","output":"hello\nworld\n"}`, nil))

	got := buf.String()
	for _, want := range []string{
		"tool: turn=1 start tool=read id=call_1",
		"tool: turn=1 end tool=read id=call_1 result={\"path\":\"README.md\",\"output\":\"hello\\nworld\\n\"}",
		"  README.md · read file preview · 2 lines",
		"  hello",
		"  world",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("read preview output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererRendersEditDiffPreviewWithTheme(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf, WithTheme(Theme{
		Enabled:   true,
		Assistant: ThemeStyle{LabelPrefix: "<assistant>", LabelSuffix: "</assistant>"},
		Status:    ThemeStyle{LabelPrefix: "<status>", LabelSuffix: "</status>"},
		Tool:      ThemeStyle{LabelPrefix: "<tool>", LabelSuffix: "</tool>"},
		Approval:  ThemeStyle{LabelPrefix: "<approval>", LabelSuffix: "</approval>"},
		Error:     ThemeStyle{LabelPrefix: "<error>", LabelSuffix: "</error>"},
	}))

	renderer.OnEvent(NewToolCallStartedEvent(1, "edit", "call_1", map[string]any{
		"path":       "main.go",
		"old_string": "oldLine()",
		"new_string": "newLine()",
	}))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "edit", "call_1", `{"path":"main.go","replacements":1}`, nil))

	got := buf.String()
	for _, want := range []string{
		"<tool>tool: turn=1 start tool=edit id=call_1",
		"<tool>tool: turn=1 end tool=edit id=call_1 result={\"path\":\"main.go\",\"replacements\":1}</tool>",
		"<status>  main.go · edit diff · +1/-1</status>",
		"<status>  @@ -1,1 +1,1</status>",
		"<error>  - oldLine()</error>",
		"<approval>  + newLine()</approval>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("themed diff output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererFallsBackWithoutStructuredPreviewBody(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewToolCallFinishedEvent(1, "read", "call_1", `{"status":"ok"}`, nil))

	got := buf.String()
	if strings.Contains(got, "preview") || strings.Contains(got, "file preview") {
		t.Fatalf("fallback output %q unexpectedly rendered preview body", got)
	}
	if want := "tool: turn=1 end tool=read id=call_1 result={\"status\":\"ok\"}"; !strings.Contains(got, want) {
		t.Fatalf("fallback output %q missing %q", got, want)
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

	renderer.OnEvent(NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:              "compaction",
		Scope:             "conversation",
		Turn:              4,
		Severity:          "warning",
		SessionState:      "fragile",
		CompactionCount:   2,
		RestartGuidance:   "restart soon in a fresh session; repeated compaction is making retention fragile",
		RetainedTurns:     2,
		RetainedMessages:  6,
		CompactedTurns:    1,
		CompactedMessages: 3,
		SummaryTitle:      "compacted conversation history",
		SummaryPreview:    "user: earlier request | assistant: earlier reply",
		SummaryBytes:      128,
		Truncated:         true,
	}))
	renderer.OnEvent(NewContextSessionHealthEvent("conversation", 4, 2, "warning", "fragile", "restart soon in a fresh session; repeated compaction is making retention fragile"))
	renderer.OnEvent(NewContextBudgetEvent("project_context", 4, 900, 512, true, "trimmed extra files"))
	renderer.OnEvent(NewContextTokenBudgetEvent("conversation", 4, 1682, 4096, 16384, 22162, 65536, false))
	renderer.OnEvent(NewContextMaskingEvent(5, "read", "masked", "older tool result", 1, "message_index=7"))
	renderer.OnEvent(NewFileAnnotationEvent(5, "note.txt", "annotated", "unchanged since turn 2", 2, "range=lines 1-3/3"))
	renderer.OnEvent(NewScratchpadEvent(5, true, "[Current task state]\ngoal: keep going\nnext: finish", 0, ""))

	got := buf.String()
	for _, want := range []string{
		`context: warning: compaction #2 turn 4 compacted 1 turn/3 messages; retained 2 turns/6 messages; state fragile; restart soon in a fresh session; repeated compaction is making retention fragile; compactions 2; kept summary "compacted conversation history: user: earlier request | assistant: earlier reply"; summary 128 bytes; summary truncated`,
		`context: warning: session health #2 turn 4; state fragile; restart soon in a fresh session; repeated compaction is making retention fragile; after 2 compactions`,
		"context: budget project context used 900/512 bytes; turn 4; truncated; notes trimmed extra files",
		"context: budget conversation prompt=1682 reserve=4096 safety=16384 budget=22162/65536 tokens; turn 4",
		"context: info: masking turn 5; masked; tool=read; window=1; reason=older tool result; notes message_index 7",
		"context: info: file annotation turn 5; annotated; path=note.txt; reason=unchanged since turn 2; notes range lines 1-3/3, previous_turn 2",
		"context: info: scratchpad turn 5; parsed; content=\"[Current task state] goal: keep going next: finish\"; bytes=50",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestContextDiagnosticsPayloadCarriesEscalationFields(t *testing.T) {
	compaction := NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:            "compaction",
		Scope:           "conversation",
		Turn:            7,
		Severity:        "critical",
		SessionState:    "likely_lossy",
		CompactionCount: 3,
		RestartGuidance: "restart now in a new session; retained context is likely to be lossy",
	})
	sessionHealth := NewContextSessionHealthEvent("conversation", 7, 3, "critical", "likely_lossy", "restart now in a new session; retained context is likely to be lossy")

	for _, event := range []Event{compaction, sessionHealth} {
		payload, ok := event.Payload.(ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("payload type = %T, want ContextDiagnosticsEvent", event.Payload)
		}
		if got, want := payload.Severity, "critical"; got != want {
			t.Fatalf("severity = %q, want %q", got, want)
		}
		if got, want := payload.SessionState, "likely_lossy"; got != want {
			t.Fatalf("session state = %q, want %q", got, want)
		}
		if got, want := payload.CompactionCount, 3; got != want {
			t.Fatalf("compaction count = %d, want %d", got, want)
		}
		if got, want := payload.RestartGuidance, "restart now in a new session; retained context is likely to be lossy"; got != want {
			t.Fatalf("restart guidance = %q, want %q", got, want)
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

func TestPlainRendererLeavesStreamingInactiveWhenLabelWriteFails(t *testing.T) {
	var buf failingBuffer
	buf.failOnWrite = 1
	renderer := NewPlainRenderer(&buf)

	renderer.WriteAssistantChunk("Hello")

	if got := buf.String(); got != "" {
		t.Fatalf("buffer = %q, want empty output", got)
	}
	if renderer.streaming != "" {
		t.Fatalf("streaming = %q, want empty", renderer.streaming)
	}
	if renderer.Err() == nil {
		t.Fatal("Err() = nil, want write failure")
	}
}

func TestPlainRendererKeepsStreamingActiveWhenChunkWriteFails(t *testing.T) {
	var buf failingBuffer
	buf.failOnWrite = 2
	renderer := NewPlainRenderer(&buf)

	renderer.WriteAssistantChunk("Hello")

	if got, want := buf.String(), "assistant> "; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if renderer.streaming != ChannelAssistant {
		t.Fatalf("streaming = %q, want %q", renderer.streaming, ChannelAssistant)
	}
	if renderer.Err() == nil {
		t.Fatal("Err() = nil, want write failure")
	}
}

func TestPlainRendererKeepsStreamingActiveWhenFinishWriteFails(t *testing.T) {
	var buf failingBuffer
	buf.failOnWrite = 3
	renderer := NewPlainRenderer(&buf)

	renderer.WriteAssistantChunk("Hello")
	renderer.FinishAssistant()

	if got, want := buf.String(), "assistant> Hello"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if renderer.streaming != ChannelAssistant {
		t.Fatalf("streaming = %q, want %q", renderer.streaming, ChannelAssistant)
	}
	if renderer.Err() == nil {
		t.Fatal("Err() = nil, want write failure")
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
	renderer.OnEvent(NewAPIRequestEvent("test-model", nil, nil, nil, nil, prompt.ModelTokenBudget{}))
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
		NewContextTokenBudgetEvent("conversation", 2, 1682, 4096, 16384, 22162, 65536, false),
	}

	got := summarizeInspection(events, 2)

	if got.TotalDiagnostics != 4 {
		t.Fatalf("TotalDiagnostics = %d, want 4", got.TotalDiagnostics)
	}
	if got.ContextDiagnostics != 2 {
		t.Fatalf("ContextDiagnostics = %d, want 2", got.ContextDiagnostics)
	}
	for _, want := range []string{
		"stopped at turn 2: reached the max token limit",
		"budget conversation prompt=1682 reserve=4096 safety=16384 budget=22162/65536 tokens; turn 2",
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

func TestNewStream(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)
	if s == nil {
		t.Fatal("NewStream() returned nil")
	}
	if s.renderer == nil {
		t.Fatal("NewStream() did not attach a renderer")
	}
}

func TestEventStreamDelegateMethods(t *testing.T) {
	t.Run("Println", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.Println("hello", "world")
		if got := buf.String(); !strings.Contains(got, "hello world") {
			t.Fatalf("Println output %q missing %q", got, "hello world")
		}
	})
	t.Run("Printf", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.Printf("format %d", 42)
		if got := buf.String(); !strings.Contains(got, "format 42") {
			t.Fatalf("Printf output %q missing %q", got, "format 42")
		}
	})
	t.Run("Render", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.Render(Segment{Channel: ChannelStatus, Text: "segment-text"})
		if got := buf.String(); !strings.Contains(got, "segment-text") {
			t.Fatalf("Render output %q missing %q", got, "segment-text")
		}
	})
	t.Run("WriteAssistant", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.WriteAssistant("hello")
		if got := buf.String(); got != "hello\n" {
			t.Fatalf("WriteAssistant output = %q, want %q", got, "hello\n")
		}
	})
	t.Run("WriteAssistantChunk", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.WriteAssistantChunk("hello")
		if got := buf.String(); !strings.Contains(got, "assistant> hello") {
			t.Fatalf("WriteAssistantChunk output %q missing %q", got, "assistant> hello")
		}
	})
	t.Run("FinishAssistant", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		s.WriteAssistantChunk("hello")
		s.FinishAssistant()
		if got := buf.String(); !strings.Contains(got, "assistant> hello\n") {
			t.Fatalf("FinishAssistant output %q missing %q", got, "assistant> hello\n")
		}
	})
	t.Run("Themed", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf)
		got := s.Themed(ChannelStatus, "themed-text")
		if got != "themed-text" {
			t.Fatalf("Themed() = %q, want %q", got, "themed-text")
		}
	})
}

type failingBuffer struct {
	buf         bytes.Buffer
	failOnWrite int
	writes      int
}

func (b *failingBuffer) Write(p []byte) (int, error) {
	b.writes++
	if b.failOnWrite > 0 && b.writes == b.failOnWrite {
		return 0, errors.New("write failed")
	}
	return b.buf.Write(p)
}

func (b *failingBuffer) String() string {
	return b.buf.String()
}
