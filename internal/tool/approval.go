package tool

import (
	"context"
)

// ApprovalResponder handles sandbox boundary violation prompts.
type ApprovalResponder interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) error
}

// ApprovalResponderFunc adapts a plain function to ApprovalResponder.
type ApprovalResponderFunc func(ctx context.Context, req ApprovalRequest) error

// RequestApproval adapts f to the ApprovalResponder interface.
func (f ApprovalResponderFunc) RequestApproval(ctx context.Context, req ApprovalRequest) error {
	return f(ctx, req)
}

// ApprovalRequest carries a sandbox boundary violation for user decision.
type ApprovalRequest struct {
	Tool              ToolDef
	CallID            string
	Input             map[string]any
	WorkDir           string
	Preview           ApprovalPreview
	DeniedPath        string // path that caused the violation (may be empty for bash)
	Reason            string // human-readable reason for the prompt
	GrantInstructions string // how to permanently allow this
	Response          chan ApprovalResponse
}

// ApprovalResponse is the user decision.
type ApprovalResponse struct {
	Allow   bool
	Message string
}
