package tui

import "strings"

func (b *contentBuffer) appendMarkdownBlock(block string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	if isMarkdownLikeBlock(block) {
		b.segments = append(b.segments, contentSegment{kind: segmentAssistantMarkdown, text: block, renderDirty: true})
		return
	}
	b.segments = append(b.segments, contentSegment{kind: segmentAssistantProse, text: block, renderDirty: true})
}

func isMarkdownLikeBlock(block string) bool {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "```") || strings.Contains(trimmed, "~~~") {
		return true
	}
	if strings.Contains(trimmed, "\n#") || strings.HasPrefix(trimmed, "#") {
		return true
	}
	if strings.Contains(trimmed, "\n- ") || strings.Contains(trimmed, "\n* ") || strings.Contains(trimmed, "\n+ ") {
		return true
	}
	if strings.Contains(trimmed, "\n|") || strings.Contains(trimmed, "|") {
		return true
	}
	if strings.Contains(trimmed, "`") || strings.Contains(trimmed, "**") || strings.Contains(trimmed, "__") || strings.Contains(trimmed, "_") {
		return true
	}
	return false
}
