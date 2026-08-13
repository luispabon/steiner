package tui

import (
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
		b.finalizeThinkingBlock()
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

func (b *contentBuffer) appendThinkingChunk(text string, source output.ChunkSource) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamingPhase = "thinking"
	b.streamingSource = source

	if td := b.liveThinkingSegment(); td != nil {
		td.body += text
		runes := []rune(td.body)
		if len(runes) > 80 {
			td.preview = string(runes[:80])
		} else {
			td.preview = td.body
		}
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
	preview := text
	if runes := []rune(text); len(runes) > 80 {
		preview = string(runes[:80])
	}
	b.segments = append(b.segments, contentSegment{
		kind: segmentThinkingBlock,
		thinkData: &thinkingBlockData{
			preview:   preview,
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
