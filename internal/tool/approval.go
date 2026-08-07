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

// ApprovalKind distinguishes the kinds of approval requests.
type ApprovalKind string

const (
	// ApprovalKindPath is a sandbox boundary violation prompt.
	ApprovalKindPath ApprovalKind = "path"
	// ApprovalKindMCP is an MCP tool call approval prompt.
	ApprovalKindMCP ApprovalKind = "mcp"
)

// ApprovalRequest carries an approval prompt for user decision.
// Exactly one of Path or MCP is non-nil, matching Kind.
type ApprovalRequest struct {
	Tool     ToolDef
	Input    map[string]any
	CallID   string
	Response chan ApprovalResponse
	Kind     ApprovalKind

	Reason            string // human-readable reason for the prompt
	GrantInstructions string // how to permanently allow this

	Path *PathApprovalDetails // non-nil when Kind == ApprovalKindPath
	MCP  *MCPApprovalDetails  // non-nil when Kind == ApprovalKindMCP
}

// PathApprovalDetails carries a sandbox boundary violation for user decision.
type PathApprovalDetails struct {
	WorkDir    string
	Preview    ApprovalPreview
	DeniedPath string // path that caused the violation (may be empty for bash)
}

// MCPApprovalDetails carries the details of an MCP tool call approval prompt.
type MCPApprovalDetails struct {
	Server           string
	ToolName         string
	ArgumentsPreview string
}

// ApprovalResponse is the user decision.
type ApprovalResponse struct {
	Allow   bool
	Message string
}

// approvalAgentScopeKey is the context key carrying the child agent ID for
// approval events emitted while a delegated child executes a tool.
type approvalAgentScopeKey struct{}

// WithApprovalAgentScope returns a context carrying agentID so approval events
// emitted during that tool execution can be attributed to the delegated child.
// An empty agentID returns ctx unchanged.
func WithApprovalAgentScope(ctx context.Context, agentID string) context.Context {
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, approvalAgentScopeKey{}, agentID)
}

// ApprovalAgentScope returns the child agent ID attached to ctx by
// WithApprovalAgentScope, or "" when absent.
func ApprovalAgentScope(ctx context.Context) string {
	agentID, _ := ctx.Value(approvalAgentScopeKey{}).(string)
	return agentID
}
