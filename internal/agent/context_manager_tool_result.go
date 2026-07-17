package agent

import "strings"

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
