package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// scopedChildCompactingBuffer builds a child buffer with the root compaction
// state active, so tests can prove child transcript events are not suppressed
// by a root compaction.
func scopedChildCompactingBuffer() *contentBuffer {
	buffer := scopedChildBuffer()
	buffer.styles = testStyles(theme.AccentAmber)
	buffer.compaction = compactionState{active: true}
	return buffer
}

// A root compaction rewrites the root prefix only, so scoped child thinking,
// assistant chunks, and the finalized message must all still reach the child
// transcript while root compaction is active. The finalized message must also
// clear the child's streamed-assistant accumulator.
func TestAppendEventScopedChildContentSurvivesRootCompaction(t *testing.T) {
	t.Parallel()
	buffer := scopedChildCompactingBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "reasoning", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hello", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, " world", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "hello world"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, thinking, kinds := collectDelegationTranscript(dd)
	wantKinds := []delegationTranscriptEntryKind{delegationTranscriptEntryThinking, delegationTranscriptEntryAssistant}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("entry kinds = %v, want %v (root compaction suppressed child content)", kinds, wantKinds)
	}
	if assistant != "hello world" {
		t.Fatalf("assistant transcript = %q, want %q", assistant, "hello world")
	}
	if thinking != "reasoning" {
		t.Fatalf("thinking transcript = %q, want %q", thinking, "reasoning")
	}
	if dd.streamedAssistantText != "" {
		t.Fatalf("streamedAssistantText = %q after final message, want reset", dd.streamedAssistantText)
	}

	rendered := stripANSI(buffer.String(80))
	if !strings.Contains(rendered, "hello world") {
		t.Fatalf("rendered output missing child assistant text during root compaction: %q", rendered)
	}
}

// With thinking hidden, a root compaction must still not drop child assistant
// output: suppressed thinking is the only difference from the visible case.
func TestAppendEventScopedChildContentWithThinkingHiddenSurvivesRootCompaction(t *testing.T) {
	t.Parallel()
	buffer := scopedChildCompactingBuffer()
	buffer.showThinking = false

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "hidden reasoning", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "answer", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "answer"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, thinking, kinds := collectDelegationTranscript(dd)
	wantKinds := []delegationTranscriptEntryKind{delegationTranscriptEntryAssistant}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("entry kinds = %v, want %v", kinds, wantKinds)
	}
	if thinking != "" {
		t.Fatalf("thinking transcript = %q, want empty while showThinking is false", thinking)
	}
	if assistant != "answer" {
		t.Fatalf("assistant transcript = %q, want %q (final message duplicated or dropped)", assistant, "answer")
	}
	if dd.streamedAssistantText != "" {
		t.Fatalf("streamedAssistantText = %q after final message, want reset", dd.streamedAssistantText)
	}
}

// Child content surviving a root compaction must not loosen root suppression:
// unscoped root thinking chunks, assistant chunks, and the finalized message
// stay dropped while compaction is active.
func TestAppendEventRootContentStillSuppressedDuringCompaction(t *testing.T) {
	t.Parallel()
	buffer := scopedChildCompactingBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "root reasoning", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "root answer", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "root answer"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "child answer", output.ChunkSourceAssistant), "child-1"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments = %d, want only the delegation segment", len(buffer.segments))
	}
	if buffer.streamBuffer != "" || buffer.hadChunks {
		t.Fatalf("root stream state = {buffer:%q hadChunks:%t}, want suppressed while compaction is active", buffer.streamBuffer, buffer.hadChunks)
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if assistant, _, _ := collectDelegationTranscript(dd); assistant != "child answer" {
		t.Fatalf("child assistant transcript = %q, want %q", assistant, "child answer")
	}
}

// A scoped child compaction summary stays inside the child card during a root
// compaction: it must not create a root compaction banner or leak its summary
// text into the root transcript.
func TestAppendEventScopedChildCompactionSummaryHiddenDuringRootCompaction(t *testing.T) {
	t.Parallel()
	buffer := scopedChildCompactingBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "info",
		CompactionCount: 1,
		SummaryTitle:    "compacted child history",
		SummaryText:     "child-only summary",
	}), "child-1"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments = %d, want only the delegation segment", len(buffer.segments))
	}
	if buffer.segments[0].compactionData != nil {
		t.Fatal("scoped child compaction created a root compaction banner")
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if dd.compactionCount != 1 {
		t.Fatalf("compactionCount = %d, want 1", dd.compactionCount)
	}
	if got, want := dd.currentOperation, "compacted child history"; got != want {
		t.Fatalf("currentOperation = %q, want %q", got, want)
	}
	rendered := stripANSI(buffer.String(80))
	if strings.Contains(rendered, "child-only summary") {
		t.Fatalf("rendered output leaked the child compaction summary: %q", rendered)
	}
}
