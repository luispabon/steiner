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
	renderer.OnEvent(NewApprovalRequestedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "path", "", ""))
	renderer.OnEvent(NewApprovalAcceptedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "ok", "path", "", ""))
	renderer.OnEvent(NewApprovalDeniedEvent(1, "bash", "", "prompt", `{"command":"pwd"}`, "blocked", "path", "", ""))
	renderer.OnEvent(NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-1", "handoff message"))
	renderer.OnEvent(NewWorkflowHandoffAcceptedEvent("implement", ".steiner/plans/step-1", "handoff message"))
	renderer.OnEvent(NewWorkflowHandoffDeclinedEvent("review", ".steiner/plans/step-2", "declined message"))
	renderer.OnEvent(NewToolCallFinishedEvent(1, "bash", "call_1", `{"exit_code":0}`, nil))
	renderer.OnEvent(NewStopReasonEvent(2, "complete", nil))
	renderer.OnEvent(NewStopReasonEvent(3, "max_turns", nil))
	renderer.OnEvent(NewStopReasonEvent(4, "workflow_handoff", nil))

	got := buf.String()
	for _, want := range []string{
		"status: model turn=1 started",
		"tool: turn=1 start tool=bash",
		"approval: turn=1 requested tool=write mode=prompt args={\"path\":\"note.txt\"}",
		"approval: turn=1 accepted tool=write mode=prompt args={\"path\":\"note.txt\"} message=ok",
		"approval: turn=1 denied tool=bash mode=prompt args={\"command\":\"pwd\"} message=blocked",
		"handoff: workflow handoff next=implement target=.steiner/plans/step-1 message=handoff message",
		"handoff: workflow handoff accepted next=implement target=.steiner/plans/step-1 message=handoff message",
		"handoff: workflow handoff declined next=review target=.steiner/plans/step-2 message=declined message",
		"tool: turn=1 end tool=bash",
		"status: run complete after 2 turns",
		"status: stopped after 3 turns: reached the max turn limit next: increase limits.max_turns or continue in a new prompt",
		"status: workflow handoff accepted next: clear the current conversation and start the next workflow",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
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
		Kind:                "compaction",
		Scope:               "conversation",
		Turn:                4,
		Severity:            "warning",
		SessionState:        "fragile",
		CompactionCount:     2,
		RestartGuidance:     "restart soon in a fresh session; repeated compaction is making retention fragile",
		RetainedTurns:       2,
		RetainedMessages:    6,
		CompactedTurns:      1,
		CompactedMessages:   3,
		SummaryTitle:        "compacted conversation history",
		SummaryPreview:      "user: earlier request | assistant: earlier reply",
		SummaryBytes:        128,
		Mode:                "emergency",
		BeforePromptTokens:  2400,
		BeforeUsagePercent:  4,
		AfterPromptTokens:   1682,
		AfterUsagePercent:   3,
		RetainedRawTurns:    2,
		SummaryTokenBudget:  128,
		ThresholdAchieved:   true,
		PromptTokens:        1682,
		ContextWindow:       65536,
		ContextUsagePercent: 3,
		CompactionThreshold: 70,
		EstimatorPadTokens:  16384,
		Status:              "ok",
		Truncated:           true,
	}))
	renderer.OnEvent(NewContextSessionHealthEvent("conversation", 4, 2, "warning", "fragile", "restart soon in a fresh session; repeated compaction is making retention fragile"))
	renderer.OnEvent(NewContextBudgetEvent("project_context", 4, 900, 512, true, "trimmed extra files"))
	renderer.OnEvent(NewContextTokenBudgetEvent("conversation", 4, 1682, 65536, 3, 70, 16384, 22162, "ok", false))
	renderer.OnEvent(NewFileAnnotationEvent(5, "note.txt", "annotated", "unchanged since turn 2", 2, "range=lines 1-3/3"))

	got := buf.String()
	for _, want := range []string{
		`context: warning: compaction #2 turn 4 compacted 1 turn/3 messages; retained 2 turns/6 messages; state fragile; restart soon in a fresh session; repeated compaction is making retention fragile; compactions 2; mode=emergency; before prompt_tokens=2400 context_usage_percent=4%; after prompt_tokens=1682 context_usage_percent=3%; retained raw turns=2; summary token budget=128; threshold achieved=true; kept summary "compacted conversation history: user: earlier request | assistant: earlier reply"; summary 128 bytes; summary truncated`,
		`context: warning: session health #2 turn 4; state fragile; restart soon in a fresh session; repeated compaction is making retention fragile; after 2 compactions`,
		"context: budget project context used 900/512 bytes; turn 4; truncated; notes trimmed extra files",
		"context: budget conversation prompt_tokens=1682 context_window=65536 context_usage_percent=3% compaction_threshold=70% estimator_pad_tokens=16384 status=ok; turn 4",
		"context: info: file annotation turn 5; annotated; path=note.txt; reason=unchanged since turn 2; notes range lines 1-3/3, previous_turn 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output %q missing %q", got, want)
		}
	}
}

func TestPlainRendererSuppressesTransportOverrideProviderDiagnostics(t *testing.T) {
	var buf bytes.Buffer
	renderer := NewPlainRenderer(&buf)

	renderer.OnEvent(NewTransportDiagnosticEvent("model-a", "configured", "effective", "fallback", "override selected"))
	renderer.OnEvent(NewProviderDiagnosticEvent(ProviderDiagnosticEvent{
		Turn:     3,
		Severity: "warning",
		Message:  "provider returned transient error, retrying turn in 5s",
	}))

	got := buf.String()
	if strings.Contains(got, "transport_override") || strings.Contains(got, "model model-a uses") {
		t.Fatalf("stream output %q unexpectedly rendered transport override diagnostic", got)
	}
	if want := "status: provider turn=3 warning message=provider returned transient error, retrying turn in 5s"; !strings.Contains(got, want) {
		t.Fatalf("stream output %q missing warning diagnostic %q", got, want)
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

	compactionPayload, ok := eventPayloadAs[ContextCompactionEvent](compaction)
	if !ok {
		t.Fatalf("payload type = %T, want ContextCompactionEvent", compaction.Payload)
	}
	if got, want := compactionPayload.Severity, "critical"; got != want {
		t.Fatalf("severity = %q, want %q", got, want)
	}
	if got, want := compactionPayload.SessionState, "likely_lossy"; got != want {
		t.Fatalf("session state = %q, want %q", got, want)
	}
	if got, want := compactionPayload.CompactionCount, 3; got != want {
		t.Fatalf("compaction count = %d, want %d", got, want)
	}
	if got, want := compactionPayload.RestartGuidance, "restart now in a new session; retained context is likely to be lossy"; got != want {
		t.Fatalf("restart guidance = %q, want %q", got, want)
	}

	sessionPayload, ok := eventPayloadAs[ContextSessionHealthEvent](sessionHealth)
	if !ok {
		t.Fatalf("payload type = %T, want ContextSessionHealthEvent", sessionHealth.Payload)
	}
	if got, want := sessionPayload.Severity, "critical"; got != want {
		t.Fatalf("severity = %q, want %q", got, want)
	}
	if got, want := sessionPayload.SessionState, "likely_lossy"; got != want {
		t.Fatalf("session state = %q, want %q", got, want)
	}
	if got, want := sessionPayload.CompactionCount, 3; got != want {
		t.Fatalf("compaction count = %d, want %d", got, want)
	}
	if got, want := sessionPayload.RestartGuidance, "restart now in a new session; retained context is likely to be lossy"; got != want {
		t.Fatalf("restart guidance = %q, want %q", got, want)
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

func eventPayloadAs[T any](event Event) (T, bool) {
	payload, ok := event.Payload.(T)
	return payload, ok
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
		NewContextDiagnosticsEvent(ContextDiagnosticsEvent{
			Kind:               "compaction",
			Scope:              "conversation",
			Turn:               2,
			Mode:               "normal",
			CompactedTurns:     2,
			CompactedMessages:  4,
			RetainedTurns:      3,
			RetainedMessages:   8,
			BeforePromptTokens: 2400,
			BeforeUsagePercent: 4,
			AfterPromptTokens:  1682,
			AfterUsagePercent:  3,
			RetainedRawTurns:   3,
			SummaryTokenBudget: 144,
			ThresholdAchieved:  true,
			SummaryTitle:       "compacted conversation history",
			SummaryBytes:       144,
		}),
		NewStopReasonEvent(2, "max_tokens", nil),
		NewContextTokenBudgetEvent("conversation", 2, 1682, 65536, 3, 70, 16384, 22162, "ok", false),
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
		"budget conversation prompt_tokens=1682 context_window=65536 context_usage_percent=3% compaction_threshold=70% estimator_pad_tokens=16384 status=ok; turn 2",
		"compaction turn 2 compacted 2 turns/4 messages; retained 3 turns/8 messages; mode=normal; before prompt_tokens=2400 context_usage_percent=4%; after prompt_tokens=1682 context_usage_percent=3%; retained raw turns=3; summary token budget=144; threshold achieved=true; kept summary \"compacted conversation history\"; summary 144 bytes",
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
