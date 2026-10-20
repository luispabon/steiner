package output

import "strings"

// OneshotFinishedEvent records the terminal outcome of a oneshot run.
type OneshotFinishedEvent struct {
	RunID string `json:"run_id,omitempty"`
	Err   string `json:"err,omitempty"`
}

// PhaseTransitionEvent records a oneshot phase handoff.
type PhaseTransitionEvent struct {
	RunID     string `json:"run_id,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Status    string `json:"status,omitempty"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// PhaseIndicatorEvent records a oneshot phase state update.
type PhaseIndicatorEvent struct {
	RunID   string `json:"run_id,omitempty"`
	Phase   string `json:"phase,omitempty"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// NewOneshotFinishedEvent creates a new oneshot finished event.
func NewOneshotFinishedEvent(runID string, err error) Event {
	payload := OneshotFinishedEvent{
		RunID: runID,
	}
	if err != nil {
		payload.Err = err.Error()
	}
	return newEvent(EventTypeOneshotFinished, payload)
}

// NewPhaseTransitionEvent creates a phase_transition event.
func NewPhaseTransitionEvent(runID, from, to, status, model, sessionID string) Event {
	return newEvent(EventTypePhaseTransition, PhaseTransitionEvent{
		RunID:     strings.TrimSpace(runID),
		From:      strings.TrimSpace(from),
		To:        strings.TrimSpace(to),
		Status:    strings.TrimSpace(status),
		Model:     strings.TrimSpace(model),
		SessionID: strings.TrimSpace(sessionID),
	})
}

// NewPhaseIndicatorEvent creates a phase_indicator event.
func NewPhaseIndicatorEvent(runID, phase, state, message string) Event {
	return newEvent(EventTypePhaseIndicator, PhaseIndicatorEvent{
		RunID:   strings.TrimSpace(runID),
		Phase:   strings.TrimSpace(phase),
		State:   strings.TrimSpace(state),
		Message: strings.TrimSpace(message),
	})
}
