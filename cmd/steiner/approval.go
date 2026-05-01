package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/tool"
)

type tuiApprovalResponder struct {
	coordinator *interactive.ApprovalCoordinator

	mu          sync.Mutex
	alwaysAllow map[string]bool
}

func newTUIApprovalResponder(coordinator *interactive.ApprovalCoordinator) *tuiApprovalResponder {
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
