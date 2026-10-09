package tui

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// scopedChildBuffer builds an empty buffer that records delegation thinking.
func scopedChildBuffer() *contentBuffer {
	return &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}
}

// collectDelegationTranscript joins the assistant and thinking bodies of a
// child transcript, in arrival order, so tests can assert on content without
// depending on how consecutive entries merge.
func collectDelegationTranscript(dd *delegationDisplayState) (assistant, thinking string, kinds []delegationTranscriptEntryKind) {
	for _, entry := range dd.entries {
		kinds = append(kinds, entry.kind)
		switch entry.kind {
		case delegationTranscriptEntryAssistant:
			assistant += entry.body
		case delegationTranscriptEntryThinking:
			thinking += entry.body
		}
	}
	return assistant, thinking, kinds
}

// Thinking chunks interleaved between streamed assistant chunks must not hide
// the streamed answer: an identical finalized message is suppressed even though
// the immediately previous transcript entry is thinking, not assistant.
func TestAppendEventScopedChildThinkingBetweenChunksDuplicateFinalSuppressed(t *testing.T) {
	t.Parallel()
	buffer := scopedChildBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "considering ", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hello", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "more", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, " world", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "hello world"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, thinking, kinds := collectDelegationTranscript(dd)
	wantKinds := []delegationTranscriptEntryKind{
		delegationTranscriptEntryThinking,
		delegationTranscriptEntryAssistant,
		delegationTranscriptEntryThinking,
		delegationTranscriptEntryAssistant,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("entry kinds = %v, want %v", kinds, wantKinds)
	}
	if assistant != "hello world" {
		t.Fatalf("assistant transcript = %q, want %q (final message duplicated streamed chunks)", assistant, "hello world")
	}
	if thinking != "considering more" {
		t.Fatalf("thinking transcript = %q, want %q (thinking must be preserved in arrival order)", thinking, "considering more")
	}
}

// Streamed-assistant tracking must reset on each finalized message so a prior
// turn's stream cannot suppress or leak into the next turn's final message.
func TestAppendEventScopedChildDuplicateFinalSuppressionResetsPerTurn(t *testing.T) {
	t.Parallel()
	buffer := scopedChildBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "one", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "plan one", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "one"), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(2, "two", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(2, "plan two", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(2, "assistant", "two"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, thinking, _ := collectDelegationTranscript(dd)
	if assistant != "onetwo" {
		t.Fatalf("assistant transcript = %q, want %q (streamed text leaked across turns)", assistant, "onetwo")
	}
	if thinking != "plan oneplan two" {
		t.Fatalf("thinking transcript = %q, want %q", thinking, "plan oneplan two")
	}
}

// A finalized message with no streamed chunks, with or without preceding
// thinking, must still append its assistant content.
func TestAppendEventScopedChildFinalMessageWithoutStreamedChunksAppends(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		thinking  string
		final     string
		wantKinds []delegationTranscriptEntryKind
	}{
		{
			name:      "final only",
			final:     "answer",
			wantKinds: []delegationTranscriptEntryKind{delegationTranscriptEntryAssistant},
		},
		{
			name:      "thinking then final",
			thinking:  "reasoning",
			final:     "answer",
			wantKinds: []delegationTranscriptEntryKind{delegationTranscriptEntryThinking, delegationTranscriptEntryAssistant},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buffer := scopedChildBuffer()
			buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
			if tc.thinking != "" {
				buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, tc.thinking, output.ChunkSourceAssistant), "child-1"))
			}
			buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", tc.final), "child-1"))

			dd := buffer.segments[0].delegData
			if dd == nil {
				t.Fatal("delegData = nil, want delegation state")
			}
			_, _, kinds := collectDelegationTranscript(dd)
			if !reflect.DeepEqual(kinds, tc.wantKinds) {
				t.Fatalf("entry kinds = %v, want %v", kinds, tc.wantKinds)
			}
			last := dd.entries[len(dd.entries)-1]
			if last.kind != delegationTranscriptEntryAssistant || last.body != tc.final {
				t.Fatalf("final entry = {kind:%v body:%q}, want assistant %q", last.kind, last.body, tc.final)
			}
		})
	}
}

// A finalized message must be suppressed against the streamed chunks even when
// the two differ only in whitespace and newlines, since the duplicate check
// compares normalizeDelegationText of both sides.
func TestAppendEventScopedChildDuplicateFinalSuppressedAcrossWhitespace(t *testing.T) {
	t.Parallel()
	buffer := scopedChildBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hello\n", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "  world", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", " hello   world\n"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, _, _ := collectDelegationTranscript(dd)
	if assistant != "hello\n  world" {
		t.Fatalf("assistant transcript = %q, want %q (whitespace-only difference duplicated the final message)", assistant, "hello\n  world")
	}
}

// Streamed-assistant tracking is isolated per child: a finalized message on one
// child is not suppressed by another child's matching streamed chunks, while
// each child still suppresses a final that matches its own stream.
func TestAppendEventScopedChildDuplicateFinalSuppressionIsolatedPerChild(t *testing.T) {
	t.Parallel()
	buffer := scopedChildBuffer()

	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "first task", "", ""))
	buffer.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-2"), "second task", "", ""))
	// Only child-1 streams assistant text.
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "shared", output.ChunkSourceAssistant), "child-1"))
	// child-2 finalizes with the same text but never streamed it: must be appended.
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "shared"), "child-2"))
	// child-1 finalizes with the text it streamed: must be suppressed.
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "shared"), "child-1"))

	child1, ok := buffer.findDelegation("child-1")
	if !ok || child1.dd == nil {
		t.Fatal("child-1 delegation not found")
	}
	child2, ok := buffer.findDelegation("child-2")
	if !ok || child2.dd == nil {
		t.Fatal("child-2 delegation not found")
	}
	if assistant, _, _ := collectDelegationTranscript(child1.dd); assistant != "shared" {
		t.Fatalf("child-1 assistant transcript = %q, want %q (final not suppressed against its own stream)", assistant, "shared")
	}
	if assistant, _, _ := collectDelegationTranscript(child2.dd); assistant != "shared" {
		t.Fatalf("child-2 assistant transcript = %q, want %q (final suppressed by another child's stream)", assistant, "shared")
	}
}
