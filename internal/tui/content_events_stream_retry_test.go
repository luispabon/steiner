package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// retryResetBuffer builds a root transcript that renders thinking blocks so a
// discarded reasoning block is visible to assertions.
func retryResetBuffer() *contentBuffer {
	b := newOrderBuffer()
	b.showThinking = true
	return b
}

func startRootModelCall(b *contentBuffer, turn int) {
	b.AppendEvent(output.NewModelCallStartedEvent(turn, "test-model", 1))
}

func appendAssistantChunk(b *contentBuffer, turn int, text string) {
	b.AppendEvent(output.NewAssistantChunkEventWithSource(turn, text, output.ChunkSourceAssistant))
}

func appendThinkingChunk(b *contentBuffer, turn int, text string) {
	b.AppendEvent(output.NewThinkingChunkEventWithSource(turn, text, output.ChunkSourceAssistant))
}

// appendUnboundChildAssistantChunk streams an assistant chunk scoped to a child
// that no delegation card is bound to.
func appendUnboundChildAssistantChunk(b *contentBuffer, agentID, text string) {
	b.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, text, output.ChunkSourceAssistant), agentID))
}

// appendUnboundChildThinkingChunk streams a thinking chunk scoped to a child
// that no delegation card is bound to.
func appendUnboundChildThinkingChunk(b *contentBuffer, agentID, text string) {
	b.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, text, output.ChunkSourceAssistant), agentID))
}

// retryDiagnostic builds the provider retry diagnostic the agent emits when it
// discards a partially streamed attempt.
func retryDiagnostic(reset bool, agentID string) output.Event {
	ev := output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind:        output.DiagnosticKindRetry,
		Severity:    "warning",
		Message:     "retrying after partial stream",
		Attempt:     2,
		MaxAttempts: 3,
		RetryReset:  reset,
	})
	if agentID != "" {
		ev = output.WithAgentScope(ev, agentID)
	}
	return ev
}

// streamAssistantSegments returns the assistant markdown segments carrying
// their producing attempt.
func streamAssistantSegments(b *contentBuffer) []contentSegment {
	var out []contentSegment
	for _, seg := range b.segments {
		if seg.kind == segmentAssistantMarkdown {
			out = append(out, seg)
		}
	}
	return out
}

func streamAssistantTexts(b *contentBuffer) []string {
	var out []string
	for _, seg := range streamAssistantSegments(b) {
		out = append(out, seg.text)
	}
	return out
}

func thinkingBodies(b *contentBuffer) []string {
	var out []string
	for _, seg := range b.segments {
		if seg.kind == segmentThinkingBlock && seg.thinkData != nil {
			out = append(out, seg.thinkData.body)
		}
	}
	return out
}

func renderedRetryTranscript(b *contentBuffer) string {
	return stripANSI(b.String(80))
}

// assertCollapseIndexesValid fails when a rewrite left collapse state keyed to
// a segment index that no longer exists.
func assertCollapseIndexesValid(t *testing.T, b *contentBuffer) {
	t.Helper()
	for index := range b.collapseState {
		if index < 0 || index >= len(b.segments) {
			t.Errorf("collapseState keyed at %d with %d segments", index, len(b.segments))
		}
	}
}

// thinkingAttempts returns the attempt carrying each thinking block.
func thinkingAttempts(b *contentBuffer) []uint64 {
	var out []uint64
	for _, seg := range b.segments {
		if seg.kind == segmentThinkingBlock && seg.thinkData != nil {
			out = append(out, seg.streamAttempt)
		}
	}
	return out
}

// streamingThinkingBodies returns the bodies of the thinking blocks still
// marked streaming.
func streamingThinkingBodies(b *contentBuffer) []string {
	var out []string
	for _, seg := range b.segments {
		if seg.kind == segmentThinkingBlock && seg.thinkData != nil && seg.thinkData.streaming {
			out = append(out, seg.thinkData.body)
		}
	}
	return out
}

// assertThinkingBlocksFinalized fails when a thinking block still renders
// streaming and expanded after the stream that wrote it moved on.
func assertThinkingBlocksFinalized(t *testing.T, b *contentBuffer) {
	t.Helper()
	if live := b.liveThinkingSegment(); live != nil {
		t.Errorf("thinking block %q still live, want every superseded block finalized", live.body)
	}
	for index, seg := range b.segments {
		if seg.kind != segmentThinkingBlock || seg.thinkData == nil {
			continue
		}
		if seg.thinkData.streaming {
			t.Errorf("thinking block %d (%q) still streaming, want it finalized", index, seg.thinkData.body)
		}
		if !seg.thinkData.collapsed {
			t.Errorf("thinking block %d (%q) expanded, want it collapsed", index, seg.thinkData.body)
		}
		if !b.collapseState[index] {
			t.Errorf("thinking block %d (%q) collapse state = false, want it collapsed", index, seg.thinkData.body)
		}
	}
}

func TestRetryDiagnosticWithoutTextKeepsActivityRetryLabel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.applyEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind: output.DiagnosticKindRetry, Severity: "warning", Message: "retrying", Attempt: 2, MaxAttempts: 5,
	}))
	m.applyEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind: output.DiagnosticKindRetry, Severity: "warning", RetryReset: true,
	}))
	if got, want := m.activity.retry, "retrying 2/5"; got != want {
		t.Errorf("activity retry label = %q, want the earlier attempt label %q", got, want)
	}
}

func TestInterruptedStreamSurvivesLaterRetryReset(t *testing.T) {
	t.Parallel()
	b := retryResetBuffer()
	startRootModelCall(b, 1)
	appendAssistantChunk(b, 1, "partial answer")
	b.AppendInterrupted()
	b.AppendEvent(retryDiagnostic(true, ""))

	if rendered := renderedRetryTranscript(b); !strings.Contains(rendered, "partial answer") {
		t.Errorf("rendered transcript = %q, want interrupted answer retained", rendered)
	}
}

func TestRetryResetDiscardsInFlightStream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func(t *testing.T, b *contentBuffer)
		check func(t *testing.T, b *contentBuffer)
	}{
		{
			name: "retry reset drops the abandoned answer and thinking",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "abandoned opening")
				appendThinkingChunk(b, 1, "abandoned reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "retried closing")
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "retried closing"))
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"retried closing"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "retried closing") {
					t.Errorf("rendered transcript = %q, want the retried answer", rendered)
				}
				if strings.Contains(rendered, "abandoned opening") || strings.Contains(rendered, "abandoned reasoning") {
					t.Errorf("rendered transcript = %q, want the abandoned attempt gone", rendered)
				}
				if got := thinkingBodies(b); len(got) != 0 {
					t.Errorf("thinking bodies = %q, want the abandoned block discarded", got)
				}
			},
		},
		{
			name: "notice mid-attempt survives a gap rewrite",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "abandoned opening")
				appendThinkingChunk(b, 1, "first reasoning")
				b.AppendEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Severity: "warning", Message: "disk almost full"}))
				appendAssistantChunk(b, 1, "abandoned closing")
				appendThinkingChunk(b, 1, "second reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "retried answer"))
			},
			check: func(t *testing.T, b *contentBuffer) {
				rendered := renderedRetryTranscript(b)
				for _, want := range []string{"disk almost full", "retried answer"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, want)
					}
				}
				for _, absent := range []string{"abandoned opening", "abandoned closing", "first reasoning", "second reasoning"} {
					if strings.Contains(rendered, absent) {
						t.Errorf("rendered transcript = %q, want %q discarded", rendered, absent)
					}
				}
				if got := thinkingBodies(b); len(got) != 0 {
					t.Errorf("thinking bodies = %q, want the streamed blocks discarded", got)
				}
				assertCollapseIndexesValid(t, b)
			},
		},
		{
			name: "child scoped reset leaves root stream untouched",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "root opening text")
				appendThinkingChunk(b, 1, "root reasoning")
				appendAssistantChunk(b, 1, "root closing text")
				b.AppendEvent(retryDiagnostic(true, "child-1"))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"root opening text"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
				if got, want := thinkingBodies(b), []string{"root reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if rendered := renderedRetryTranscript(b); !strings.Contains(rendered, "root opening text") || !strings.Contains(rendered, "root reasoning") {
					t.Errorf("rendered transcript = %q, want the live root stream still rendered", rendered)
				}
				if b.streamBuffer != "root closing text" {
					t.Errorf("stream buffer = %q, want the pending root text untouched", b.streamBuffer)
				}
				if !b.streaming || !b.hadChunks {
					t.Errorf("streaming = %v, hadChunks = %v, want the root stream still live", b.streaming, b.hadChunks)
				}
				if b.streamAttempt != 1 {
					t.Errorf("stream attempt = %d, want 1", b.streamAttempt)
				}
			},
		},
		{
			name: "retry diagnostic without reset keeps streamed content",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "par")
				appendThinkingChunk(b, 1, "reasoning")
				appendAssistantChunk(b, 1, "tial")
				b.AppendEvent(retryDiagnostic(false, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"par"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
				if got, want := thinkingBodies(b), []string{"reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if b.streamBuffer != "tial" {
					t.Errorf("stream buffer = %q, want the pending text kept", b.streamBuffer)
				}
				if !b.hadChunks {
					t.Error("hadChunks = false, want the streamed attempt recorded")
				}
				if b.streamAttempt != 1 {
					t.Errorf("stream attempt = %d, want 1", b.streamAttempt)
				}
			},
		},
		{
			name: "earlier completed call survives a later reset",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "first answer")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
				startRootModelCall(b, 2)
				appendAssistantChunk(b, 2, "par")
				appendThinkingChunk(b, 2, "second reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 2, "tial")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"first answer", "tial"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
				if got := thinkingBodies(b); len(got) != 0 {
					t.Errorf("thinking bodies = %q, want the retried call's block discarded", got)
				}
				if got, want := streamAssistantSegments(b)[0].streamAttempt, uint64(1); got != want {
					t.Errorf("first call attempt = %d, want %d", got, want)
				}
				if got, want := streamAssistantSegments(b)[1].streamAttempt, uint64(2); got != want {
					t.Errorf("second call attempt = %d, want %d", got, want)
				}
			},
		},
		{
			name: "successful retry without chunks renders the final message once",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "par")
				b.AppendEvent(retryDiagnostic(true, ""))
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "recovered answer"))
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				rendered := renderedRetryTranscript(b)
				if got := strings.Count(rendered, "recovered answer"); got != 1 {
					t.Errorf("rendered transcript = %q, want the final message exactly once", rendered)
				}
				if strings.Contains(rendered, "par") {
					t.Errorf("rendered transcript = %q, want the abandoned attempt gone", rendered)
				}
				segs := streamAssistantSegments(b)
				if len(segs) != 1 || segs[0].streamAttempt != 0 {
					t.Errorf("assistant segments = %+v, want one non-stream segment", segs)
				}
			},
		},
		{
			name: "reset before the first flush clears pending stream state",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "par")
				b.AppendEvent(retryDiagnostic(true, ""))
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "recovered answer"))
			},
			check: func(t *testing.T, b *contentBuffer) {
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "recovered answer") || strings.Contains(rendered, "par") {
					t.Errorf("rendered transcript = %q, want only the recovered answer", rendered)
				}
				if b.streaming || b.hadChunks || b.streamBuffer != "" || b.streamingPhase != "" || b.streamingSource != "" {
					t.Errorf("stream state after reset: streaming=%v hadChunks=%v buffer=%q phase=%q source=%q, want it cleared",
						b.streaming, b.hadChunks, b.streamBuffer, b.streamingPhase, b.streamingSource)
				}
				if b.streamPreview.source != "" {
					t.Errorf("stream preview source = %q, want it cleared", b.streamPreview.source)
				}
				if got, want := streamAssistantTexts(b), []string{"recovered answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
			},
		},
		{
			// Regression for #997: a reset from a root stream that never opened
			// a model call, such as a compaction summary, used to discard the
			// previous call's completed answer.
			name: "completed answer survives a reset from a later root stream",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "first answer")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
				b.AppendEvent(retryDiagnostic(true, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"first answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				if rendered := renderedRetryTranscript(b); !strings.Contains(rendered, "first answer") {
					t.Errorf("rendered transcript = %q, want the completed answer kept", rendered)
				}
				if b.modelCallActive {
					t.Error("model call still marked active, want the discard window closed by the API response")
				}
			},
		},
		{
			name: "reset without a model call start keeps streamed output",
			build: func(_ *testing.T, b *contentBuffer) {
				// A compaction summary streams through the root sink without a
				// ModelCallStarted event, so no discard window is open.
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "earlier answer"))
				appendAssistantChunk(b, 1, "compaction summary")
				b.AppendEvent(retryDiagnostic(true, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				rendered := renderedRetryTranscript(b)
				for _, want := range []string{"earlier answer", "compaction summary"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, want)
					}
				}
				if b.streamBuffer != "compaction summary" || !b.streaming || !b.hadChunks {
					t.Errorf("stream state after reset: buffer=%q streaming=%v hadChunks=%v, want the pending stream untouched",
						b.streamBuffer, b.streaming, b.hadChunks)
				}
				if b.streamPreview.source != "" && b.streamBuffer != "compaction summary" {
					t.Errorf("stream preview source = %q, want it left as is", b.streamPreview.source)
				}
			},
		},
		{
			name: "two resets within one call keep only the final answer",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "alpha")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "beta")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "gamma")
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "gamma"))
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"gamma"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "gamma") {
					t.Errorf("rendered transcript = %q, want the final answer kept", rendered)
				}
				for _, absent := range []string{"alpha", "beta"} {
					if strings.Contains(rendered, absent) {
						t.Errorf("rendered transcript = %q, want %q discarded", rendered, absent)
					}
				}
			},
		},
		{
			name: "child API response does not close the root discard window",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "par")
				b.AppendEvent(output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "", nil), "child-1"))
				b.AppendEvent(retryDiagnostic(true, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got := streamAssistantTexts(b); len(got) != 0 {
					t.Errorf("committed segments = %q, want the root attempt discarded", got)
				}
				if rendered := renderedRetryTranscript(b); strings.Contains(rendered, "par") {
					t.Errorf("rendered transcript = %q, want the child response not to shield the root attempt", rendered)
				}
				if !b.modelCallActive {
					t.Error("model call window closed by a child response, want it still open")
				}
			},
		},
		{
			// Regression for #997: a call start commits the previous call's pending
			// text under the attempt that produced it, instead of leaving it to be
			// tagged with the new attempt and discarded by a reset inside that call.
			name: "model call start commits pending text under the previous attempt",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "previous call pending text")
				startRootModelCall(b, 2)
			},
			check: func(t *testing.T, b *contentBuffer) {
				segs := streamAssistantSegments(b)
				if len(segs) != 1 {
					t.Fatalf("committed segments = %+v, want the pending text committed once", segs)
				}
				if got, want := segs[0].text, "previous call pending text"; got != want {
					t.Errorf("committed text = %q, want %q", got, want)
				}
				if got, want := segs[0].streamAttempt, uint64(1); got != want {
					t.Errorf("committed attempt = %d, want the attempt that produced it (%d)", got, want)
				}
				if b.streamBuffer != "" {
					t.Errorf("stream buffer = %q, want it emptied by the call start", b.streamBuffer)
				}
				if got, want := b.streamAttempt, uint64(2); got != want {
					t.Errorf("stream attempt = %d, want %d", got, want)
				}
				if !b.modelCallActive {
					t.Error("model call window closed, want it open for the new call")
				}
			},
		},
		{
			// Regression for #997: the committed text keeps the attempt that
			// produced it, so a reset in the new call drops only the new attempt.
			name: "retry reset in a new call drops only the new attempt",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "previous call pending text")
				startRootModelCall(b, 2)
				appendAssistantChunk(b, 2, "second call abandoned text")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 2, "second call retried text")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"previous call pending text", "second call retried text"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				segs := streamAssistantSegments(b)
				if len(segs) != 2 {
					t.Fatalf("committed segments = %+v, want one per call", segs)
				}
				if got, want := segs[0].streamAttempt, uint64(1); got != want {
					t.Errorf("first call attempt = %d, want %d", got, want)
				}
				if got, want := segs[1].streamAttempt, uint64(2); got != want {
					t.Errorf("second call attempt = %d, want %d", got, want)
				}
				rendered := renderedRetryTranscript(b)
				if strings.Contains(rendered, "second call abandoned text") {
					t.Errorf("rendered transcript = %q, want the abandoned attempt gone", rendered)
				}
				if !strings.Contains(rendered, "previous call pending text") {
					t.Errorf("rendered transcript = %q, want the previous call's text kept", rendered)
				}
			},
		},
		{
			// Regression for #997: an assistant chunk scoped to a child with no
			// delegation binding belongs to no root attempt. It must not join the
			// root's pending text, and a root reset must not discard it.
			name: "unbound child chunk survives a root retry reset",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "root abandoned opening")
				appendUnboundChildAssistantChunk(b, "ghost-child", "child visible answer")
				appendAssistantChunk(b, 1, "root abandoned closing")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "root retried answer")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"child visible answer", "root retried answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				segs := streamAssistantSegments(b)
				if len(segs) != 2 {
					t.Fatalf("committed segments = %+v, want the child text and the retried answer", segs)
				}
				if got := segs[0].streamAttempt; got != 0 {
					t.Errorf("child segment attempt = %d, want 0 so no root attempt owns it", got)
				}
				if got, want := segs[1].streamAttempt, uint64(1); got != want {
					t.Errorf("root segment attempt = %d, want %d", got, want)
				}
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "child visible answer") {
					t.Errorf("rendered transcript = %q, want the unbound child text kept", rendered)
				}
				for _, absent := range []string{"root abandoned opening", "root abandoned closing"} {
					if strings.Contains(rendered, absent) {
						t.Errorf("rendered transcript = %q, want %q discarded", rendered, absent)
					}
				}
			},
		},
		{
			name: "root reset keeps unbound child text that is still pending",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "root pending answer")
				appendUnboundChildAssistantChunk(b, "ghost-child", "child pending answer")
				b.AppendEvent(retryDiagnostic(true, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"child pending answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				if b.streamBuffer != "" {
					t.Errorf("stream buffer = %q, want it cleared by the reset", b.streamBuffer)
				}
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "child pending answer") {
					t.Errorf("rendered transcript = %q, want the pending child text kept, not discarded", rendered)
				}
				if strings.Contains(rendered, "root pending answer") {
					t.Errorf("rendered transcript = %q, want the abandoned root text gone", rendered)
				}
			},
		},
		{
			name: "unbound child thinking survives a root retry reset",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendThinkingChunk(b, 1, "root abandoned reasoning")
				appendUnboundChildThinkingChunk(b, "ghost-child", "child visible reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := thinkingBodies(b), []string{"child visible reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if b.liveThinkingSegment() != nil {
					t.Error("child thinking block still streaming, want the reset to finalize it")
				}
				for _, seg := range b.segments {
					if seg.kind == segmentThinkingBlock && seg.thinkData != nil && !seg.thinkData.collapsed {
						t.Error("child thinking block expanded, want the reset to collapse it")
					}
				}
				assertCollapseIndexesValid(t, b)
			},
		},
		{
			// Regression: a root attempt and an unbound child alternate owners, so
			// every chunk supersedes the previous live block. Each superseded block
			// must be finalized when the new one opens, otherwise the earlier ones
			// stay streaming and expanded for the rest of the turn.
			name: "interleaved root and unbound child thinking finalizes each superseded block",
			build: func(t *testing.T, b *contentBuffer) {
				t.Helper()
				startRootModelCall(b, 1)
				appendThinkingChunk(b, 1, "root first reasoning")
				appendUnboundChildThinkingChunk(b, "ghost-child", "child reasoning")
				appendThinkingChunk(b, 1, "root second reasoning")
				if got, want := streamingThinkingBodies(b), []string{"root second reasoning"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("streaming thinking mid-turn = %q, want only the newest block %q", got, want)
				}
				appendUnboundChildThinkingChunk(b, "ghost-child", "child second reasoning")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := thinkingBodies(b), []string{"root first reasoning", "child reasoning", "root second reasoning", "child second reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if got, want := thinkingAttempts(b), []uint64{1, 0, 1, 0}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking attempts = %v, want %v", got, want)
				}
				assertThinkingBlocksFinalized(t, b)
				rendered := renderedRetryTranscript(b)
				for _, body := range thinkingBodies(b) {
					if !strings.Contains(rendered, body) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, body)
					}
				}
				assertCollapseIndexesValid(t, b)
			},
		},
		{
			// Regression: the child block that a root chunk superseded keeps its
			// text, and the retry reset drops only the root attempt, leaving the
			// child block finalized rather than streaming.
			name: "root retry reset after interleaved unbound child thinking keeps the child reasoning",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendThinkingChunk(b, 1, "root abandoned reasoning")
				appendUnboundChildThinkingChunk(b, "ghost-child", "child kept reasoning")
				appendThinkingChunk(b, 1, "root more abandoned reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendThinkingChunk(b, 1, "root retried reasoning")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := thinkingBodies(b), []string{"child kept reasoning", "root retried reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if got, want := thinkingAttempts(b), []uint64{0, 1}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking attempts = %v, want %v", got, want)
				}
				assertThinkingBlocksFinalized(t, b)
				rendered := renderedRetryTranscript(b)
				for _, want := range []string{"child kept reasoning", "root retried reasoning"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, want)
					}
				}
				if strings.Contains(rendered, "abandoned") {
					t.Errorf("rendered transcript = %q, want the abandoned root blocks gone", rendered)
				}
				assertCollapseIndexesValid(t, b)
			},
		},
		{
			name: "interleaved thinking across attempts stays separated",
			build: func(_ *testing.T, b *contentBuffer) {
				startRootModelCall(b, 1)
				appendThinkingChunk(b, 1, "attempt one thinking")
				appendAssistantChunk(b, 1, "attempt one answer")
				appendThinkingChunk(b, 1, "attempt one more thinking")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendThinkingChunk(b, 1, "attempt two thinking")
				appendAssistantChunk(b, 1, "attempt two answer")
				appendThinkingChunk(b, 1, "attempt two more thinking")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"attempt two answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
				if got, want := strings.Join(thinkingBodies(b), ""), "attempt two thinkingattempt two more thinking"; got != want {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if rendered := renderedRetryTranscript(b); strings.Contains(rendered, "attempt one") {
					t.Errorf("rendered transcript = %q, want the abandoned attempt absent", rendered)
				}
				assertCollapseIndexesValid(t, b)
			},
		},
		{
			name: "reset before the first flush drops the cached streaming preview",
			build: func(t *testing.T, b *contentBuffer) {
				t.Helper()
				// Hide thinking so the frame is keyed only on the stream cache: a
				// visibility toggle invalidates it for the wrong reason.
				b.showThinking = false
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "abandoned preview")
				if first := renderedRetryTranscript(b); !strings.Contains(first, "abandoned preview") {
					t.Fatalf("rendered before the reset = %q, want the streaming preview", first)
				}
				b.AppendEvent(retryDiagnostic(true, ""))
				if after := renderedRetryTranscript(b); strings.Contains(after, "abandoned preview") {
					t.Fatalf("rendered after the reset = %q, want the cached preview dropped", after)
				}
				appendAssistantChunk(b, 1, "retried answer")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"retried answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				if rendered := renderedRetryTranscript(b); strings.Contains(rendered, "abandoned preview") {
					t.Errorf("rendered transcript = %q, want the abandoned preview absent", rendered)
				}
			},
		},
		{
			name: "thinking chunk does not merge into a block from an earlier attempt",
			build: func(_ *testing.T, b *contentBuffer) {
				appendThinkingChunk(b, 1, "earlier attempt reasoning")
				startRootModelCall(b, 1)
				appendThinkingChunk(b, 1, "current attempt reasoning")
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := thinkingBodies(b), []string{"earlier attempt reasoning", "current attempt reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want two separate blocks %q", got, want)
				}
				blocks := 0
				for _, seg := range b.segments {
					if seg.kind == segmentThinkingBlock {
						blocks++
					}
				}
				if blocks != 2 {
					t.Fatalf("thinking blocks = %d, want a fresh block for the current attempt", blocks)
				}
				if got, want := b.segments[0].streamAttempt, uint64(0); got != want {
					t.Errorf("earlier block attempt = %d, want %d", got, want)
				}
				if got, want := b.segments[1].streamAttempt, uint64(1); got != want {
					t.Errorf("current block attempt = %d, want %d", got, want)
				}
			},
		},
		{
			name: "discard past a leading notice keeps the notice and renders consistently",
			build: func(t *testing.T, b *contentBuffer) {
				t.Helper()
				b.AppendEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Severity: "warning", Message: "disk almost full"}))
				startRootModelCall(b, 1)
				appendAssistantChunk(b, 1, "abandoned answer")
				appendThinkingChunk(b, 1, "abandoned reasoning")
				if before := renderedRetryTranscript(b); !strings.Contains(before, "abandoned answer") {
					t.Fatalf("rendered before the reset = %q, want the abandoned answer", before)
				}
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "retried answer")
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"retried answer"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				if got := thinkingBodies(b); len(got) != 0 {
					t.Errorf("thinking bodies = %q, want the abandoned block discarded", got)
				}
				rendered := renderedRetryTranscript(b)
				for _, want := range []string{"disk almost full", "retried answer"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, want)
					}
				}
				if strings.Contains(rendered, "abandoned") {
					t.Errorf("rendered transcript = %q, want the abandoned attempt gone", rendered)
				}
				if len(b.segmentHeights) != len(b.segments) {
					t.Errorf("segment heights = %d for %d segments, want one height per segment", len(b.segmentHeights), len(b.segments))
				}
				for i, height := range b.segmentHeights {
					if height == 0 {
						t.Errorf("segment %d rendered no lines, want a height for every visible segment", i)
					}
				}
				assertCollapseIndexesValid(t, b)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := retryResetBuffer()
			tc.build(t, b)
			tc.check(t, b)
		})
	}
}
