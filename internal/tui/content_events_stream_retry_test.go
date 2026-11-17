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
				appendAssistantChunk(b, 1, "par")
				appendThinkingChunk(b, 1, "abandoned reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				appendAssistantChunk(b, 1, "tial")
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "tial"))
				b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"tial"}; !reflect.DeepEqual(got, want) {
					t.Errorf("committed segments = %q, want %q", got, want)
				}
				rendered := renderedRetryTranscript(b)
				if !strings.Contains(rendered, "tial") {
					t.Errorf("rendered transcript = %q, want the retried answer", rendered)
				}
				if strings.Contains(rendered, "par") || strings.Contains(rendered, "abandoned reasoning") {
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
				appendAssistantChunk(b, 1, "par")
				appendThinkingChunk(b, 1, "first reasoning")
				b.AppendEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Severity: "warning", Message: "disk almost full"}))
				appendAssistantChunk(b, 1, "tail")
				appendThinkingChunk(b, 1, "second reasoning")
				b.AppendEvent(retryDiagnostic(true, ""))
				b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "tial"))
			},
			check: func(t *testing.T, b *contentBuffer) {
				rendered := renderedRetryTranscript(b)
				for _, want := range []string{"disk almost full", "tial"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("rendered transcript = %q, want it to contain %q", rendered, want)
					}
				}
				for _, absent := range []string{"par", "tail", "first reasoning", "second reasoning"} {
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
				appendAssistantChunk(b, 1, "par")
				appendThinkingChunk(b, 1, "root reasoning")
				appendAssistantChunk(b, 1, "tial")
				b.AppendEvent(retryDiagnostic(true, "child-1"))
			},
			check: func(t *testing.T, b *contentBuffer) {
				if got, want := streamAssistantTexts(b), []string{"par"}; !reflect.DeepEqual(got, want) {
					t.Errorf("streamed segments = %q, want %q", got, want)
				}
				if got, want := thinkingBodies(b), []string{"root reasoning"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking bodies = %q, want %q", got, want)
				}
				if b.streamBuffer != "tial" {
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
