package agent

import "strings"

func (s *ContextStateManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	base := &s.baseContextManager
	normalizedToolName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedToolName == "read" {
		base.minVisibleTurn = s.minVisibleTurn
		shaped, _, _ := base.observeReadToolResultWithObservation(turn, content)
		return shaped
	}

	shaped := base.observeToolResult(turn, toolName, input, content)
	s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
	return shaped
}
