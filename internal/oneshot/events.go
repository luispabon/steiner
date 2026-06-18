package oneshot

import "github.com/luispabon/steiner/internal/output"

const (
	phaseTransitionStarting  = "starting"
	phaseTransitionCompleted = "completed"
	phaseTransitionFailed    = "failed"
	phaseIndicatorStarting   = "starting"
	phaseIndicatorRunning    = "running"
	phaseIndicatorCompleted  = "completed"
	phaseIndicatorBoundary   = "boundary"
	phaseIndicatorCancelled  = "cancelled"
)

func emitPhaseTransition(sink output.EventSink, runID string, from, to Phase, status, model, sessionID string) {
	if sink == nil {
		return
	}
	sink.Emit(output.NewPhaseTransitionEvent(runID, string(from), string(to), status, model, sessionID))
}

func emitPhaseIndicator(sink output.EventSink, runID string, phase Phase, state, message string) {
	if sink == nil {
		return
	}
	sink.Emit(output.NewPhaseIndicatorEvent(runID, string(phase), state, message))
}
