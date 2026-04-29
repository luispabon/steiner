package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luispabon/steiner/internal/tool"
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

// huhApprovalResponder presents a charmbracelet/huh dialog for interactive
// tool approvals.  It pauses the Bubble Tea program before showing the dialog
// and restores it after, as required by the boundary contract in
// huh_boundary.go.
//
// It maintains a session-local always-allow cache keyed by tool name.  Entries
// in the cache are never written to disk.
type huhApprovalResponder struct {
	program *tea.Program

	mu          sync.Mutex
	alwaysAllow map[string]bool
}

// newHuhApprovalResponder creates a responder backed by the given Bubble Tea
// program.  The program reference is used to pause and resume the terminal
// around huh form rendering.
func newHuhApprovalResponder(p *tea.Program) *huhApprovalResponder {
	return &huhApprovalResponder{
		program:     p,
		alwaysAllow: make(map[string]bool),
	}
}

// RequestApproval shows a huh Select dialog and sends the result on
// req.Response.
func (h *huhApprovalResponder) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	if h.program == nil {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "no terminal program available"}
		return fmt.Errorf("no terminal program available")
	}

	toolName := req.Tool.Name
	preview := req.Preview.Summary()

	// Fast path: already always-allowed for this tool.
	h.mu.Lock()
	cached := h.alwaysAllow[toolName]
	h.mu.Unlock()
	if cached {
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "always allowed"}
		return nil
	}

	choice, err := runHuhApprovalForm(ctx, h.program, toolName, preview)
	if err != nil {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: fmt.Sprintf("approval error: %v", err)}
		return fmt.Errorf("huh approval form: %w", err)
	}

	switch choice {
	case approvalChoiceAlwaysAllow:
		h.mu.Lock()
		h.alwaysAllow[toolName] = true
		h.mu.Unlock()
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "always allowed"}
	case approvalChoiceAllow:
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "approved"}
	default:
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "denied"}
	}
	return nil
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
