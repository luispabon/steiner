package interactive

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
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
			Model:                 payload.Model,
			Messages:              payload.Messages,
			Tools:                 payload.Tools,
			MaxTokens:             payload.MaxTokens,
			Blocks:                payload.Blocks,
			ModelBudget:           payload.ModelBudget,
			AgentID:               event.Scope.AgentID,
			AgentType:             event.Scope.AgentType,
			Kind:                  payload.Kind,
			EstimatedPromptTokens: payload.EstimatedPromptTokens,
			RawPromptTokens:       payload.RawPromptTokens,
		})
	}
}

// approvalResponder bridges tool approval requests through the session's
// ApprovalCoordinator, enabling the TUI (or other client) to display and
// respond to approval prompts. Approval lifecycle events go to events.
type approvalResponder struct {
	coordinator *ApprovalCoordinator
	events      output.EventSink

	mu          sync.Mutex
	alwaysAllow map[string]bool
}

func newApprovalResponder(coordinator *ApprovalCoordinator, events output.EventSink) *approvalResponder {
	return &approvalResponder{
		coordinator: coordinator,
		events:      events,
		alwaysAllow: make(map[string]bool),
	}
}

// RequestApproval registers a user-input approval with the coordinator before
// its requested event is emitted, so an event subscriber can respond
// synchronously. Cached grants and an unavailable coordinator skip registration.
func (h *approvalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	if h == nil || h.coordinator == nil {
		return h.respond(ctx, req, func(context.Context, tool.ApprovalRequest) error {
			return fmt.Errorf("approval UI is unavailable")
		})
	}

	toolName := req.Tool.Name
	// MCP tools use server+tool grant key; path tools use bare tool name.
	grantKey := req.Tool.Name
	if req.Kind == tool.ApprovalKindMCP && req.MCP != nil {
		grantKey = req.MCP.Server + "\x00" + req.MCP.ToolName
	}

	if h.isAlwaysAllowed(grantKey) {
		return h.respond(ctx, req, func(_ context.Context, inner tool.ApprovalRequest) error {
			inner.Response <- tool.ApprovalResponse{Allow: true, Message: "always allowed"}
			return nil
		})
	}

	responseCh := h.coordinator.Begin(req.CallID, toolName, "", string(req.Kind), tool.ApprovalAgentScope(ctx))
	return h.respond(ctx, req, func(_ context.Context, inner tool.ApprovalRequest) error {
		// Finish runs before the eventing wrapper emits a denied event for an error return.
		defer h.coordinator.Finish(responseCh)

		select {
		case submission, ok := <-responseCh:
			if !ok {
				return fmt.Errorf("approval response channel closed")
			}
			if submission.Decision == "always_allow" {
				h.cacheAlwaysAllow(grantKey)
			}
			inner.Response <- approvalResponseForDecision(submission.Decision)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

// respond runs decide inside the eventing wrapper, which emits the requested
// event before decide and the decision event after decide returns. The eventing
// wrapper sends an error return's message to req.Response itself.
func (h *approvalResponder) respond(ctx context.Context, req tool.ApprovalRequest, decide func(context.Context, tool.ApprovalRequest) error) error {
	var events output.EventSink
	if h != nil {
		events = h.events
	}
	return agent.NewEventingApprover(events, tool.ApprovalResponderFunc(decide)).RequestApproval(ctx, req)
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
