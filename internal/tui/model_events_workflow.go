package tui

import "github.com/luispabon/steiner/internal/output"

// applyWorkflowHandoffEvent opens the handoff modal on a request and closes it
// on accept or decline. It reports whether the request opened the modal, so
// applyEvent returns before the shared post-apply work.
func (m *Model) applyWorkflowHandoffEvent(event output.Event, payload output.WorkflowHandoffEvent) bool {
	switch event.Type {
	case output.EventTypeWorkflowHandoffRequested:
		m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, m.workflowHandoffModelSelection(payload.Next))
		m.input.Blur()
		m.notifyBlocking("workflow handoff requested")
		m.syncViewport()
		return true
	case output.EventTypeWorkflowHandoffAccepted, output.EventTypeWorkflowHandoffDeclined:
		m.workflowHandoff = m.workflowHandoff.close()
		m.input.Focus()
	}
	return false
}
