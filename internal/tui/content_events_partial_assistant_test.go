package tui

import (
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestPartialAssistantMessageAfterModelCallFinishedDoesNotDuplicate pins the
// run-exit partial commit reaching the TUI after the streaming call already
// ended: the finalized message must not repeat the streamed block, and it must
// clear hadChunks so a later non-streamed message still renders.
func TestPartialAssistantMessageAfterModelCallFinishedDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	b := newOrderBuffer()

	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "the report so far", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn: 1, Model: "test-model", Err: errors.New("stream interrupted after partial output"),
	}))
	b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "the report so far"))

	want := []string{"text:the report so far"}
	if got := orderedSegments(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %q, want %q (the partial must not duplicate the streamed block)", got, want)
	}
	if b.hadChunks {
		t.Fatal("hadChunks = true after the partial message, want it cleared")
	}

	b.AppendEvent(output.NewAssistantMessageEvent(2, "assistant", "a later unstreamed message"))
	want = append(want, "text:a later unstreamed message")
	if got := orderedSegments(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %q, want %q (a later non-streamed message must render)", got, want)
	}
}

// TestDelegatedChildRetryThenFailureDoesNotDuplicatePartial pins the retried
// child case: attempt one streams text, the retry streams more, the retry fails,
// and the run exit commits only the retry's text. The child transcript must not
// append that text again after the streamed attempt text it ends.
func TestDelegatedChildRetryThenFailureDoesNotDuplicatePartial(t *testing.T) {
	t.Parallel()
	b := scopedChildBuffer()
	streamErr := errors.New("stream interrupted after partial output")

	b.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	b.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "draft", output.ChunkSourceAssistant), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "", streamErr), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn: 1, Model: "test-model", Err: streamErr,
	}), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "final part", output.ChunkSourceAssistant), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "", streamErr), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn: 1, Model: "test-model", Err: streamErr,
	}), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewStopReasonEvent(1, "error", streamErr), "child-1"))
	b.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "final part"), "child-1"))

	dd := b.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	assistant, _, kinds := collectDelegationTranscript(dd)
	if assistant != "draftfinal part" {
		t.Fatalf("assistant transcript = %q, want the streamed text once without the committed retry text repeated", assistant)
	}
	wantKinds := []delegationTranscriptEntryKind{delegationTranscriptEntryAssistant}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("entry kinds = %v, want %v (the committed partial must not append an entry)", kinds, wantKinds)
	}

	b.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(2, "assistant", "a later child message"), "child-1"))
	if _, _, kinds := collectDelegationTranscript(dd); len(kinds) != 2 || kinds[1] != delegationTranscriptEntryAssistant {
		t.Fatalf("entry kinds after the later message = %v, want a new assistant entry", kinds)
	}
	if last := dd.lastEntry(); last == nil || last.body != "a later child message" {
		t.Fatalf("last entry = %+v, want the later child message", last)
	}
}
