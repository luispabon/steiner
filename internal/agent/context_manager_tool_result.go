package agent

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func (s *SmartContextManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "scratchpad":
		return s.scratchpad.IngestToolResult(turn, shaped)
	case "read":
		return s.observeReadToolResult(turn, shaped)
	case "edit", "write", "apply_patch", "bash":
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	default:
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	}
}

func (s *SmartContextManager) observeReadToolResult(turn int, shaped string) string {
	result, _ := parseReadResult(shaped)
	next, observation := s.fileTracker.ObserveRead(turn, shaped, s.annotationsEnabled())
	// Visibility gate: if the original read turn is no longer safely
	// referenceable, suppress the annotation and return full content so the
	// model isn't confused by a dangling turn reference.
	if observation.Action == "annotated" {
		if observation.PreviousRead.LastTurn <= 0 {
			next = shaped
			observation.Action = "full"
			observation.Reason = "previous read turn not safely referenceable"
		} else {
			dropped := s.epoch.MinVisibleTurn() > 0 && observation.PreviousRead.LastTurn < s.epoch.MinVisibleTurn()
			masked := s.epoch.MaskBoundary() > 0 && observation.PreviousRead.LastTurn < s.epoch.MaskBoundary()
			if dropped || masked {
				next = shaped
				observation.Action = "full"
				observation.Reason = "previous read no longer visible in context"
			}
		}
	}
	s.emitFileAnnotationDiagnostics(turn, result, observation, shaped, next)
	update, facts := s.fileTracker.ObserveToolResult(turn, "read", nil, next)
	// Supplement with suppression fact that ObserveToolResult cannot infer from
	// content alone — it requires the full observation computed above.
	if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
		path := sanitizeScratchpadPath(result.Path)
		if path != "" {
			facts = append(facts, fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
		}
	}
	s.applyFileTrackerUpdate(update, facts)
	return next
}

func (s *SmartContextManager) applyFileTrackerUpdate(update workingFileUpdate, facts []string) {
	s.scratchpad.SetWorkingFile(update.Path, update.LastAction)
	s.scratchpad.AppendDecisionFacts(facts)
}

func (s *SmartContextManager) emitFileAnnotationDiagnostics(turn int, result readResult, observation fileObservation, original, shaped string) {
	if s.events == nil {
		return
	}
	if strings.TrimSpace(result.Path) == "" {
		return
	}

	notes := []string{fmt.Sprintf("range=%s", result.rangeSummary())}
	if strings.TrimSpace(original) != strings.TrimSpace(shaped) {
		notes = append(notes, "annotation produced")
	}
	notes = append(notes, observation.Notes...)

	previousTurn := 0
	if observation.HadPrevious {
		previousTurn = observation.PreviousRead.LastTurn
	}
	emitEvent(s.events, output.NewFileAnnotationEvent(
		turn,
		strings.TrimSpace(result.Path),
		observation.Action,
		observation.Reason,
		previousTurn,
		notes...,
	))
}
