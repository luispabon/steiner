package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type eventingApprover struct {
	inner tool.ApprovalResponder
	sink  output.EventSink
}

// NewEventingApprover wraps an approver and emits approval lifecycle events.
func NewEventingApprover(sink output.EventSink, inner tool.ApprovalResponder) tool.ApprovalResponder {
	if sink == nil {
		sink = output.NoopSink{}
	}
	return eventingApprover{inner: inner, sink: sink}
}

//nolint:gocyclo // kind-guarded preview branches are prescribed by the ApprovalKind tagged union
func (a eventingApprover) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	preview := output.CompactJSON(req.Input)
	if req.Kind == tool.ApprovalKindPath && preview == "" && req.Path != nil && req.Path.Preview.Summary() != "" {
		preview = req.Path.Preview.Summary()
	}
	if req.Kind == tool.ApprovalKindMCP && preview == "" && req.MCP != nil && req.MCP.ArgumentsPreview != "" {
		preview = req.MCP.ArgumentsPreview
	}
	server := ""
	toolName := ""
	if req.Kind == tool.ApprovalKindMCP && req.MCP != nil {
		server = req.MCP.Server
		toolName = req.MCP.ToolName
	}
	emitEvent(a.sink, output.NewApprovalRequestedEvent(0, req.Tool.Name, req.CallID, req.Reason, preview, string(req.Kind), server, toolName))
	if a.inner == nil {
		response := tool.ApprovalResponse{Allow: false, Message: "approval is required"}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.CallID, req.Reason, preview, response.Message, string(req.Kind), server, toolName))
		return nil
	}
	bridge := make(chan tool.ApprovalResponse, 1)
	innerReq := req
	innerReq.Response = bridge
	if err := a.inner.RequestApproval(ctx, innerReq); err != nil {
		response := tool.ApprovalResponse{Allow: false, Message: err.Error()}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.CallID, req.Reason, preview, response.Message, string(req.Kind), server, toolName))
		return err
	}
	go func() {
		var response tool.ApprovalResponse
		select {
		case response = <-bridge:
		case <-ctx.Done():
			response = tool.ApprovalResponse{Allow: false, Message: ctx.Err().Error()}
		}
		if response.Allow {
			emitEvent(a.sink, output.NewApprovalAcceptedEvent(0, req.Tool.Name, req.CallID, req.Reason, preview, response.Message, string(req.Kind), server, toolName))
		} else {
			message := strings.TrimSpace(response.Message)
			if message == "" {
				message = "tool execution denied"
			}
			emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.CallID, req.Reason, preview, message, string(req.Kind), server, toolName))
		}
		req.Response <- response
	}()
	return nil
}
