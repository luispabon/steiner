package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

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
