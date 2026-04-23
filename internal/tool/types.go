package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

type ToolDef struct {
	Name            string
	ExecPath        string
	Subcommand      string
	Description     string
	ParameterSchema map[string]any
	Timeout         time.Duration
	Approval        config.ApprovalMode
	Handler         func(ctx context.Context, input map[string]any) (any, error)
}

type JSONEnvelope struct {
	OK     bool               `json:"ok"`
	Result any                `json:"result,omitempty"`
	Error  *JSONEnvelopeError `json:"error,omitempty"`
}

type JSONEnvelopeError struct {
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *JSONEnvelopeError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Kind) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

type ApprovalRequest struct {
	Tool     ToolDef
	Mode     config.ApprovalMode
	Input    map[string]any
	WorkDir  string
	Preview  ApprovalPreview
	Response chan ApprovalResponse
}

type ApprovalResponse struct {
	Allow   bool
	Message string
}

type ToolExecutionError struct {
	Tool     string
	Kind     string
	Message  string
	ExitCode int
	Output   ExecutionMetadata
	Details  any
}

func (e *ToolExecutionError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{"tool execution failed"}
	if strings.TrimSpace(e.Tool) != "" {
		parts = append(parts, e.Tool)
	}
	if strings.TrimSpace(e.Kind) != "" {
		parts = append(parts, e.Kind)
	}
	if strings.TrimSpace(e.Message) != "" {
		parts = append(parts, e.Message)
	}
	if e.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit=%d", e.ExitCode))
	}
	return strings.Join(parts, ": ")
}
