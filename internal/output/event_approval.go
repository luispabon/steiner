package output

import "strings"

// ApprovalEvent captures approval lifecycle decisions for mutation tools.
type ApprovalEvent struct {
	Turn     int    `json:"turn"`
	Tool     string `json:"tool,omitempty"`
	CallID   string `json:"call_id,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Preview  string `json:"preview,omitempty"`
	Allowed  bool   `json:"allowed"`
	Message  string `json:"message,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Server   string `json:"server,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

// WorkflowHandoffEvent captures a workflow handoff request for later handling.
type WorkflowHandoffEvent struct {
	Next       string `json:"next,omitempty"`
	Target     string `json:"target,omitempty"`
	Message    string `json:"message,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Submission string `json:"submission,omitempty"`
}

// ApprovalEventParams holds the arguments for the approval event constructors.
type ApprovalEventParams struct {
	// Turn is the turn the approval belongs to.
	Turn int
	// Tool is the tool name the model called.
	Tool string
	// CallID is the provider tool call ID the approval applies to.
	CallID string
	// Mode is the approval reason shown to the user.
	Mode string
	// Preview is the summary of the call awaiting approval.
	Preview string
	// Message is the approval response message. Only the accepted and denied constructors use it.
	Message string
	// Kind is the approval kind, such as "path" or "mcp".
	Kind string
	// Server is the MCP server name for MCP approvals.
	Server string
	// ToolName is the MCP tool name for MCP approvals.
	ToolName string
}

// NewApprovalRequestedEvent creates a new approval requested event. It ignores p.Message.
func NewApprovalRequestedEvent(p ApprovalEventParams) Event {
	return newApprovalEvent(EventTypeApprovalRequested, p, "", false)
}

// NewApprovalAcceptedEvent creates a new approval accepted event.
func NewApprovalAcceptedEvent(p ApprovalEventParams) Event {
	return newApprovalEvent(EventTypeApprovalAccepted, p, p.Message, true)
}

// NewApprovalDeniedEvent creates a new approval denied event.
func NewApprovalDeniedEvent(p ApprovalEventParams) Event {
	return newApprovalEvent(EventTypeApprovalDenied, p, p.Message, false)
}

func newApprovalEvent(eventType string, p ApprovalEventParams, message string, allowed bool) Event {
	return newEvent(eventType, ApprovalEvent{
		Turn:     p.Turn,
		Tool:     p.Tool,
		CallID:   p.CallID,
		Mode:     p.Mode,
		Preview:  p.Preview,
		Allowed:  allowed,
		Message:  message,
		Kind:     p.Kind,
		Server:   p.Server,
		ToolName: p.ToolName,
	})
}

// NewWorkflowHandoffRequestedEvent creates a new workflow handoff request event.
func NewWorkflowHandoffRequestedEvent(next, target, message, submission string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffRequested, next, target, message, "", submission)
}

// NewWorkflowHandoffAcceptedEvent creates a workflow handoff accepted event.
func NewWorkflowHandoffAcceptedEvent(next, target, message string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffAccepted, next, target, message, "accepted", "")
}

// NewWorkflowHandoffDeclinedEvent creates a workflow handoff declined event.
func NewWorkflowHandoffDeclinedEvent(next, target, message string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffDeclined, next, target, message, "declined", "")
}

func newWorkflowHandoffEvent(eventType string, next, target, message, decision, submission string) Event {
	return newEvent(eventType, WorkflowHandoffEvent{
		Next:       strings.TrimSpace(next),
		Target:     strings.TrimSpace(target),
		Message:    strings.TrimSpace(message),
		Decision:   decision,
		Submission: strings.TrimSpace(submission),
	})
}
