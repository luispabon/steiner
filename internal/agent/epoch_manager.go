package agent

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// EpochManager owns masking epoch state and window-based message masking.
type EpochManager struct {
	maskingWindowTurns     int
	epochMaskBoundary      int
	epochStartTurn         int
	minVisibleTurn         int
	contextPressureTrigger func(currentTurn int, state RunState) bool
	events                 output.EventSink
}

// MaskingWindow returns the configured masking window in turns, defaulting to 5.
func (e *EpochManager) MaskingWindow() int {
	if e.maskingWindowTurns <= 0 {
		return defaultMaskingWindowTurns
	}
	return e.maskingWindowTurns
}

// MinVisibleTurn returns the lowest turn number that is fully visible in context.
func (e *EpochManager) MinVisibleTurn() int {
	return e.minVisibleTurn
}

// MaskBoundary returns the current epoch mask boundary turn.
func (e *EpochManager) MaskBoundary() int {
	return e.epochMaskBoundary
}

// UpdateMinVisibleTurn scans messages to update the minimum visible turn.
func (e *EpochManager) UpdateMinVisibleTurn(messages []Message) {
	e.minVisibleTurn = minTurnInMessages(messages)
}

// InitializeFromTurnCount initialises epoch boundaries from the loaded turn count.
// No-op if already initialised or turnCount is zero.
func (e *EpochManager) InitializeFromTurnCount(turnCount int) {
	if turnCount <= 0 {
		return
	}
	if e.epochStartTurn > 0 || e.epochMaskBoundary > 0 {
		return
	}
	window := e.MaskingWindow()
	e.epochStartTurn = turnCount
	e.epochMaskBoundary = turnCount - window
	if e.epochMaskBoundary < 0 {
		e.epochMaskBoundary = 0
	}
}

// ShouldAdvance reports whether the epoch should advance this turn.
func (e *EpochManager) ShouldAdvance(currentTurn int, state RunState) bool {
	window := e.MaskingWindow()
	if currentTurn-e.epochStartTurn >= window {
		return true
	}
	if e.contextPressureTrigger == nil {
		return false
	}
	return e.contextPressureTrigger(currentTurn, state)
}

// Advance advances the epoch boundary and returns the trigger description.
func (e *EpochManager) Advance(currentTurn int) string {
	window := e.MaskingWindow()
	trigger := "turn_count"
	if e.contextPressureTrigger != nil && currentTurn-e.epochStartTurn < window {
		trigger = "context_pressure"
	}
	e.epochMaskBoundary = currentTurn - window
	if e.epochMaskBoundary < 0 {
		e.epochMaskBoundary = 0
	}
	e.epochStartTurn = currentTurn
	return trigger
}

// ResetEpoch resets the epoch boundaries and emits a diagnostic event.
// It implements EpochResetter.
func (e *EpochManager) ResetEpoch(turn int) {
	e.epochMaskBoundary = 0
	e.epochStartTurn = turn
	e.emitResetDiagnostic(turn, "compaction")
}

// Reset resets the epoch boundaries with an explicit trigger string.
func (e *EpochManager) Reset(turn int, trigger string) {
	e.epochMaskBoundary = 0
	e.epochStartTurn = turn
	e.emitResetDiagnostic(turn, trigger)
}

// SetEventSink installs the event sink for epoch diagnostic events.
func (e *EpochManager) SetEventSink(sink output.EventSink) {
	e.events = sink
}

func (e *EpochManager) emitResetDiagnostic(turn int, trigger string) {
	if e.events == nil {
		return
	}
	emitEvent(e.events, output.NewContextMaskingEvent(
		turn,
		"",
		"reset",
		"epoch boundary reset",
		e.MaskingWindow(),
		e.epochMaskBoundary,
		e.epochStartTurn,
		0,
		trigger,
		"reset",
	))
}

func (e *EpochManager) emitMaskingDiagnostics(turn, window, previousBoundary, previousStartTurn int, trigger string, original, masked []Message) {
	if e.events == nil || len(original) == 0 || len(original) != len(masked) {
		return
	}
	newlyMaskedTurns := map[int]struct{}{}
	for i := range original {
		if strings.TrimSpace(original[i].Content) == strings.TrimSpace(masked[i].Content) {
			continue
		}
		action := "masked"
		reason := "older than masking window"
		epochStatus := "previously_masked"
		toolName := strings.TrimSpace(original[i].Name)
		switch original[i].Role {
		case MessageRoleAssistant:
			if strings.TrimSpace(masked[i].Content) != "" && strings.TrimSpace(masked[i].Content) != strings.TrimSpace(original[i].Content) {
				action = "trimmed"
				reason = "older assistant prose"
			}
		case MessageRoleTool:
			if toolName == "" {
				toolName = "tool"
			}
			reason = "older tool result"
		default:
			action = "masked"
		}
		if trigger != "" && original[i].Turn > 0 && original[i].Turn >= previousBoundary && original[i].Turn < e.epochMaskBoundary {
			epochStatus = "newly_masked"
			newlyMaskedTurns[original[i].Turn] = struct{}{}
		}
		notes := []string{fmt.Sprintf("message_index=%d", i)}
		if original[i].ToolCallID != "" {
			notes = append(notes, "tool_call_id="+original[i].ToolCallID)
		}
		emitEvent(e.events, output.NewContextMaskingEvent(turn, toolName, action, reason, window, e.epochMaskBoundary, e.epochStartTurn, 0, trigger, epochStatus, notes...))
	}
	if trigger != "" {
		emitEvent(e.events, output.NewContextMaskingEvent(
			turn,
			"",
			"advance",
			"epoch boundary advanced",
			window,
			e.epochMaskBoundary,
			e.epochStartTurn,
			len(newlyMaskedTurns),
			trigger,
			"advance",
			fmt.Sprintf("previous_boundary=%d", previousBoundary),
			fmt.Sprintf("previous_start_turn=%d", previousStartTurn),
		))
	}
}
