package tool

import "context"

// WorkflowHandoffRequest captures a workflow transition request emitted by the
// workflow_handoff built-in tool.
type WorkflowHandoffRequest struct {
	Next       string
	Target     string
	Message    string
	Submission string // resolved literal prompt text; empty means "skill invocation" convention
}

// WorkflowHandoffResponse captures the user's decision on a workflow handoff.
type WorkflowHandoffResponse struct {
	Accepted bool
}

// WorkflowHandoffResponder blocks until the caller receives a user decision for
// a workflow handoff request or the context ends.
type WorkflowHandoffResponder interface {
	RequestWorkflowHandoff(ctx context.Context, req WorkflowHandoffRequest) (WorkflowHandoffResponse, error)
}

// WorkflowHandoffTransition is returned when a workflow handoff was accepted
// and the current run must terminate cleanly for the next workflow to start.
type WorkflowHandoffTransition struct {
	Next    string `json:"next,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message,omitempty"`
}

// WorkflowHandoffAccepted is a control result used by the agent loop to stop
// the current run without appending a normal tool result message.
type WorkflowHandoffAccepted struct {
	Transition WorkflowHandoffTransition
}
