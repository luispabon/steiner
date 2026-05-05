package tui

import (
	"strings"
)

func (b *contentBuffer) flushCompletedBlocks() {
	for {
		block, rest, ok := nextCompleteMarkdownBlock(b.streamBuffer)
		if !ok {
			return
		}
		b.appendMarkdownBlock(block)
		b.streamBuffer = rest
	}
}

func nextCompleteMarkdownBlock(buffer string) (string, string, bool) {
	if buffer == "" {
		return "", "", false
	}

	if end, ok := completeFencedBlockEnd(buffer); ok {
		return buffer[:end], buffer[end:], true
	}

	if idx := strings.Index(buffer, "\n\n"); idx >= 0 {
		end := idx + 2
		for end < len(buffer) && buffer[end] == '\n' {
			end++
		}
		return buffer[:end], buffer[end:], true
	}

	line, rest, ok := cutFirstLine(buffer)
	if !ok {
		return "", "", false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line, rest, true
	}
	if isStandaloneMarkdownLine(trimmed) && strings.HasSuffix(line, "\n") {
		return line, rest, true
	}
	return "", "", false
}

func completeFencedBlockEnd(buffer string) (int, bool) {
	line, _, ok := cutFirstLine(buffer)
	if !ok {
		return 0, false
	}
	fence := fenceDelimiter(strings.TrimSpace(line))
	if fence == "" {
		return 0, false
	}

	offset := len(line)
	for offset < len(buffer) {
		nextLine, _, ok := cutFirstLine(buffer[offset:])
		if !ok {
			return 0, false
		}
		offset += len(nextLine)
		if matchesFence(strings.TrimSpace(nextLine), fence) {
			for offset < len(buffer) && buffer[offset] == '\n' {
				offset++
			}
			return offset, true
		}
	}
	return 0, false
}

func cutFirstLine(text string) (string, string, bool) {
	if text == "" {
		return "", "", false
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx+1], text[idx+1:], true
	}
	return text, "", true
}

func fenceDelimiter(line string) string {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```"
	case strings.HasPrefix(line, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

func matchesFence(line, fence string) bool {
	return fence != "" && strings.HasPrefix(line, fence)
}

func isStandaloneMarkdownLine(line string) bool {
	if strings.HasPrefix(line, "#") {
		return true
	}
	if strings.HasPrefix(line, "> ") {
		return true
	}
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	for i := 1; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			return line[i] == '.' && i+1 < len(line) && line[i+1] == ' '
		}
	}
	return false
}

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

// isMarkdownLikeUserContent returns true when text is likely to benefit from
// glamour rendering. The heuristics are intentionally conservative so that
// plain conversational text is never accidentally rendered as markdown.
func isMarkdownLikeUserContent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	// Fenced code block
	if strings.Contains(trimmed, "```") || strings.Contains(trimmed, "~~~") {
		return true
	}
	// ATX heading at start of text or after a newline
	if strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "\n#") {
		return true
	}
	// Unordered list item: requires newline before bullet (not a lone "- foo")
	if strings.Contains(trimmed, "\n- ") || strings.Contains(trimmed, "\n* ") || strings.Contains(trimmed, "\n+ ") {
		return true
	}
	// Leading list item spanning the whole input
	if (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ")) &&
		strings.ContainsRune(trimmed, '\n') {
		return true
	}
	// Block quote
	if strings.HasPrefix(trimmed, "> ") || strings.Contains(trimmed, "\n> ") {
		return true
	}
	// Ordered list with continuation
	if strings.Contains(trimmed, "\n1. ") || strings.HasPrefix(trimmed, "1. ") {
		return true
	}
	return false
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
