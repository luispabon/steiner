package interactive

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// snapshotSink is an EventSink that captures APIRequestEvent payloads into
// a SnapshotStore for later context reporting.
type snapshotSink struct {
	store *SnapshotStore
}

func (s *snapshotSink) Emit(event output.Event) {
	if payload, ok := event.Payload.(output.APIRequestEvent); ok {
		s.store.Store(RequestContextSnapshot{
			Model:       payload.Model,
			Messages:    payload.Messages,
			Tools:       payload.Tools,
			MaxTokens:   payload.MaxTokens,
			Blocks:      payload.Blocks,
			ModelBudget: payload.ModelBudget,
			AgentID:     event.Scope.AgentID,
			AgentType:   event.Scope.AgentType,
			Kind:        payload.Kind,
		})
	}
}

// approvalResponder bridges tool approval requests through the session's
// ApprovalCoordinator, enabling the TUI (or other client) to display and
// respond to approval prompts.
type approvalResponder struct {
	coordinator *ApprovalCoordinator

	mu          sync.Mutex
	alwaysAllow map[string]bool
}

func newApprovalResponder(coordinator *ApprovalCoordinator) *approvalResponder {
	return &approvalResponder{
		coordinator: coordinator,
		alwaysAllow: make(map[string]bool),
	}
}

func (h *approvalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	if h == nil || h.coordinator == nil {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval UI is unavailable"}
		return fmt.Errorf("approval UI is unavailable")
	}

	toolName := req.Tool.Name
	// MCP tools use server+tool grant key; path tools use bare tool name.
	grantKey := req.Tool.Name
	if req.Kind == tool.ApprovalKindMCP && req.MCP != nil {
		grantKey = req.MCP.Server + "\x00" + req.MCP.ToolName
	}

	if h.isAlwaysAllowed(grantKey) {
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "always allowed"}
		return nil
	}

	responseCh := h.coordinator.Begin(req.CallID, toolName, "", string(req.Kind), tool.ApprovalAgentScope(ctx))
	defer h.coordinator.Finish(responseCh)

	select {
	case submission, ok := <-responseCh:
		if !ok {
			req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval response channel closed"}
			return fmt.Errorf("approval response channel closed")
		}
		response := approvalResponseForDecision(submission.Decision)
		if submission.Decision == "always_allow" {
			h.cacheAlwaysAllow(grantKey)
		}
		req.Response <- response
		return nil
	case <-ctx.Done():
		response := tool.ApprovalResponse{Allow: false, Message: ctx.Err().Error()}
		req.Response <- response
		return ctx.Err()
	}
}

func (h *approvalResponder) isAlwaysAllowed(toolName string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.alwaysAllow[toolName]
}

func (h *approvalResponder) cacheAlwaysAllow(toolName string) {
	h.mu.Lock()
	h.alwaysAllow[toolName] = true
	h.mu.Unlock()
}

func approvalResponseForDecision(decision string) tool.ApprovalResponse {
	switch decision {
	case "always_allow":
		return tool.ApprovalResponse{Allow: true, Message: "always allowed"}
	case "allow_once":
		return tool.ApprovalResponse{Allow: true, Message: "approved"}
	default:
		return tool.ApprovalResponse{Allow: false, Message: "denied"}
	}
}

type workflowHandoffResponder struct {
	coordinator *WorkflowHandoffCoordinator
	events      output.EventSink
	session     *Session
}

func newWorkflowHandoffResponder(coordinator *WorkflowHandoffCoordinator, events output.EventSink, session *Session) *workflowHandoffResponder {
	return &workflowHandoffResponder{
		coordinator: coordinator,
		events:      events,
		session:     session,
	}
}

func (h *workflowHandoffResponder) RequestWorkflowHandoff(ctx context.Context, req tool.WorkflowHandoffRequest) (tool.WorkflowHandoffResponse, error) {
	if h == nil || h.coordinator == nil {
		return tool.WorkflowHandoffResponse{}, fmt.Errorf("workflow handoff UI is unavailable")
	}

	responseCh := h.coordinator.Begin(req)
	defer h.coordinator.Finish(responseCh)
	if h.events != nil {
		h.events.Emit(output.NewWorkflowHandoffRequestedEvent(req.Next, req.Target, req.Message, req.Submission))
	}

	select {
	case submission, ok := <-responseCh:
		if !ok {
			return tool.WorkflowHandoffResponse{}, fmt.Errorf("workflow handoff response channel closed")
		}
		if workflowHandoffAccepted(submission.Decision) {
			if h.events != nil {
				h.events.Emit(output.NewWorkflowHandoffAcceptedEvent(req.Next, req.Target, req.Message))
			}
			if h.session != nil {
				h.session.SetMode(config.ExecutionModeBuild)
			}
			return tool.WorkflowHandoffResponse{Accepted: true}, nil
		}
		if h.events != nil {
			h.events.Emit(output.NewWorkflowHandoffDeclinedEvent(req.Next, req.Target, req.Message))
		}
		return tool.WorkflowHandoffResponse{}, nil
	case <-ctx.Done():
		return tool.WorkflowHandoffResponse{}, ctx.Err()
	}
}

func workflowHandoffAccepted(decision string) bool {
	return strings.EqualFold(strings.TrimSpace(decision), "accept")
}
