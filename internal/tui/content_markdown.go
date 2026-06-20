package tui

import "strings"

func (b *contentBuffer) appendMarkdownBlock(block string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	b.segments = append(b.segments, contentSegment{kind: segmentAssistantMarkdown, text: block, renderDirty: true})
}
