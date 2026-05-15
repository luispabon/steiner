package agent

import (
	"fmt"
	"strings"
)

func (s *SmartContextManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	s.ensureDefaults()
	normalizedToolName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedToolName == "scratchpad" {
		shaped := s.baseContextManager.observeToolResult(turn, toolName, input, content)
		return s.scratchpad.IngestToolResult(turn, shaped)
	}
	if normalizedToolName == "read" {
		s.baseContextManager.minVisibleTurn = maxVisibleTurn(s.minVisibleTurn, s.epoch.MinVisibleTurn(), s.epoch.MaskBoundary())
		shaped, result, observation := s.baseContextManager.observeReadToolResultWithObservation(turn, content)
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
			path := sanitizeScratchpadPath(result.Path)
			if path != "" {
				facts = append(facts, fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
			}
		}
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	}

	shaped := s.baseContextManager.observeToolResult(turn, toolName, input, content)
	update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
	s.applyFileTrackerUpdate(update, facts)
	return shaped
}

func (s *SmartContextManager) applyFileTrackerUpdate(update workingFileUpdate, facts []string) {
	s.scratchpad.SetWorkingFile(update.Path, update.LastAction)
	s.scratchpad.AppendDecisionFacts(facts)
}

func maxVisibleTurn(turns ...int) int {
	maxTurn := 0
	for _, turn := range turns {
		if turn > maxTurn {
			maxTurn = turn
		}
	}
	return maxTurn
}
