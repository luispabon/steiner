package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func (s *ContextStateManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	base := &s.baseContextManager
	shaped := base.observeToolResult(turn, toolName, input, content)
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

	finalContent, outcome := dedupReadResult(shaped, turn, prior)
	s.fileTracker.RecordRead(turn, finalContent)
	if outcome.Action == "annotated" {
		if result, ok := parseReadResult(finalContent); ok {
			emitEvent(s.events, output.NewFileAnnotationEvent(turn, result.Path, outcome.Action, outcome.Reason, outcome.PreviousTurn, result.rangeSummary()))
		}
	}
	return finalContent
}
