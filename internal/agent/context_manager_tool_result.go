package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (s *ContextStateManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	s.fileTracker.ObserveToolResult(turn, toolName, input, content)
	return content
}

func (s *ContextStateManager) observeFreshToolResult(turn int, toolName string, input map[string]any, content string, prior []Message) string {
	s.ensureDefaults()
	if !strings.EqualFold(strings.TrimSpace(toolName), "read") {
		return s.observeToolResult(turn, toolName, input, content)
	}

	if !s.readAnnotations {
		s.fileTracker.RecordRead(turn, content)
		return content
	}

	finalContent, outcome := dedupReadResult(content, prior)
	s.fileTracker.RecordRead(turn, finalContent)
	if outcome.Action == "annotated" {
		if result, ok := parseReadResult(finalContent); ok {
			emitEvent(s.events, output.NewFileAnnotationEvent(turn, result.Path, outcome.Action, outcome.Reason, outcome.PreviousTurn, result.rangeSummary()))
		}
	}
	return finalContent
}
