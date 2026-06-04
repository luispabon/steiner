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

func (a eventingApprover) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	preview := output.CompactJSON(req.Input)
	if preview == "" && req.Preview.Summary() != "" {
		preview = req.Preview.Summary()
	}
	emitEvent(a.sink, output.NewApprovalRequestedEvent(0, req.Tool.Name, req.Reason, preview))
	if a.inner == nil {
		response := tool.ApprovalResponse{Allow: false, Message: "approval is required"}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.Reason, preview, response.Message))
		return nil
	}
	bridge := make(chan tool.ApprovalResponse, 1)
	innerReq := req
	innerReq.Response = bridge
	if err := a.inner.RequestApproval(ctx, innerReq); err != nil {
		response := tool.ApprovalResponse{Allow: false, Message: err.Error()}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.Reason, preview, response.Message))
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
			emitEvent(a.sink, output.NewApprovalAcceptedEvent(0, req.Tool.Name, req.Reason, preview, response.Message))
		} else {
			message := strings.TrimSpace(response.Message)
			if message == "" {
				message = "tool execution denied"
			}
			emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, req.Reason, preview, message))
		}
		req.Response <- response
	}()
	return nil
}
