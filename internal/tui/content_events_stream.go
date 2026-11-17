package tui

import (
	"log/slog"
	"runtime"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// appendModelCallStartedEvent opens a new attempt identity for a root model
// call. Every chunk of the call, a provider retried attempt included, keeps
// that identity, so a retry reset can drop exactly what the call streamed.
// Opening the call also opens the discard window: only an active root model
// call may be discarded, so a reset arriving from a stream that never announced
// itself cannot delete an earlier call's committed output.
//
// The window does not always close where the call ends: a call cancelled before
// its response arrives ends without an APIResponse. The call start therefore
// commits whatever the previous call left pending before the attempt advances,
// under the attempt that streamed it. Otherwise that text would be retagged
// with the new attempt, and a reset inside the new call would discard output
// the previous call owned.
func (b *contentBuffer) appendModelCallStartedEvent(event output.Event) {
	if event.Scope.AgentID != "" {
		return
	}
	b.finishStreaming()
	b.streamAttempt++
	b.modelCallActive = true
}

// appendAPIResponseEvent ends the provider response: it always flushes the
// pending stream, and closes the root discard window only for a root response.
// A child response runs alongside the root call and must leave that window open.
func (b *contentBuffer) appendAPIResponseEvent(event output.Event) {
	b.finishStreaming()
	if event.Scope.AgentID == "" {
		b.modelCallActive = false
	}
}

// appendStreamMarkdownBlock appends streamed assistant text tagged with the
// attempt that produced it, so a retry reset can discard it later.
func (b *contentBuffer) appendStreamMarkdownBlock(block string, attempt uint64) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	b.segments = append(b.segments, contentSegment{
		kind:          segmentAssistantMarkdown,
		text:          block,
		streamAttempt: attempt,
		renderDirty:   true,
	})
}

// flushStreamBuffer commits the pending answer text under the attempt that
// wrote it, then empties the buffer. Text written by a stream that owns no root
// attempt keeps attempt zero, so a root retry reset preserves it.
func (b *contentBuffer) flushStreamBuffer() {
	block, attempt := b.streamBuffer, b.pendingAttempt
	b.streamBuffer = ""
	b.appendStreamMarkdownBlock(block, attempt)
}

// discardInFlightStream drops the current root model call's streamed output
// after a provider retry reset, together with any pending stream state. It
// never increments the attempt: the retried attempt belongs to the same model
// call. Pending state is cleared even when no segment matched, because a reset
// can arrive before the first flush. A reset that arrives while no root model
// call is open discards nothing and clears nothing.
func (b *contentBuffer) discardInFlightStream() {
	if !b.modelCallActive {
		return
	}
	attempt := b.streamAttempt
	// Pending output that no root attempt owns, such as an unbound child chunk,
	// is not this call's to discard: commit it, and close any reasoning block it
	// left live, so the rewrite below keeps it in place.
	if b.liveThinkingSegment() != nil && b.segments[len(b.segments)-1].streamAttempt != attempt {
		b.finalizeThinkingBlock()
	}
	if b.streamBuffer != "" && b.pendingAttempt != attempt {
		b.flushStreamBuffer()
	}
	rewritten := false
	if attempt != 0 {
		kept := make([]contentSegment, 0, len(b.segments))
		oldToNew := make(map[int]int, len(b.segments))
		start := -1
		for i, seg := range b.segments {
			if seg.streamAttempt == attempt && streamedSegmentKind(seg.kind) {
				if start < 0 {
					start = i
				}
				continue
			}
			oldToNew[i] = len(kept)
			kept = append(kept, seg)
		}
		if start >= 0 {
			b.commitSegmentRewrite(kept, oldToNew, start)
			rewritten = true
		}
	}
	if !rewritten {
		// No segment held the abandoned attempt, but a rendered frame can still
		// cache its streaming preview. Drop the render caches the way Clear does.
		b.stringCacheWidth, b.stringCacheBlocks = 0, nil
		b.gen++
	}
	b.streamBuffer = ""
	b.streaming = false
	b.hadChunks = false
	b.streamingPhase = ""
	b.streamingSource = ""
	b.streamPreview.reset()
}

// streamedSegmentKind reports whether kind holds text a model stream produced.
func streamedSegmentKind(kind contentSegmentKind) bool {
	return kind == segmentAssistantMarkdown || kind == segmentThinkingBlock
}

func (b *contentBuffer) appendThinkingChunkEvent(event output.Event) {
	if b.compaction.SuppressThinking() {
		return
	}
	if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
		b.appendThinkingChunk(payload.Content, payload.Source, b.streamAttemptFor(event))
	}
}

func (b *contentBuffer) appendAssistantChunkEvent(event output.Event) {
	if b.compaction.SuppressThinking() {
		return
	}
	if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
		b.appendAssistantChunk(payload.Content, payload.Source, b.streamAttemptFor(event))
	}
}

// streamAttemptFor reports the attempt a chunk event streams under: the open
// root model call for a root event, and zero for a child event that no
// delegation could bind. An unbound child chunk is not part of the root
// response, so a root retry reset must not discard its text.
func (b *contentBuffer) streamAttemptFor(event output.Event) uint64 {
	if event.Scope.AgentID != "" {
		return 0
	}
	return b.streamAttempt
}

func (b *contentBuffer) appendAssistantMessageEvent(event output.Event) {
	if b.compaction.SuppressThinking() {
		return
	}
	if payload, ok := event.Payload.(output.AssistantMessageEvent); ok && payload.Content != "" && !b.hadChunks {
		b.finishStreaming()
		b.appendMarkdownBlock(payload.Content)
	}
	b.hadChunks = false
}

func (b *contentBuffer) finishStreaming() {
	if b.streaming {
		if td := b.liveThinkingSegment(); td != nil {
			slog.Debug("finishStreaming finalizing open thinking segment",
				"caller", callerName(2), "phase", b.streamingPhase, "source", b.streamingSource,
				"thinking_body_len", len(td.body), "pending_stream_buffer_len", len(b.streamBuffer))
		}
		b.finalizeThinkingBlock()
		b.flushStreamBuffer()
	}
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
	b.streamingSource = ""
}

// callerName reports the function name skip frames above its own call site,
// for debug logging that identifies which code path triggered a mid-stream
// finalize (see finishStreaming).
func callerName(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}
	return fn.Name()
}

func (b *contentBuffer) liveThinkingSegment() *thinkingBlockData {
	if len(b.segments) == 0 {
		return nil
	}
	last := &b.segments[len(b.segments)-1]
	if last.kind == segmentThinkingBlock && last.thinkData != nil && last.thinkData.streaming {
		return last.thinkData
	}
	return nil
}

// stripThinkingMarkers removes Codex markdown bold markers from thinking
// text so reasoning summaries render as plain text.
func stripThinkingMarkers(text string) string {
	return strings.ReplaceAll(text, "**", "")
}

func (b *contentBuffer) appendThinkingChunk(text string, source output.ChunkSource, attempt uint64) {
	if text == "" {
		return
	}

	// Flush any assistant text that arrived before this thinking chunk into
	// its own segment now, so it renders in arrival order instead of being
	// deferred until the turn's streaming buffer is flushed at turn end.
	// Key the flush decision on the current streaming phase (was it "answer" just now?)
	// rather than on whether a thinking segment happens to be open, to avoid
	// corrupting interleave order when thinking and answer phases alternate.
	if b.streamingPhase == "answer" && strings.TrimSpace(b.streamBuffer) != "" {
		b.flushStreamBuffer()
	}

	b.streaming = true
	b.streamingPhase = "thinking"
	b.streamingSource = source

	// Merge only into a live block from the same attempt: a retried attempt must
	// not inherit the abandoned attempt's reasoning text, and unbound child
	// reasoning must not merge into the root call's block.
	if td := b.liveThinkingSegment(); td != nil {
		if b.segments[len(b.segments)-1].streamAttempt == attempt {
			td.body += text
			for i := len(b.segments) - 1; i >= 0; i-- {
				if b.segments[i].kind == segmentThinkingBlock && b.segments[i].thinkData == td {
					b.segments[i].renderDirty = true
					b.invalidatePrefixIfCached(i)
					break
				}
			}
			return
		}
		// A chunk from another owner supersedes the live block. Finalize it
		// before opening the new one: only the last block can be live, so a
		// superseded block left streaming would render expanded for the rest of
		// the turn.
		b.finalizeThinkingBlock()
	}

	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentThinkingBlock,
		thinkData: &thinkingBlockData{
			collapsed: false,
			streaming: true,
			body:      text,
			source:    source,
		},
		streamAttempt: attempt,
		renderDirty:   true,
	})
	b.collapseState[idx] = false
}

func (b *contentBuffer) finalizeThinkingBlock() {
	if td := b.liveThinkingSegment(); td != nil {
		td.streaming = false
		td.collapsed = true
		if idx := len(b.segments) - 1; idx >= 0 {
			b.collapseState[idx] = true
			b.segments[idx].renderDirty = true
			b.invalidatePrefixIfCached(idx)
		}
	}
}

func (b *contentBuffer) appendAssistantChunk(text string, source output.ChunkSource, attempt uint64) {
	if text == "" {
		return
	}
	// Text already pending from a different attempt is committed first, so a
	// chunk never joins text another attempt produced under one identity.
	if b.streamBuffer != "" && b.pendingAttempt != attempt {
		b.flushStreamBuffer()
	}
	b.streaming = true
	b.hadChunks = true
	b.streamBuffer += text
	b.pendingAttempt = attempt
	b.streamingSource = source
	if b.streamingPhase == "" || b.streamingPhase == "thinking" {
		b.streamingPhase = "answer"
	}
}
