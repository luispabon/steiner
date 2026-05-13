package interactive

import (
	"context"
	"fmt"
	"sync"

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
		s.store.Store(output.RequestContextSnapshot(payload))
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
	if h.isAlwaysAllowed(toolName) {
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "always allowed"}
		return nil
	}

	responseCh := h.coordinator.Begin(toolName, string(req.Mode))
	defer h.coordinator.Finish(responseCh)

	select {
	case submission, ok := <-responseCh:
		if !ok {
			req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval response channel closed"}
			return fmt.Errorf("approval response channel closed")
		}
		response := approvalResponseForDecision(submission.Decision)
		if submission.Decision == "always_allow" {
			h.cacheAlwaysAllow(toolName)
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
