package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

func (s *ContextStateManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	base := &s.baseContextManager
	normalizedToolName := strings.ToLower(strings.TrimSpace(toolName))
	shaped := base.observeToolResult(turn, toolName, input, content)
	if normalizedToolName == "read" {
		s.fileTracker.RecordRead(turn, shaped)
		return shaped
	}
	s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
	return shaped
}

func (s *ContextStateManager) observeFreshToolResult(turn int, toolName string, input map[string]any, content string, prior []Message) string {
	s.ensureDefaults()
	if !strings.EqualFold(strings.TrimSpace(toolName), "read") {
		return s.observeToolResult(turn, toolName, input, content)
	}

	shaped := tool.ShapeIngestedToolResult(toolName, content)
	if !s.readAnnotations {
		s.fileTracker.RecordRead(turn, shaped)
		return shaped
	}

	finalContent, _ := dedupReadResult(shaped, turn, prior)
	s.fileTracker.RecordRead(turn, finalContent)
	return finalContent
}
