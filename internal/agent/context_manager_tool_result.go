package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

func (s *ContextStateManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	base := &s.baseContextManager
	normalizedToolName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedToolName == "read" {
		return base.observeReadToolResult(turn, content)
	}

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
	if !s.annotationsEnabled() {
		s.fileTracker.RecordRead(turn, shaped)
		return shaped
	}

	finalContent, outcome := dedupReadResult(shaped, turn, prior)
	s.fileTracker.RecordRead(turn, finalContent)
	result, _ := parseReadResult(finalContent)
	observation := fileObservation{
		Path:         result.Path,
		Action:       outcome.Action,
		Reason:       outcome.Reason,
		PreviousRead: trackedFileRead{LastTurn: outcome.PreviousTurn},
		HadPrevious:  outcome.PreviousTurn > 0,
	}
	if outcome.Action == "annotated" {
		s.baseContextManager.emitFileAnnotationDiagnostics(turn, result, observation, shaped, finalContent)
	}
	return finalContent
}
