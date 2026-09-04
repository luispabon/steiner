package tui

import (
	"log/slog"
	"runtime"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendThinkingChunkEvent(event output.Event) {
	if b.compaction.SuppressThinking() {
		return
	}
	if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
		b.appendThinkingChunk(payload.Content, payload.Source)
	}
}

func (b *contentBuffer) appendAssistantChunkEvent(event output.Event) {
	if b.compaction.SuppressThinking() {
		return
	}
	if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
		b.appendAssistantChunk(payload.Content, payload.Source)
	}
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
		if strings.TrimSpace(b.streamBuffer) != "" {
			b.appendMarkdownBlock(b.streamBuffer)
		}
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

func (b *contentBuffer) appendThinkingChunk(text string, source output.ChunkSource) {
	if text == "" {
		return
	}

	// Flush any assistant text that arrived before this thinking chunk into
	// its own segment now, so it renders in arrival order instead of being
	// deferred until the turn's streaming buffer is flushed at turn end.
	if b.liveThinkingSegment() == nil && strings.TrimSpace(b.streamBuffer) != "" {
		b.appendMarkdownBlock(b.streamBuffer)
		b.streamBuffer = ""
	}

	b.streaming = true
	b.streamingPhase = "thinking"
	b.streamingSource = source

	if td := b.liveThinkingSegment(); td != nil {
		td.body += text
		for i := len(b.segments) - 1; i >= 0; i-- {
			if b.segments[i].kind == segmentThinkingBlock && b.segments[i].thinkData == td {
				b.segments[i].renderDirty = true
				b.gen++
				break
			}
		}
		return
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
		renderDirty: true,
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
			b.gen++
		}
	}
}

func (b *contentBuffer) appendAssistantChunk(text string, source output.ChunkSource) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamBuffer += text
	b.streamingSource = source
	if b.streamingPhase == "" || b.streamingPhase == "thinking" {
		b.streamingPhase = "answer"
	}
}
