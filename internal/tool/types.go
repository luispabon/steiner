package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ToolDef describes a callable tool and its execution policy.
//
//nolint:revive // package API keeps tool-prefixed names for compatibility.
type ToolDef struct {
	Name            string
	ExecPath        string
	Subcommand      string
	Description     string
	ParameterSchema map[string]any
	Timeout         time.Duration
	Handler         func(ctx context.Context, input map[string]any) (any, error)
}

// RetentionKindDelegateSummary identifies retained delegate summaries.
const RetentionKindDelegateSummary = "delegate_summary"

// ToolRetention carries runtime-only metadata that can be preserved alongside a
// tool result without being surfaced to providers.
//
//nolint:revive // package API keeps tool-prefixed names for compatibility.
type ToolRetention struct {
	Kind       string `json:"kind,omitempty"`
	Summary    string `json:"summary,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Status     string `json:"status,omitempty"`
	TurnCount  int    `json:"turn_count,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
}

// JSONEnvelope is the structured tool result envelope returned to providers.
type JSONEnvelope struct {
	OK     bool               `json:"ok"`
	Result any                `json:"result,omitempty"`
	Error  *JSONEnvelopeError `json:"error,omitempty"`
}

// JSONEnvelopeError describes a failed JSONEnvelope result.
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

// ToolExecutionError reports a failed tool execution with shaped output.
//
//nolint:revive // package API keeps tool-prefixed names for compatibility.
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
	if e.Output.Stdout.Summary() != "" || e.Output.Stderr.Summary() != "" {
		details := []string{}
		if e.Output.Stdout.Summary() != "" {
			details = append(details, "stdout: "+e.Output.Stdout.Summary())
		}
		if e.Output.Stderr.Summary() != "" {
			details = append(details, "stderr: "+e.Output.Stderr.Summary())
		}
		parts = append(parts, strings.Join(details, ", "))
	}
	return strings.Join(parts, ": ")
}
