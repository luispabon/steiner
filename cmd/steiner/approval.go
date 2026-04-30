package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

type channelApprovalResponder struct {
	ch chan bool
}

func (r channelApprovalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	allowed, ok := <-r.ch
	if !ok {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval channel closed"}
		return fmt.Errorf("approval channel closed")
	}
	req.Response <- tool.ApprovalResponse{Allow: allowed, Message: "approved"}
	return nil
}

type pendingTUIApproval struct {
	toolName string
	mode     string
	response chan tui.ApprovalSubmission
}

type tuiApprovalCoordinator struct {
	mu      sync.Mutex
	pending *pendingTUIApproval
}

func (c *tuiApprovalCoordinator) begin(toolName, mode string) chan tui.ApprovalSubmission {
	response := make(chan tui.ApprovalSubmission, 1)
	c.mu.Lock()
	c.pending = &pendingTUIApproval{
		toolName: toolName,
		mode:     mode,
		response: response,
	}
	c.mu.Unlock()
	return response
}

func (c *tuiApprovalCoordinator) finish(response chan tui.ApprovalSubmission) {
	c.mu.Lock()
	if c.pending != nil && c.pending.response == response {
		c.pending = nil
	}
	c.mu.Unlock()
}

func (c *tuiApprovalCoordinator) submit(submission tui.ApprovalSubmission) {
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending == nil {
		return
	}
	if pending.toolName != "" && submission.Tool != "" && submission.Tool != pending.toolName {
		return
	}
	if pending.mode != "" && submission.Mode != "" && submission.Mode != pending.mode {
		return
	}
	select {
	case pending.response <- submission:
	default:
	}
}

type tuiApprovalResponder struct {
	coordinator *tuiApprovalCoordinator

	mu          sync.Mutex
	alwaysAllow map[string]bool
}

func newTUIApprovalResponder(coordinator *tuiApprovalCoordinator) *tuiApprovalResponder {
	return &tuiApprovalResponder{
		coordinator: coordinator,
		alwaysAllow: make(map[string]bool),
	}
}

func (h *tuiApprovalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
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

	responseCh := h.coordinator.begin(toolName, string(req.Mode))
	defer h.coordinator.finish(responseCh)

	select {
	case submission, ok := <-responseCh:
		if !ok {
			req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval response channel closed"}
			return fmt.Errorf("approval response channel closed")
		}
		response := approvalResponseForDecision(submission.Decision)
		if submission.Decision == tui.ApprovalDecisionAlwaysAllow {
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

func (h *tuiApprovalResponder) isAlwaysAllowed(toolName string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.alwaysAllow[toolName]
}

func (h *tuiApprovalResponder) cacheAlwaysAllow(toolName string) {
	h.mu.Lock()
	h.alwaysAllow[toolName] = true
	h.mu.Unlock()
}

func approvalResponseForDecision(decision tui.ApprovalDecision) tool.ApprovalResponse {
	switch decision {
	case tui.ApprovalDecisionAlwaysAllow:
		return tool.ApprovalResponse{Allow: true, Message: "always allowed"}
	case tui.ApprovalDecisionAllowOnce:
		return tool.ApprovalResponse{Allow: true, Message: "approved"}
	default:
		return tool.ApprovalResponse{Allow: false, Message: "denied"}
	}
}

type stdinApprovalResponder struct {
	reader *bufio.Reader
}

func (h stdinApprovalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	if h.reader == nil {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval input is unavailable"}
		return fmt.Errorf("approval input is unavailable")
	}
	line, err := h.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: err.Error()}
		return err
	}
	if err == io.EOF && strings.TrimSpace(line) == "" {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "approval input is unavailable"}
		return fmt.Errorf("approval input is unavailable")
	}

	response := tool.ApprovalResponse{Allow: false, Message: "denied"}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		response = tool.ApprovalResponse{Allow: true, Message: "approved"}
	}
	req.Response <- response
	return nil
}
