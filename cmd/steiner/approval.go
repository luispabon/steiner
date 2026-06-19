package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// Package main owns the terminal-level approval boundary.
// Any raw-terminal confirmation flow must stay here and not leak into internal/tui.
type stdinApprovalResponder struct {
	reader *bufio.Reader
}

func (h stdinApprovalResponder) RequestApproval(_ context.Context, req tool.ApprovalRequest) error {
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
